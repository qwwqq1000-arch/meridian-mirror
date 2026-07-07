package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// ErrCCBodyConflict signals that a CC-shaped body had parameter conflicts
// (e.g. thinking+temperature) that were resolved in-memory, but re-marshaling
// would corrupt thinking block signatures. Relay should degrade to SDK path.
var ErrCCBodyConflict = errors.New("cc_body_conflict")

// marshalBody serializes body without escaping HTML characters (<, >, &).
// Go's json.Marshal escapes these by default, which corrupts thinking block
// signatures that contain HTML-like content.
func marshalBody(body map[string]any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(body); err != nil {
		return nil, err
	}
	b := buf.Bytes()
	// Encode appends a newline; strip it
	if len(b) > 0 && b[len(b)-1] == '\n' {
		b = b[:len(b)-1]
	}
	return b, nil
}

// Full identity used when INJECTING (only on the body-check-off path, faking a
// non-CC body as CC). Detection uses the version-stable prefix below, since the
// real first line varies across CLI versions ("…for Claude." vs "…for Claude,
// running within the Claude Agent SDK.").
const ClaudeCodeIdentity = "You are Claude Code, Anthropic's official CLI for Claude, running within the Claude Agent SDK."
const ccIdentityPrefix = "You are Claude Code, Anthropic's official CLI for Claude"

// agentSDKIdentityPrefix is the identity the headless SDK path (`claude -p`) emits
// — real CC 2.1.198 sends this, NOT the "You are Claude Code…" line. Verified from
// golden captures. LearnFromCC must accept it or it rejects the real 2.1.198 body
// and silently falls back to the stale builtin template.
const agentSDKIdentityPrefix = "You are a Claude agent, built on Anthropic's Claude Agent SDK"

// deriveUserID builds metadata.user_id exactly like real CC:
// {"device_id":<stable per-machine sha256>,"account_uuid":<real uuid>,"session_id":<session>}.
// Verified against 20 golden captures: real CC uses the real account uuid, a
// device_id that is INDEPENDENT of account_uuid, and a session_id EQUAL to the
// x-claude-code-session-id header. session_id is threaded in from the relay so it
// is the same value used in that header.
func deriveUserID(account, configDir, sessionID string) string {
	accountUUID := readAccountUUID(configDir)
	if accountUUID == "" {
		// Fallback only if the real uuid is not yet populated: derive from account,
		// but keep it distinct from device_id.
		h := sha256.Sum256([]byte("meridian-acct:" + account))
		accountUUID = fmt.Sprintf("%x-%x-%x-%x-%x", h[0:4], h[4:6], h[6:8], h[8:10], h[10:16])
	}
	dh := sha256.Sum256([]byte("meridian-device:" + machineSeed()))
	deviceID := fmt.Sprintf("%x", dh)
	return `{"device_id":"` + deviceID + `","account_uuid":"` + accountUUID + `","session_id":"` + sessionID + `"}`
}

// deviceSeedPath is the machine-level persisted device seed. It lives in the
// meridian config volume so device_id stays STABLE across container recreates
// (os.Hostname() returns the container id, which changes on every recreate)
// while being UNIQUE per machine and UNCORRELATED across the fleet (a random
// value, not derived from any shared/patterned input like hostname). Exposed as
// a var so tests can point it at a temp file.
var deviceSeedPath = "/home/claude/.config/meridian/.device_seed"

var (
	deviceSeedOnce sync.Once
	deviceSeedVal  string
)

// machineSeed returns a stable, unique, per-machine seed for device_id. It
// reads (or creates, on first use) a persisted random seed in the config
// volume, so device_id survives recreates and never collides across the fleet.
func machineSeed() string {
	deviceSeedOnce.Do(func() { deviceSeedVal = loadOrCreateDeviceSeed() })
	return deviceSeedVal
}

// loadOrCreateDeviceSeed reads the persisted seed, generating and persisting a
// random one on first use. Falls back to a fresh random value (unique but not
// persisted) if the volume is unwritable, and to os.Hostname() only if the RNG
// itself fails — so device_id is always at least unique per machine.
func loadOrCreateDeviceSeed() string {
	if b, err := os.ReadFile(deviceSeedPath); err == nil {
		if s := strings.TrimSpace(string(b)); s != "" {
			return s
		}
	}
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err == nil {
		seed := hex.EncodeToString(buf)
		if os.MkdirAll(filepath.Dir(deviceSeedPath), 0o755) == nil {
			_ = os.WriteFile(deviceSeedPath, []byte(seed), 0o600)
		}
		return seed // stable within this process even if the write failed
	}
	if h, err := os.Hostname(); err == nil && h != "" {
		return h
	}
	return "meridian-node"
}

