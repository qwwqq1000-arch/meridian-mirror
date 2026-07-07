package main

import (
	"bytes"
	"encoding/json"
	"strings"
)

// Byte-order-preserving serialization helpers. Real CC (node) emits JSON object
// keys in insertion order; Go's json.Marshal sorts them alphabetically. To match
// real CC byte-for-byte we splice raw captured/client bytes instead of round-
// tripping through map[string]any, and construct model-derived objects with the
// exact per-model key order verified from golden captures.

// orderedKV is a key with its raw JSON value, preserving insertion order.
type orderedKV struct {
	key string
	val json.RawMessage
}

// parseOrderedObject decodes a JSON object preserving key order and raw values.
// ok=false if raw is not a JSON object.
func parseOrderedObject(raw []byte) ([]orderedKV, bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	t, err := dec.Token()
	if err != nil {
		return nil, false
	}
	if d, ok := t.(json.Delim); !ok || d != '{' {
		return nil, false
	}
	var out []orderedKV
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, false
		}
		key, ok := kt.(string)
		if !ok {
			return nil, false
		}
		var val json.RawMessage
		if err := dec.Decode(&val); err != nil {
			return nil, false
		}
		out = append(out, orderedKV{key, val})
	}
	return out, true
}

// emitOrderedObject serializes key/raw-value pairs preserving order, no HTML escape.
func emitOrderedObject(kvs []orderedKV) json.RawMessage {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, p := range kvs {
		if i > 0 {
			buf.WriteByte(',')
		}
		kb, _ := marshalNoEscape(p.key)
		buf.Write(kb)
		buf.WriteByte(':')
		buf.Write(p.val)
	}
	buf.WriteByte('}')
	return buf.Bytes()
}

// stripKeyOrdered removes a top-level key, preserving the order of the rest.
func stripKeyOrdered(raw json.RawMessage, key string) json.RawMessage {
	kvs, ok := parseOrderedObject(raw)
	if !ok {
		return raw
	}
	out := kvs[:0]
	changed := false
	for _, p := range kvs {
		if p.key == key {
			changed = true
			continue
		}
		out = append(out, p)
	}
	if !changed {
		return raw
	}
	return emitOrderedObject(out)
}

// jsonStringBytes encodes s as a JSON string value (no HTML escaping).
func jsonStringBytes(s string) json.RawMessage {
	b, _ := marshalNoEscape(s)
	return b
}

func peekRole(msg json.RawMessage) string {
	var m struct {
		Role string `json:"role"`
	}
	json.Unmarshal(msg, &m)
	return m.Role
}

func peekName(b json.RawMessage) string {
	var m struct {
		Name string `json:"name"`
	}
	json.Unmarshal(b, &m)
	return m.Name
}

func isEmptyTextBlock(b json.RawMessage) bool {
	var m struct {
		Type string  `json:"type"`
		Text *string `json:"text"`
	}
	if json.Unmarshal(b, &m) != nil {
		return false
	}
	return m.Type == "text" && m.Text != nil && *m.Text == ""
}

func isEmptyImageBlock(b json.RawMessage) bool {
	var m struct {
		Type   string `json:"type"`
		Source *struct {
			Data string `json:"data"`
		} `json:"source"`
	}
	if json.Unmarshal(b, &m) != nil {
		return false
	}
	if m.Type != "image" {
		return false
	}
	return m.Source == nil || m.Source.Data == ""
}

// processUserMessage returns the byte-preserved form of a non-assistant message.
// It drops empty text/image blocks and optionally prepends a folded system text
// block (foldText), emitting {"role":...,"content":[...]}. When nothing changes
// and no fold is needed, the original bytes are returned unchanged so the client's
// exact key order survives.
func processUserMessage(msg json.RawMessage, foldText string) json.RawMessage {
	kvs, ok := parseOrderedObject(msg)
	if !ok {
		return msg
	}
	var roleRaw, contentRaw json.RawMessage
	for _, p := range kvs {
		switch p.key {
		case "role":
			roleRaw = p.val
		case "content":
			contentRaw = p.val
		}
	}

	var blocks []json.RawMessage
	isArray := len(contentRaw) > 0 && json.Unmarshal(contentRaw, &blocks) == nil

	var kept []json.RawMessage
	stripped := false
	if isArray {
		for _, b := range blocks {
			if isEmptyTextBlock(b) || isEmptyImageBlock(b) {
				stripped = true
				continue
			}
			kept = append(kept, b)
		}
	}

	if foldText == "" && !stripped {
		return emitRoleFirst(msg) // content bytes intact, wrapper normalized to role-first
	}

	var newBlocks []json.RawMessage
	if foldText != "" {
		newBlocks = append(newBlocks, json.RawMessage(`{"type":"text","text":`+string(jsonStringBytes(foldText))+`}`))
	}
	if isArray {
		newBlocks = append(newBlocks, kept...)
	} else if len(contentRaw) > 0 {
		// content is a string (or other scalar) — wrap it as a text block
		newBlocks = append(newBlocks, json.RawMessage(`{"type":"text","text":`+string(contentRaw)+`}`))
	}
	if len(newBlocks) == 0 {
		newBlocks = append(newBlocks, json.RawMessage(`{"type":"text","text":"."}`))
	}
	if len(roleRaw) == 0 {
		roleRaw = json.RawMessage(`"user"`)
	}
	var buf bytes.Buffer
	buf.WriteString(`{"role":`)
	buf.Write(roleRaw)
	buf.WriteString(`,"content":[`)
	for i, b := range newBlocks {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.Write(b)
	}
	buf.WriteString(`]}`)
	return buf.Bytes()
}

