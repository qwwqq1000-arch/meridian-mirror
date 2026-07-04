package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync"
	"time"
)

// BodyTemplate holds the complete request body structure captured from a genuine
// Claude Code CLI request. Non-CC requests are merged with this template so every
// outgoing request matches a real CLI request exactly.
type BodyTemplate struct {
	System            []any  `json:"system"`
	ContextManagement any    `json:"context_management"`
	OutputConfig      any    `json:"output_config"`
	Diagnostics       any    `json:"diagnostics"`
	Tools             []any  `json:"tools"`
	Thinking          any    `json:"thinking"`
	Stream            bool   `json:"stream"`
	MaxTokens         int    `json:"max_tokens"`
	Version           string `json:"-"`
	Betas             string `json:"-"`
	NodeVersion       string `json:"-"`
	BuildTime         string `json:"-"`
}

type BodyTemplateCache struct {
	mu         sync.RWMutex
	tmpl       *BodyTemplate
	capturedAt time.Time
	ttl        time.Duration
}

func NewBodyTemplateCache(ttl time.Duration) *BodyTemplateCache {
	return &BodyTemplateCache{ttl: ttl}
}

func (c *BodyTemplateCache) Get() *BodyTemplate {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.tmpl == nil {
		return nil
	}
	if time.Since(c.capturedAt) > c.ttl {
		return c.tmpl
	}
	return c.tmpl
}

// LearnFromCC extracts a body template from a genuine Claude Code request body.
// Called on every CC-shaped request that passes through relay.
// Returns true only if the template was actually stored (false if the identity
// isn't recognized, so callers don't log a misleading "learned").
func (c *BodyTemplateCache) LearnFromCC(rawBody []byte, fpVersion, fpBetas, fpNodeVer string) bool {
	if c == nil {
		return false
	}
	var body map[string]any
	if json.Unmarshal(rawBody, &body) != nil {
		return false
	}
	if !hasClaudeIdentity(body["system"]) {
		return false
	}

	tmpl := &BodyTemplate{
		Stream:  true,
		Version: fpVersion,
		Betas:   fpBetas,
	}

	if sys, ok := body["system"].([]any); ok {
		tmpl.System = sys
	}
	if cm, ok := body["context_management"]; ok {
		tmpl.ContextManagement = cm
	}
	if oc, ok := body["output_config"]; ok {
		if ocMap, ok := oc.(map[string]any); ok {
			if ocMap["effort"] == "xhigh" {
				ocMap["effort"] = "high"
			}
		}
		tmpl.OutputConfig = oc
	}
	if diag, ok := body["diagnostics"]; ok {
		tmpl.Diagnostics = diag
	}
	if tools, ok := body["tools"].([]any); ok {
		tmpl.Tools = tools
	}
	if th, ok := body["thinking"]; ok {
		tmpl.Thinking = th
	}
	if s, ok := body["stream"].(bool); ok {
		tmpl.Stream = s
	}
	if mt, ok := body["max_tokens"].(float64); ok && mt > 0 {
		tmpl.MaxTokens = int(mt)
	}
	if fpNodeVer != "" {
		tmpl.NodeVersion = fpNodeVer
	}

	c.mu.Lock()
	c.tmpl = tmpl
	c.capturedAt = time.Now()
	c.mu.Unlock()
	logDD("body template learned: system=%d blocks, tools=%d, version=%s",
		len(tmpl.System), len(tmpl.Tools), tmpl.Version)
	return true
}