func CloakBody(raw []byte, userID string) ([]byte, error) {
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}

	// Fixes operate on parsed body — gjson/sjson mis-match nested keys on
	// large bodies. Returns true if any field was actually changed.
	c1 := fixToolChoiceThinkingConflict(body)
	c2 := fixThinkingContextConflicts(body)
	c3 := fixInvalidEffort(body)
	changed := c1 || c2 || c3
	logDD("CloakBody: c1=%v c2=%v c3=%v changed=%v thinking=%v temp=%v oc_effort=%v top_effort=%v reasoning_effort=%v", c1, c2, c3, changed, body["thinking"], body["temperature"], func() any { if oc, ok := body["output_config"].(map[string]any); ok { return oc["effort"] }; return nil }(), body["effort"], body["reasoning_effort"])

	// Sanitize fields that ALL requests need cleaned — even CC-shaped ones.
	// These are lightweight fixes that don't alter the structural shape.
	sanitizeThinkingBlocks(body)
	metaDirty := sanitizeMetadata(body, userID)

	if hasClaudeIdentity(body["system"]) {
		// c1/c2 are structural conflicts (thinking+tool_choice, thinking+context)
		// that can't be byte-fixed. c3 (xhigh→high) and metaDirty are byte-fixable.
		if c1 || c2 {
			return nil, ErrCCBodyConflict
		}
		result := raw
		if metaDirty {
			result = stripMetadataPadBytes(result)
		}
		if c3 {
			result = fixEffortXhighBytes(result)
		}
		return forceStreamTrue(result), nil
	}

	fillDefaults(body)

	body["system"] = normalizeSystem(body["system"])
	sanitizeCacheTTL(body)
	return marshalBody(body)
}

// fillDefaults adds missing fields that a real CLI always sends.
// Without these the request either 400s (max_tokens) or looks non-CLI.
func fillDefaults(body map[string]any) {
	model, _ := body["model"].(string)

	// max_tokens — required by API, real CLI always sends it
	if _, ok := body["max_tokens"].(float64); !ok {
		body["max_tokens"] = defaultMaxTokens(model)
	}

	// stream — real CLI always sends true
	if _, ok := body["stream"].(bool); !ok {
		body["stream"] = true
	}

	// thinking — only inject when absent AND max_tokens can accommodate it.
	// If user explicitly set a small max_tokens, respect it — don't inject
	// thinking that would force us to silently inflate their budget.
	userSetMaxTokens := body["max_tokens"] != nil
	if _, ok := body["thinking"]; !ok {
		if thinkingModel(model) {
			mt := toInt(body["max_tokens"])
			budget := defaultThinkingBudget(model)
			if !userSetMaxTokens {
				// We set max_tokens ourselves — use full defaults
				body["thinking"] = map[string]any{
					"type":          "enabled",
					"budget_tokens": budget,
				}
			} else if mt > budget+1024 {
				// User's max_tokens has room for thinking
				body["thinking"] = map[string]any{
					"type":          "enabled",
					"budget_tokens": budget,
				}
			} else if mt > 2048 {
				// User's max_tokens is small — fit thinking budget inside it
				body["thinking"] = map[string]any{
					"type":          "enabled",
					"budget_tokens": mt / 2,
				}
			}
			// else: max_tokens too small for any thinking, skip injection
		}
	}

	// If user set both thinking AND max_tokens and they conflict, shrink
	// the thinking budget rather than inflating max_tokens.
	ensureThinkingFitsMaxTokens(body)
}

func toInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	}
	return 0
}

