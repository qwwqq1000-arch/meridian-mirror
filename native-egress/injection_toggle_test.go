package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// toggleTmpl mirrors a real 2.1.198 capture: SystemRaw with 3 blocks
// (billing, identity, main harness prompt) and ToolsRaw with base tools, so the
// byte-preserving production path (not the parsed fallback) is exercised.
func toggleTmpl() *BodyTemplate {
	return &BodyTemplate{
		SystemRaw: json.RawMessage(`[` +
			`{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.198.542; cc_entrypoint=sdk-cli;"},` +
			`{"type":"text","text":"You are a Claude agent, built on Anthropic's Claude Agent SDK.","cache_control":{"type":"ephemeral","ttl":"1h"}},` +
			`{"type":"text","text":"You are an interactive agent. MAIN HARNESS PROMPT body here.","cache_control":{"type":"ephemeral","ttl":"1h"}}` +
			`]`),
		ToolsRaw:          json.RawMessage(`[{"name":"Bash","description":"b","input_schema":{"type":"object"}},{"name":"Read","description":"r","input_schema":{"type":"object"}}]`),
		ContextManagement: map[string]any{"edits": []any{map[string]any{"type": "clear_thinking_20251015", "keep": "all"}}},
		Stream:            true,
	}
}

func mergeToggle(t *testing.T, userBody string, injectSys, injectTools bool) map[string]any {
	t.Helper()
	out, err := MergeUserRequest([]byte(userBody), toggleTmpl(),
		`{"device_id":"d","account_uuid":"a","session_id":"s"}`, injectSys, injectTools)
	if err != nil {
		t.Fatal(err)
	}
	var b map[string]any
	if err := json.Unmarshal(out, &b); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out)
	}
	return b
}

func toolNames(b map[string]any) []string {
	names := []string{}
	if raw, ok := b["tools"].([]any); ok {
		for _, tl := range raw {
			names = append(names, tl.(map[string]any)["name"].(string))
		}
	}
	return names
}

const helloUser = `{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`

// --- system prompt toggle ---

func TestInjectSystemPromptOff_KeepsOnlyIdentity(t *testing.T) {
	b := mergeToggle(t, helloUser, false, false)
	sys, _ := b["system"].([]any)
	if len(sys) != 2 {
		t.Fatalf("injectSystemPrompt=false must keep exactly 2 blocks (billing+identity), got %d", len(sys))
	}
	if strings.Contains(sys[0].(map[string]any)["text"].(string), "billing-header") == false {
		t.Error("block[0] must be the billing header")
	}
	if !strings.Contains(sys[1].(map[string]any)["text"].(string), "Claude Agent SDK") {
		t.Error("block[1] must be the identity block")
	}
	// identity block keeps its cache breakpoint so the tiny prefix still caches
	if _, ok := sys[1].(map[string]any)["cache_control"]; !ok {
		t.Error("identity block must retain cache_control")
	}
}

func TestInjectSystemPromptOff_DropsMainPrompt(t *testing.T) {
	out, _ := MergeUserRequest([]byte(helloUser), toggleTmpl(),
		`{"device_id":"d"}`, false, false)
	if strings.Contains(string(out), "MAIN HARNESS PROMPT") {
		t.Error("injectSystemPrompt=false must drop the main harness prompt (system[2])")
	}
}

func TestInjectSystemPromptOn_KeepsAllThreeBlocks(t *testing.T) {
	b := mergeToggle(t, helloUser, true, true)
	sys, _ := b["system"].([]any)
	if len(sys) != 3 {
		t.Fatalf("injectSystemPrompt=true must keep all 3 blocks, got %d", len(sys))
	}
	out, _ := MergeUserRequest([]byte(helloUser), toggleTmpl(), `{"device_id":"d"}`, true, true)
	if !strings.Contains(string(out), "MAIN HARNESS PROMPT") {
		t.Error("injectSystemPrompt=true must include the main harness prompt")
	}
}

// --- tools toggle ---

func TestInjectToolsOff_PassesUserToolsVerbatim(t *testing.T) {
	user := `{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}],"tools":[{"name":"Foo","description":"f","input_schema":{"type":"object"}}]}`
	b := mergeToggle(t, user, false, false)
	names := toolNames(b)
	if len(names) != 1 || names[0] != "Foo" {
		t.Fatalf("injectTools=false must pass the user's own tools through verbatim, got %v", names)
	}
	for _, n := range names {
		if n == "Bash" || n == "Read" {
			t.Errorf("injectTools=false must NOT inject CC base tools, but saw %s", n)
		}
	}
}

func TestInjectToolsOff_NoUserTools_OmitsToolsField(t *testing.T) {
	b := mergeToggle(t, helloUser, false, false)
	if _, has := b["tools"]; has {
		t.Error("injectTools=false with no user tools must omit the tools field entirely")
	}
}

func TestInjectToolsOn_InjectsBaseTools(t *testing.T) {
	b := mergeToggle(t, helloUser, true, true)
	names := toolNames(b)
	has := map[string]bool{}
	for _, n := range names {
		has[n] = true
	}
	if !has["Bash"] || !has["Read"] {
		t.Fatalf("injectTools=true must inject CC base tools, got %v", names)
	}
}

// --- identity-mode preserves the cheap, non-prompt disguise fields ---

func TestIdentityMode_KeepsMetadataThinkingMaxTokens(t *testing.T) {
	out, _ := MergeUserRequest([]byte(helloUser), toggleTmpl(),
		`{"device_id":"d","account_uuid":"a","session_id":"s"}`, false, false)
	s := string(out)
	for _, want := range []string{`"metadata"`, `"user_id"`, `"thinking"`, `"max_tokens"`, `"context_management"`} {
		if !strings.Contains(s, want) {
			t.Errorf("identity mode must still emit %s (cheap disguise field)", want)
		}
	}
}

// mixed toggles resolve independently
func TestMixedToggles_SystemOnToolsOff(t *testing.T) {
	user := `{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}],"tools":[{"name":"Foo","description":"f","input_schema":{"type":"object"}}]}`
	b := mergeToggle(t, user, true, false)
	if len(b["system"].([]any)) != 3 {
		t.Error("system on → 3 blocks")
	}
	if names := toolNames(b); len(names) != 1 || names[0] != "Foo" {
		t.Errorf("tools off → user's own tools only, got %v", names)
	}
}