// MergeUserRequest reconciles a user's request against the captured CC template
// per the governing principle: template-fixed fields from the capture, model-
// derived fields forced to what real CC sends for that model, only
// model/messages/tool_choice from the user, base tools always present plus only
// CC-recognized user tools, non-CC top-level fields stripped, and a fixed CC key
// order. Values verified against 20 golden captures. No cc_prev_req.
func MergeUserRequest(userBody []byte, tmpl *BodyTemplate, userID string) ([]byte, error) {
	var user map[string]any
	if err := json.Unmarshal(userBody, &user); err != nil {
		return nil, err
	}
	model, _ := user["model"].(string)
	result := make(map[string]any, 12)

	// ① template-fixed (from the capture)
	result["system"] = append([]any{}, tmpl.System...)
	result["metadata"] = map[string]any{"user_id": userID}
	if tmpl.ContextManagement != nil {
		result["context_management"] = tmpl.ContextManagement
	}

	// ④ tools: base set always; append only CC-recognized user tools (deduped)
	baseNames := map[string]bool{}
	merged := append([]any{}, tmpl.Tools...)
	for _, tl := range tmpl.Tools {
		if tm, ok := tl.(map[string]any); ok {
			if n, ok := tm["name"].(string); ok {
				baseNames[n] = true
			}
		}
	}
	if userTools, ok := user["tools"].([]any); ok {
		for _, tl := range userTools {
			tm, ok := tl.(map[string]any)
			if !ok {
				continue
			}
			n, _ := tm["name"].(string)
			if n == "" || baseNames[n] || !isCCRecognizedTool(n, baseNames) {
				continue // dedup base, drop non-CC
			}
			delete(tm, "cache_control")
			merged = append(merged, tl)
			baseNames[n] = true
		}
	}
	if len(merged) > 0 {
		result["tools"] = merged
	}

	// ② model-derived (forced)
	result["max_tokens"] = modelMaxTokens(model)
	result["thinking"] = modelThinking(model)
	if oc := modelOutputConfig(model); oc != nil {
		result["output_config"] = oc
	}

	// ③ user passthrough (only these)
	result["model"] = user["model"]
	msgs := stripEmptyTextBlocks(user["messages"])
	// Real CC never adds extra system blocks; a non-CC agent's system prompt is
	// folded into the conversation (CLAUDE.md-style) so its instructions still
	// reach the model while the system stays exactly the 3 CC blocks.
	msgs = foldUserSystemIntoMessages(msgs, user["system"])
	stripEmptyImageBlocks(msgs)
	result["messages"] = msgs
	sanitizeToolChoice(user)
	if tc, ok := user["tool_choice"]; ok {
		result["tool_choice"] = tc
	}
	result["stream"] = true
	if s, ok := user["stream"].(bool); ok {
		result["stream"] = s
	}

	ensureCacheControl(result)
	return marshalBodyOrdered(result)
}

func modelMaxTokens(model string) int {
	if isNewModel(model) {
		return 64000
	}
	return 32000
}

// isNewModel returns true for models that use 64000 max_tokens + adaptive
// thinking (everything except haiku, per the golden per-model captures).
func isNewModel(model string) bool { return !strings.Contains(model, "haiku") }

func modelThinking(model string) map[string]any {
	if strings.Contains(model, "haiku") {
		return map[string]any{"type": "enabled", "budget_tokens": 31999, "display": "omitted"}
	}
	return map[string]any{"type": "adaptive", "display": "omitted"}
}

func modelOutputConfig(model string) map[string]any {
	if strings.Contains(model, "haiku") {
		return nil
	}
	return map[string]any{"effort": "high"}
}

// isCCRecognizedTool reports whether a user-supplied tool is one real CC would
// carry: an MCP tool (mcp__*) or a name already in the template's base set.
func isCCRecognizedTool(name string, baseNames map[string]bool) bool {
	return strings.HasPrefix(name, "mcp__") || baseNames[name]
}

// ccKeyOrder is real CC's top-level key order (verified from the golden capture).
var ccKeyOrder = []string{
	"model", "messages", "system", "tools", "metadata",
	"max_tokens", "thinking", "context_management", "output_config", "stream",
}