// ensureThinkingFitsMaxTokens shrinks thinking.budget_tokens to fit within
// max_tokens. Never inflates max_tokens — that would silently increase the
// user's cost. If max_tokens is too small for any thinking, remove thinking.
func ensureThinkingFitsMaxTokens(body map[string]any) {
	th, ok := body["thinking"].(map[string]any)
	if !ok {
		return
	}
	budget := toInt(th["budget_tokens"])
	if budget == 0 {
		return
	}
	mt := toInt(body["max_tokens"])
	if mt == 0 {
		return
	}
	if mt > budget {
		return // already valid
	}
	if mt > 2048 {
		th["budget_tokens"] = mt / 2
	} else {
		delete(body, "thinking")
	}
}

func defaultMaxTokens(model string) int {
	switch {
	case strings.Contains(model, "haiku"):
		return 16384
	case strings.Contains(model, "sonnet"):
		return 32000
	default:
		return 64000
	}
}

func thinkingModel(model string) bool {
	return strings.Contains(model, "opus") ||
		strings.Contains(model, "sonnet-4") ||
		strings.Contains(model, "haiku-4")
}

func defaultThinkingBudget(model string) int {
	switch {
	case strings.Contains(model, "opus"):
		return 32000
	case strings.Contains(model, "sonnet"):
		return 16000
	default:
		return 10000
	}
}

// fixThinkingContextConflicts resolves conflicts between thinking config and
// context_management/temperature. Operates on parsed body, re-serialized via json.Marshal.
//  1. thinking:disabled + clear_thinking in context_management → remove clear_thinking edits
//  2. temperature ≠ 1 + thinking enabled/adaptive → remove thinking (user chose temperature)
// fixToolChoiceThinkingConflict removes thinking when tool_choice forces a tool.
func fixToolChoiceThinkingConflict(body map[string]any) bool {
	tc, _ := body["tool_choice"].(map[string]any)
	if tc == nil {
		return false
	}
	tcType, _ := tc["type"].(string)
	if tcType != "any" && tcType != "tool" {
		return false
	}
	_, hadThinking := body["thinking"]
	delete(body, "thinking")
	if oc, ok := body["output_config"].(map[string]any); ok {
		delete(oc, "effort")
	}
	return hadThinking
}

// fixThinkingContextConflicts resolves:
//  1. thinking:disabled + clear_thinking → remove clear_thinking edits
//  2. temperature ≠ 1 + thinking enabled/adaptive → remove thinking (user chose temp)
func fixThinkingContextConflicts(body map[string]any) bool {
	changed := false
	th, _ := body["thinking"].(map[string]any)
	thinkingType := ""
	if th != nil {
		thinkingType, _ = th["type"].(string)
	}

	if thinkingType == "enabled" || thinkingType == "adaptive" {
		if temp, ok := body["temperature"].(float64); ok && temp != 1.0 {
			delete(body, "thinking")
			thinkingType = ""
			changed = true
		}
	}

	if thinkingType != "disabled" && thinkingType != "" {
		return changed
	}
	cm, _ := body["context_management"].(map[string]any)
	if cm == nil {
		return changed
	}
	edits, _ := cm["edits"].([]any)
	filtered := make([]any, 0, len(edits))
	for _, e := range edits {
		if em, ok := e.(map[string]any); ok {
			if t, _ := em["type"].(string); strings.HasPrefix(t, "clear_thinking") {
				changed = true
				continue
			}
		}
		filtered = append(filtered, e)
	}
	cm["edits"] = filtered
	return changed
}

// fixInvalidEffort removes unrecognized effort values.
func fixInvalidEffort(body map[string]any) bool {
	oc, _ := body["output_config"].(map[string]any)
	if oc == nil {
		return false
	}
	effort, _ := oc["effort"].(string)
	switch effort {
	case "", "low", "medium", "high", "max":
		return false
	case "xhigh":
		oc["effort"] = "high"
		return true
	default:
		delete(oc, "effort")
		return true
	}
}

func hasCacheControl(body map[string]any) bool {
	sys, _ := body["system"].([]any)
	for _, s := range sys {
		if m, ok := s.(map[string]any); ok {
			if _, has := m["cache_control"]; has {
				return true
			}
		}
	}
	return false
}

// ensureCacheControl adds a default ephemeral cache breakpoint to the last
// system block if no cache_control exists anywhere in system blocks.
func ensureCacheControl(body map[string]any) {
	sys, _ := body["system"].([]any)
	if len(sys) == 0 {
		return
	}
	for _, s := range sys {
		if m, ok := s.(map[string]any); ok {
			if _, has := m["cache_control"]; has {
				return
			}
		}
	}
	if last, ok := sys[len(sys)-1].(map[string]any); ok {
		last["cache_control"] = map[string]any{"type": "ephemeral"}
	}
}