// emitRoleFirst normalizes a message wrapper to real CC's {"role":...,"content":...}
// order while keeping the role/content VALUE bytes byte-identical. Real CC always
// emits role before content; opencode may send content,role. Reordering the wrapper
// is safe for thinking signatures because the content array bytes are untouched.
func emitRoleFirst(msg json.RawMessage) json.RawMessage {
	kvs, ok := parseOrderedObject(msg)
	if !ok {
		return msg
	}
	var roleRaw, contentRaw json.RawMessage
	var extra []orderedKV
	for _, p := range kvs {
		switch p.key {
		case "role":
			roleRaw = p.val
		case "content":
			contentRaw = p.val
		default:
			extra = append(extra, p)
		}
	}
	if len(roleRaw) == 0 || len(contentRaw) == 0 {
		return msg // not a normal message — leave untouched
	}
	var buf bytes.Buffer
	buf.WriteString(`{"role":`)
	buf.Write(roleRaw)
	buf.WriteString(`,"content":`)
	buf.Write(contentRaw)
	for _, p := range extra {
		buf.WriteByte(',')
		kb, _ := marshalNoEscape(p.key)
		buf.Write(kb)
		buf.WriteByte(':')
		buf.Write(p.val)
	}
	buf.WriteByte('}')
	return buf.Bytes()
}

// processMessagesOrdered rebuilds the messages array preserving byte order.
// Assistant turns keep their content bytes (thinking-signature safety) with the
// wrapper normalized to role-first; user turns are cleaned of empty blocks and the
// folded system text is prepended to the first user message.
func processMessagesOrdered(rawMsgs json.RawMessage, foldText string) ([]byte, error) {
	var msgs []json.RawMessage
	if len(rawMsgs) > 0 {
		if err := json.Unmarshal(rawMsgs, &msgs); err != nil {
			return nil, err
		}
	}
	out := make([]json.RawMessage, 0, len(msgs)+1)
	folded := false
	for _, msg := range msgs {
		if peekRole(msg) == "assistant" {
			out = append(out, emitRoleFirst(msg)) // content bytes intact, wrapper role-first
			continue
		}
		ft := ""
		if !folded && peekRole(msg) == "user" && foldText != "" {
			ft = foldText
			folded = true
		}
		out = append(out, processUserMessage(msg, ft))
	}
	if foldText != "" && !folded {
		block := `{"type":"text","text":` + string(jsonStringBytes(foldText)) + `}`
		out = append([]json.RawMessage{json.RawMessage(`{"role":"user","content":[` + block + `]}`)}, out...)
	}
	var buf bytes.Buffer
	buf.WriteByte('[')
	for i, m := range out {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.Write(m)
	}
	buf.WriteByte(']')
	return buf.Bytes(), nil
}

