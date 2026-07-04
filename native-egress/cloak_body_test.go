package main

import (
	"encoding/json"
	"testing"
)

func TestCloakBodyInjectsIdentityAndUserID(t *testing.T) {
	out, err := CloakBody([]byte(`{"system":[{"type":"text","text":"hi","cache_control":{"type":"ephemeral"}}],"messages":[]}`), "user_fake")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal(out, &m)
	sys := m["system"].([]any)
	if sys[0].(map[string]any)["text"] != ClaudeCodeIdentity {
		t.Fatal("identity not first")
	}
	if sys[1].(map[string]any)["cache_control"] == nil {
		t.Fatal("cache_control must be preserved")
	}
	if m["metadata"].(map[string]any)["user_id"] != "user_fake" {
		t.Fatal("user_id not set")
	}
}

// Genuine Claude Code carries the identity in a non-first block (a billing-header
// block comes first). CloakBody must forward verbatim, NOT prepend a duplicate.
func TestCloakBodyIdentityInNonFirstBlockNotDuplicated(t *testing.T) {
	in := `{"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.148.902"},{"type":"text","text":"` + ClaudeCodeIdentity + `\n\nmore"}],"messages":[]}`
	out, err := CloakBody([]byte(in), "u")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal(out, &m)
	sys := m["system"].([]any)
	if len(sys) != 2 {
		t.Fatalf("expected 2 system blocks (verbatim), got %d: %#v", len(sys), sys)
	}
	count := 0
	for _, b := range sys {
		if bm, ok := b.(map[string]any); ok {
			if text, ok := bm["text"].(string); ok && len(text) >= len(ClaudeCodeIdentity) && text[:len(ClaudeCodeIdentity)] == ClaudeCodeIdentity {
				count++
			}
		}
	}
	if count != 1 {
		t.Fatalf("identity must appear exactly once (no duplicate), got %d", count)
	}
}

// A genuine CC body (identity present) must be forwarded BYTE-FOR-BYTE verbatim.
// Re-marshaling reorders keys / reformats numbers and corrupts the `signature`
// on assistant thinking blocks → Anthropic 400 "thinking blocks must remain as
// they were". The raw bytes here use non-alphabetical key order on purpose; a
// re-marshal would reorder them.
func TestCloakBodyVerbatimWhenCcShaped(t *testing.T) {
	// cache_control present → no default injection → fully verbatim.
	raw := []byte(`{"model":"claude-sonnet-4-6","system":[{"type":"text","text":"You are Claude Code, Anthropic's official CLI for Claude, running within the Claude Agent SDK.","cache_control":{"type":"ephemeral"}}],"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"hmm","signature":"SIG_DO_NOT_TOUCH=="}]}]}`)
	out, err := CloakBody(raw, "u")
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(raw) {
		t.Fatalf("CC body must be verbatim.\n got: %s\nwant: %s", out, raw)
	}
}

func TestCloakBodyPreserves1hCacheTTL(t *testing.T) {
	raw := []byte(`{"system":[{"type":"text","text":"You are Claude Code, Anthropic's official CLI for Claude.","cache_control":{"type":"ephemeral","ttl":"1h"}}],"messages":[]}`)
	out, _ := CloakBody(raw, "u")
	if string(out) != string(raw) {
		t.Fatalf("client ttl:1h must be preserved verbatim.\n got: %s", out)
	}
}

func TestCloakBodyInjectsDefault5mWhenNoCacheControl(t *testing.T) {
	// CC bodies are returned verbatim even without cache_control to avoid
	// re-marshaling (which corrupts thinking block signatures).
	raw := []byte(`{"system":[{"type":"text","text":"You are Claude Code, Anthropic's official CLI for Claude."}],"messages":[]}`)
	out, _ := CloakBody(raw, "u")
	if string(out) != string(raw) {
		t.Fatalf("CC body without cache_control must be returned verbatim.\n got: %s", out)
	}
}

func TestCloakBodyDropsThinkingWhenToolChoiceForced(t *testing.T) {
	raw := []byte(`{"system":[{"type":"text","text":"You are Claude Code, Anthropic's official CLI for Claude.","cache_control":{"type":"ephemeral"}}],"thinking":{"type":"enabled"},"tool_choice":{"type":"tool","name":"x"},"tools":[{"name":"x"}],"messages":[]}`)
	out, _ := CloakBody(raw, "u")
	var m map[string]any
	_ = json.Unmarshal(out, &m)
	if m["thinking"] != nil {
		t.Fatalf("thinking config must be dropped when tool_choice forces a tool: %s", out)
	}
}

func TestCloakBodyDropsThinkingOnFakingPath(t *testing.T) {
	raw := []byte(`{"system":[{"type":"text","text":"You are a helper."}],"thinking":{"type":"enabled"},"tool_choice":{"type":"tool","name":"x"},"tools":[{"name":"x"}],"messages":[]}`)
	out, _ := CloakBody(raw, "u")
	var m map[string]any
	_ = json.Unmarshal(out, &m)
	if m["thinking"] != nil {
		t.Fatalf("thinking must be dropped on the faking path too: %s", out)
	}
}

func TestCloakBodyKeepsThinkingWhenToolChoiceAuto(t *testing.T) {
	raw := []byte(`{"system":[{"type":"text","text":"You are Claude Code, Anthropic's official CLI for Claude.","cache_control":{"type":"ephemeral"}}],"thinking":{"type":"enabled"},"tool_choice":{"type":"auto"},"messages":[]}`)
	out, _ := CloakBody(raw, "u")
	var m map[string]any
	_ = json.Unmarshal(out, &m)
	if m["thinking"] == nil {
		t.Fatalf("thinking must be kept when tool_choice is auto: %s", out)
	}
}

func TestCloakBodyStringSystem(t *testing.T) {
	out, _ := CloakBody([]byte(`{"system":"you are X","messages":[]}`), "u")
	var m map[string]any
	_ = json.Unmarshal(out, &m)
	sys := m["system"].([]any)
	if sys[0].(map[string]any)["text"] != ClaudeCodeIdentity || sys[1].(map[string]any)["text"] != "you are X" {
		t.Fatalf("string system not normalized: %#v", sys)
	}
}