// hasClaudeIdentity reports whether the system field already carries the Claude
// Code identity (in any text block, or as a plain string). Genuine CC may put it
// in a non-first block (recent CLI prepends an `x-anthropic-billing-header`
// block), so we scan all blocks and match the version-stable prefix.
func matchesClaudeIdentity(text string) bool {
	t := strings.TrimLeft(text, " \t\r\n")
	return strings.HasPrefix(t, ccIdentityPrefix) || strings.HasPrefix(t, agentSDKIdentityPrefix)
}

func hasClaudeIdentity(sys any) bool {
	switch v := sys.(type) {
	case string:
		return matchesClaudeIdentity(v)
	case []any:
		for _, item := range v {
			if b, ok := item.(map[string]any); ok {
				if text, ok := b["text"].(string); ok && matchesClaudeIdentity(text) {
					return true
				}
			}
		}
	}
	return false
}

func normalizeSystem(sys any) []any {
	identity := map[string]any{"type": "text", "text": ClaudeCodeIdentity}
	switch v := sys.(type) {
	case nil:
		return []any{identity}
	case string:
		return []any{identity, map[string]any{"type": "text", "text": v}}
	case []any:
		// If the identity is already present anywhere, forward verbatim —
		// prepending a duplicate would be a forgery tell. Only inject when absent.
		if hasClaudeIdentity(v) {
			return v
		}
		return append([]any{identity}, v...)
	default:
		return []any{identity}
	}
}

func sanitizeCacheTTL(body map[string]any) {
	fixAny := func(b map[string]any) {
		if cc, ok := b["cache_control"].(map[string]any); ok {
			if ttl, has := cc["ttl"]; has && ttl != "5m" && ttl != "1h" {
				cc["ttl"] = "5m"
			}
		}
	}
	// Messages must use 5m — a 1h in messages after any 5m in
	// tools/system triggers API 400 (TTL ordering constraint).
	fix5m := func(b map[string]any) {
		if cc, ok := b["cache_control"].(map[string]any); ok {
			if ttl, _ := cc["ttl"].(string); ttl == "1h" {
				cc["ttl"] = "5m"
			}
			if ttl, has := cc["ttl"]; has && ttl != "5m" && ttl != "1h" {
				cc["ttl"] = "5m"
			}
		}
	}
	var walkAny func(any)
	walkAny = func(node any) {
		arr, ok := node.([]any)
		if !ok {
			return
		}
		for _, item := range arr {
			if b, ok := item.(map[string]any); ok {
				fixAny(b)
				walkAny(b["content"])
			}
		}
	}
	var walk5m func(any)
	walk5m = func(node any) {
		arr, ok := node.([]any)
		if !ok {
			return
		}
		for _, item := range arr {
			if b, ok := item.(map[string]any); ok {
				fix5m(b)
				walk5m(b["content"])
			}
		}
	}
	walkAny(body["system"])
	walkAny(body["tools"])
	if msgs, ok := body["messages"].([]any); ok {
		for _, m := range msgs {
			if mm, ok := m.(map[string]any); ok {
				walk5m(mm["content"])
			}
		}
	}
}

// sanitizeThinkingBlocks strips non-standard fields from thinking content blocks.
// Anthropic only allows type, thinking, signature (and cache_control) on thinking
// blocks. Clients like opencode add extra fields (e.g. alternative_display_type)
// that cause 400 errors.
func sanitizeThinkingBlocks(body map[string]any) {
	allowed := map[string]bool{"type": true, "thinking": true, "signature": true, "cache_control": true}
	msgs, _ := body["messages"].([]any)
	for _, m := range msgs {
		mm, _ := m.(map[string]any)
		if mm == nil {
			continue
		}
		content, _ := mm["content"].([]any)
		for _, c := range content {
			block, _ := c.(map[string]any)
			if block == nil {
				continue
			}
			if block["type"] == "thinking" {
				for k := range block {
					if !allowed[k] {
						delete(block, k)
					}
				}
			}
		}
	}
}