// foldTextFrom collapses the user's "system" field into the CLAUDE.md-style text
// that gets folded into the first user message (reusing mergeUserSystem's rules).
func foldTextFrom(sysRaw json.RawMessage) string {
	if len(sysRaw) == 0 {
		return ""
	}
	var sys any
	if json.Unmarshal(sysRaw, &sys) != nil {
		return ""
	}
	blocks := mergeUserSystem(sys)
	if len(blocks) == 0 {
		return ""
	}
	parts := make([]string, 0, len(blocks))
	for _, b := range blocks {
		if m, ok := b.(map[string]any); ok {
			if t, ok := m["text"].(string); ok && t != "" {
				parts = append(parts, t)
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

// buildSystemBytes returns real CC's captured system array verbatim (SystemRaw)
// when available; otherwise it marshals the parsed template, adding a default
// ephemeral cache breakpoint if none exists (builtin/fallback path only).
//
// When injectSystemPrompt is false (identity-only mode) it keeps only the first
// two blocks — system[0] billing header + system[1] identity — and DROPS the
// main harness prompt (block 3+, ~7K tokens). The identity blocks keep their
// captured cache_control so the tiny prefix still caches.
func buildSystemBytes(tmpl *BodyTemplate, injectSystemPrompt bool) []byte {
	if len(tmpl.SystemRaw) > 0 {
		if injectSystemPrompt {
			return tmpl.SystemRaw
		}
		var blocks []json.RawMessage
		if json.Unmarshal(tmpl.SystemRaw, &blocks) == nil && len(blocks) > 2 {
			var buf bytes.Buffer
			buf.WriteByte('[')
			for i, b := range blocks[:2] { // billing + identity only
				if i > 0 {
					buf.WriteByte(',')
				}
				buf.Write(b) // byte-preserved, keeps cache_control on identity
			}
			buf.WriteByte(']')
			return buf.Bytes()
		}
		return tmpl.SystemRaw // <=2 blocks or parse failure: nothing to drop
	}
	sys := tmpl.System
	if !injectSystemPrompt && len(sys) > 2 {
		sys = sys[:2]
	}
	hasCC := false
	for _, s := range sys {
		if m, ok := s.(map[string]any); ok {
			if _, has := m["cache_control"]; has {
				hasCC = true
				break
			}
		}
	}
	if !hasCC && len(sys) > 0 {
		cp := append([]any{}, sys...)
		if last, ok := cp[len(cp)-1].(map[string]any); ok {
			nm := make(map[string]any, len(last)+1)
			for k, v := range last {
				nm[k] = v
			}
			nm["cache_control"] = map[string]any{"type": "ephemeral"}
			cp[len(cp)-1] = nm
		}
		sys = cp
	}
	b, _ := marshalNoEscape(sys)
	return b
}

// buildToolsBytes splices the captured base tools (ToolsRaw, byte-perfect order)
// and appends only CC-recognized user tools (deduped, cache_control stripped).
//
// When injectTools is false (identity-only mode) it does NOT inject the CC base
// 28 tools (~26K tokens); it passes the user's own tools through verbatim, or
// omits the tools field entirely when the user sent none.
func buildToolsBytes(tmpl *BodyTemplate, userToolsRaw json.RawMessage, injectTools bool) []byte {
	if !injectTools {
		if len(userToolsRaw) == 0 {
			return nil
		}
		return userToolsRaw // pass the user's own tools through, byte-preserved
	}
	var baseRaw []json.RawMessage
	if len(tmpl.ToolsRaw) > 0 {
		json.Unmarshal(tmpl.ToolsRaw, &baseRaw)
	} else {
		for _, tl := range tmpl.Tools {
			if b, err := marshalNoEscape(tl); err == nil {
				baseRaw = append(baseRaw, b)
			}
		}
	}
	baseNames := map[string]bool{}
	for _, b := range baseRaw {
		if n := peekName(b); n != "" {
			baseNames[n] = true
		}
	}
	merged := append([]json.RawMessage{}, baseRaw...)
	if len(userToolsRaw) > 0 {
		var userTools []json.RawMessage
		json.Unmarshal(userToolsRaw, &userTools)
		for _, tb := range userTools {
			n := peekName(tb)
			if n == "" || baseNames[n] || !isCCRecognizedTool(n, baseNames) {
				continue
			}
			merged = append(merged, stripKeyOrdered(tb, "cache_control"))
			baseNames[n] = true
		}
	}
	if len(merged) == 0 {
		return nil
	}
	var buf bytes.Buffer
	buf.WriteByte('[')
	for i, b := range merged {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.Write(b)
	}
	buf.WriteByte(']')
	return buf.Bytes()
}

// buildContextManagementBytes splices the captured context_management verbatim.
func buildContextManagementBytes(tmpl *BodyTemplate) []byte {
	if len(tmpl.ContextManagementRaw) > 0 {
		return tmpl.ContextManagementRaw
	}
	if tmpl.ContextManagement == nil {
		return nil
	}
	b, _ := marshalNoEscape(tmpl.ContextManagement)
	return b
}

// modelThinkingBytes / modelOutputConfigBytes emit the exact per-model bytes real
// CC sends (verified from golden captures), preserving key order.
func modelThinkingBytes(model string) []byte {
	if strings.Contains(model, "haiku") {
		return []byte(`{"budget_tokens":31999,"type":"enabled","display":"omitted"}`)
	}
	return []byte(`{"type":"adaptive","display":"omitted"}`)
}

func modelOutputConfigBytes(model string) []byte {
	if strings.Contains(model, "haiku") {
		return nil
	}
	return []byte(`{"effort":"high"}`)
}

// sanitizeToolChoiceBytes normalizes a string tool_choice into the object form the
// API expects, preserving an already-object value verbatim.
func sanitizeToolChoiceBytes(raw json.RawMessage) []byte {
	if len(raw) == 0 {
		return nil
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		switch s {
		case "auto":
			return nil // real CC omits tool_choice; "auto" is the implicit default
		case "any", "none":
			return []byte(`{"type":"` + s + `"}`)
		default:
			return append(append([]byte(`{"type":"tool","name":`), jsonStringBytes(s)...), '}')
		}
	}
	// Object form: omit the default {"type":"auto"} — real CC never sends
	// tool_choice for the auto case (verified absent in captures that carry tools).
	var obj struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &obj) == nil && obj.Type == "auto" {
		return nil
	}
	return raw
}