// marshalBodyOrdered serializes result with real CC's key order (json.Marshal on
// a map sorts keys alphabetically, which differs from real CC) and without HTML
// escaping (< > & would corrupt thinking-block signatures).
func marshalBodyOrdered(result map[string]any) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	first := true
	emit := func(k string, v any) error {
		if !first {
			buf.WriteByte(',')
		}
		first = false
		kb, _ := marshalNoEscape(k)
		buf.Write(kb)
		buf.WriteByte(':')
		vb, err := marshalNoEscape(v)
		if err != nil {
			return err
		}
		buf.Write(vb)
		return nil
	}
	seen := map[string]bool{}
	for _, k := range ccKeyOrder {
		if v, ok := result[k]; ok {
			if err := emit(k, v); err != nil {
				return nil, err
			}
			seen[k] = true
		}
	}
	for k, v := range result { // leftover keys (e.g. tool_choice) after the fixed order
		if !seen[k] {
			if err := emit(k, v); err != nil {
				return nil, err
			}
		}
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// marshalNoEscape JSON-encodes v without escaping HTML characters.
func marshalNoEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	b := buf.Bytes()
	if len(b) > 0 && b[len(b)-1] == '\n' {
		b = b[:len(b)-1]
	}
	return b, nil
}

// foldUserSystemIntoMessages moves a non-CC agent's system prompt into the
// conversation (like real CC carries CLAUDE.md-style instructions) instead of
// adding extra system blocks. It prepends the collapsed user system text as a
// text block to the first user message, so the system stays exactly the CC
// template's blocks while the agent's instructions still reach the model.
func foldUserSystemIntoMessages(msgs any, userSys any) any {
	blocks := mergeUserSystem(userSys)
	if len(blocks) == 0 {
		return msgs
	}
	parts := make([]string, 0, len(blocks))
	for _, b := range blocks {
		if m, ok := b.(map[string]any); ok {
			if t, ok := m["text"].(string); ok && t != "" {
				parts = append(parts, t)
			}
		}
	}
	text := strings.Join(parts, "\n\n")
	if text == "" {
		return msgs
	}
	sysBlock := map[string]any{"type": "text", "text": text}

	arr, ok := msgs.([]any)
	if !ok || len(arr) == 0 {
		return []any{map[string]any{"role": "user", "content": []any{sysBlock}}}
	}
	for _, m := range arr {
		mm, ok := m.(map[string]any)
		if !ok || mm["role"] != "user" {
			continue
		}
		switch content := mm["content"].(type) {
		case []any:
			mm["content"] = append([]any{sysBlock}, content...)
		case string:
			mm["content"] = []any{sysBlock, map[string]any{"type": "text", "text": content}}
		default:
			mm["content"] = []any{sysBlock}
		}
		return arr
	}
	// No user message present: prepend a new one carrying the folded system.
	return append([]any{map[string]any{"role": "user", "content": []any{sysBlock}}}, arr...)
}

// mergeUserSystem converts the user's "system" field (string or block array)
// into []any text blocks suitable for appending to the template system.
func mergeUserSystem(sys any) []any {
	switch v := sys.(type) {
	case string:
		if v == "" {
			return nil
		}
		return []any{map[string]any{"type": "text", "text": v}}
	case []any:
		if len(v) == 0 {
			return nil
		}
		return v
	}
	return nil
}

// stripEmptyTextBlocks removes {"type":"text","text":""} from message content
// arrays. Some clients send these as placeholders; the API rejects them.
func stripEmptyTextBlocks(msgs any) any {
	arr, _ := msgs.([]any)
	if arr == nil {
		return msgs
	}
	for _, m := range arr {
		mm, _ := m.(map[string]any)
		if mm == nil {
			continue
		}
		content, _ := mm["content"].([]any)
		if content == nil {
			continue
		}
		filtered := content[:0]
		for _, c := range content {
			block, _ := c.(map[string]any)
			if block != nil && block["type"] == "text" {
				text, _ := block["text"].(string)
				if text == "" {
					continue
				}
			}
			filtered = append(filtered, c)
		}
		if len(filtered) == 0 && len(content) > 0 {
			filtered = append(filtered, map[string]any{"type": "text", "text": "."})
		}
		mm["content"] = filtered
	}
	return msgs
}

// ExtractVersionFromUA parses version from "claude-cli/2.1.187 (...)" user-agent.
func ExtractVersionFromUA(ua string) string {
	if !strings.HasPrefix(ua, "claude-cli/") {
		return ""
	}
	v := strings.TrimPrefix(ua, "claude-cli/")
	if idx := strings.IndexByte(v, ' '); idx >= 0 {
		v = v[:idx]
	}
	return v
}