// sanitizeMetadata ensures only user_id exists in metadata. Returns true if
// any non-standard keys were removed (body was dirtied).
func sanitizeMetadata(body map[string]any, userID string) bool {
	meta, _ := body["metadata"].(map[string]any)
	if meta == nil {
		meta = map[string]any{}
	}
	if _, ok := meta["user_id"].(string); !ok || meta["user_id"] == "" {
		meta["user_id"] = userID
	}
	dirty := false
	for k := range meta {
		if k != "user_id" {
			delete(meta, k)
			dirty = true
		}
	}
	body["metadata"] = meta
	return dirty
}

// stripMetadataPadBytes removes "pad":"..." from metadata in raw JSON bytes
// without full re-marshal — preserving thinking block signatures.
func stripMetadataPadBytes(raw []byte) []byte {
	// Quick regex: remove ,"pad":"..." or "pad":"...", from the metadata object
	re := regexp.MustCompile(`"pad"\s*:\s*"[^"]*"\s*,?\s*`)
	result := re.ReplaceAll(raw, nil)
	// Fix trailing comma before closing brace: ,}
	trailingComma := regexp.MustCompile(`,\s*}`)
	result = trailingComma.ReplaceAll(result, []byte("}"))
	return result
}

// forceStreamTrue ensures the body has "stream":true at the byte level.
// Real CC always streams; non-CC bodies set it via fillDefaults. This handles
// the CC-shaped verbatim path where we cannot re-marshal.
func forceStreamTrue(raw []byte) []byte {
	return bytes.Replace(raw, []byte(`"stream":false`), []byte(`"stream":true `), 1)
}

// fixEffortXhighBytes maps "effort":"xhigh" → "effort":"high" at the byte
// level. Uses key-value regex to avoid replacing "xhigh" in message text.
// "xhigh" is an SDK-internal value; the API rejects it on all models.
var effortXhighRe = regexp.MustCompile(`"effort"\s*:\s*"xhigh"`)

func fixEffortXhighBytes(raw []byte) []byte {
	return effortXhighRe.ReplaceAll(raw, []byte(`"effort":"high"`))
}

// ValidateBody checks the cloaked body for conditions that will definitely be
// rejected by the API. Returns a non-empty error message if the request should
// be rejected early (saves a round-trip). Empty string means OK to send.
func ValidateBody(cloaked []byte) string {
	var body map[string]any
	if json.Unmarshal(cloaked, &body) != nil {
		return ""
	}

	model, _ := body["model"].(string)

	// 1. temperature ≠ 1 on a thinking model without thinking:disabled
	//    API auto-enables thinking on thinking-capable models via beta flag,
	//    then rejects temperature ≠ 1.
	if temp, ok := body["temperature"].(float64); ok && temp != 1.0 {
		th, _ := body["thinking"].(map[string]any)
		thType := ""
		if th != nil {
			thType, _ = th["type"].(string)
		}
		if thType != "disabled" && thinkingModel(model) {
			return "`temperature` may only be set to 1 when thinking is enabled. Set temperature to 1 or disable thinking."
		}
	}

	// 2. clear_thinking with thinking disabled or absent on thinking model
	{
		th, _ := body["thinking"].(map[string]any)
		thType := ""
		if th != nil {
			thType, _ = th["type"].(string)
		}
		if thType == "disabled" || (thType == "" && !thinkingModel(model)) {
			if cm, ok := body["context_management"].(map[string]any); ok {
				if edits, ok := cm["edits"].([]any); ok {
					for _, e := range edits {
						if em, ok := e.(map[string]any); ok {
							if t, _ := em["type"].(string); strings.HasPrefix(t, "clear_thinking") {
								return "`" + t + "` strategy requires `thinking` to be enabled or adaptive"
							}
						}
					}
				}
			}
		}
	}

	// 3. effort level not recognized
	if oc, ok := body["output_config"].(map[string]any); ok {
		if effort, ok := oc["effort"].(string); ok && effort != "" {
			switch effort {
			case "low", "medium", "high", "xhigh", "max":
			default:
				return "Unsupported effort level '" + effort + "'. Supported: low, medium, high, xhigh, max."
			}
		}
	}

	// 3. thinking blocks in non-assistant messages + empty text
	msgs, _ := body["messages"].([]any)
	for mi, msg := range msgs {
		m, _ := msg.(map[string]any)
		if m == nil {
			continue
		}
		role, _ := m["role"].(string)
		content, _ := m["content"].([]any)
		for ci, c := range content {
			block, _ := c.(map[string]any)
			if block == nil {
				continue
			}
			if block["type"] == "thinking" && role != "assistant" {
				return "messages." + itoa(mi) + ".content: thinking blocks may only be in `assistant` messages"
			}
			if block["type"] == "thinking" && role == "assistant" {
				sig, _ := block["signature"].(string)
				if sig == "" {
					return "messages." + itoa(mi) + ".content." + itoa(ci) + ": Missing `signature` in `thinking` block"
				}
				if _, err := base64.StdEncoding.DecodeString(sig); err != nil {
					return "messages." + itoa(mi) + ".content." + itoa(ci) + ": Invalid `signature` in `thinking` block"
				}
			}
			if block["type"] == "text" {
				if text, ok := block["text"].(string); ok && text == "" {
					// Stripped upstream by stripEmptyTextBlocks; log if one slips through.
					logDD("warn: empty text block at messages.%s.content.%s (should have been stripped)", itoa(mi), itoa(ci))
				}
			}
		}
	}

	// 4. base64 image > 10 MB
	for mi, msg := range msgs {
		m, _ := msg.(map[string]any)
		if m == nil {
			continue
		}
		content, _ := m["content"].([]any)
		for ci, c := range content {
			block, _ := c.(map[string]any)
			if block == nil || block["type"] != "image" {
				continue
			}
			src, _ := block["source"].(map[string]any)
			if src == nil || src["type"] != "base64" {
				continue
			}
			data, _ := src["data"].(string)
			// base64 decode size ≈ len * 3/4
			if len(data)*3/4 > 10*1024*1024 {
				return "messages." + itoa(mi) + ".content." + itoa(ci) + ".image.source.base64: image exceeds 10 MB maximum"
			}
		}
	}

	// 5. cache_control blocks > 4
	cacheCount := 0
	if sys, ok := body["system"].([]any); ok {
		for _, s := range sys {
			if sm, ok := s.(map[string]any); ok {
				if _, has := sm["cache_control"]; has {
					cacheCount++
				}
			}
		}
	}
	if tools, ok := body["tools"].([]any); ok {
		for _, t := range tools {
			if tm, ok := t.(map[string]any); ok {
				if _, has := tm["cache_control"]; has {
					cacheCount++
				}
			}
		}
	}
	for _, msg := range msgs {
		m, _ := msg.(map[string]any)
		if m == nil {
			continue
		}
		content, _ := m["content"].([]any)
		for _, c := range content {
			block, _ := c.(map[string]any)
			if block == nil {
				continue
			}
			if _, has := block["cache_control"]; has {
				cacheCount++
			}
		}
	}
	if cacheCount > 4 {
		return "A maximum of 4 blocks with cache_control may be provided. Found " + itoa(cacheCount) + "."
	}

	return ""
}

func itoa(n int) string { return strconv.Itoa(n) }

// stripThinkingBlocks removes all thinking blocks from assistant messages
// in the request body. Used to retry when upstream rejects expired signatures.
// Returns nil if parsing fails (caller should not retry).
func stripThinkingBlocks(body []byte) []byte {
	var parsed map[string]any
	if json.Unmarshal(body, &parsed) != nil {
		return nil
	}
	msgs, _ := parsed["messages"].([]any)
	if msgs == nil {
		return nil
	}
	changed := false
	for _, msg := range msgs {
		m, _ := msg.(map[string]any)
		if m == nil {
			continue
		}
		role, _ := m["role"].(string)
		if role != "assistant" {
			continue
		}
		content, _ := m["content"].([]any)
		if content == nil {
			continue
		}
		filtered := make([]any, 0, len(content))
		for _, c := range content {
			block, _ := c.(map[string]any)
			if block != nil && block["type"] == "thinking" {
				changed = true
				continue
			}
			filtered = append(filtered, c)
		}
		if len(filtered) == 0 {
			filtered = append(filtered, map[string]any{"type": "text", "text": "."})
		}
		m["content"] = filtered
	}
	if !changed {
		return nil
	}
	out, err := marshalBody(parsed)
	if err != nil {
		return nil
	}
	return out
}
