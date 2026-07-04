package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func baseTmpl() *BodyTemplate {
	return &BodyTemplate{
		System: []any{
			map[string]any{"type": "text", "text": "x-anthropic-billing-header: cc_version=2.1.198.542; cc_entrypoint=sdk-cli;"},
			map[string]any{"type": "text", "text": "You are a Claude agent, built on Anthropic's Claude Agent SDK.", "cache_control": map[string]any{"type": "ephemeral", "ttl": "1h"}},
		},
		Tools:             []any{map[string]any{"name": "Bash"}, map[string]any{"name": "Read"}},
		ContextManagement: map[string]any{"edits": []any{map[string]any{"type": "clear_thinking_20251015", "keep": "all"}}},
		Stream:            true,
	}
}

func TestNormalizerStripsUnsupportedToolAndForcesModelFields(t *testing.T) {
	user := []byte(`{"model":"claude-sonnet-4-6","max_tokens":8000,"temperature":0.7,
		"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}],
		"tools":[{"name":"Read"},{"name":"CustomUnsupportedTool"},{"name":"mcp__foo__bar"}]}`)
	out, err := MergeUserRequest(user, baseTmpl(), `{"device_id":"d","account_uuid":"a","session_id":"s"}`)
	if err != nil {
		t.Fatal(err)
	}
	var b map[string]any
	json.Unmarshal(out, &b)

	if _, ok := b["temperature"]; ok {
		t.Fatal("temperature (non-CC field) must be stripped")
	}
	if int(b["max_tokens"].(float64)) != 64000 {
		t.Fatalf("max_tokens must be forced to 64000, got %v", b["max_tokens"])
	}
	names := map[string]bool{}
	for _, tl := range b["tools"].([]any) {
		names[tl.(map[string]any)["name"].(string)] = true
	}
	if names["CustomUnsupportedTool"] {
		t.Fatal("unsupported user tool must be dropped")
	}
	if !names["Bash"] || !names["Read"] {
		t.Fatal("base tools must be present")
	}
	if !names["mcp__foo__bar"] {
		t.Fatal("MCP tool (CC-recognized) must be kept")
	}
	th := b["thinking"].(map[string]any)
	if th["type"] != "adaptive" || th["display"] != "omitted" {
		t.Fatalf("thinking must be adaptive+omitted, got %v", th)
	}
}

func TestUserSystemFoldedIntoFirstUserMessage(t *testing.T) {
	user := []byte(`{"model":"claude-sonnet-4-6","system":"AGENT INSTRUCTIONS","messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`)
	out, err := MergeUserRequest(user, baseTmpl(), `{"device_id":"d","account_uuid":"a","session_id":"s"}`)
	if err != nil {
		t.Fatal(err)
	}
	var b map[string]any
	json.Unmarshal(out, &b)

	// System must stay exactly the template's block count (not increased by user system).
	if got, want := len(b["system"].([]any)), len(baseTmpl().System); got != want {
		t.Fatalf("system block count changed by user system: got %d, want %d", got, want)
	}
	msgs := b["messages"].([]any)
	first := msgs[0].(map[string]any)
	content := first["content"].([]any)
	block0 := content[0].(map[string]any)
	if !strings.Contains(block0["text"].(string), "AGENT INSTRUCTIONS") {
		t.Fatalf("agent instructions not folded into first user message: %v", block0)
	}
}

func TestMarshalBodyKeyOrder(t *testing.T) {
	out, _ := MergeUserRequest(
		[]byte(`{"model":"claude-sonnet-4-6","messages":[]}`), baseTmpl(),
		`{"device_id":"d","account_uuid":"a","session_id":"s"}`)
	s := string(out)
	// real CC order: model, messages, system, tools, metadata, max_tokens, thinking, context_management, output_config, stream
	order := []string{`"model"`, `"messages"`, `"system"`, `"tools"`, `"metadata"`, `"max_tokens"`, `"thinking"`, `"context_management"`, `"output_config"`, `"stream"`}
	last := -1
	for _, k := range order {
		i := strings.Index(s, k)
		if i < 0 {
			continue
		}
		if i < last {
			t.Fatalf("key %s out of order", k)
		}
		last = i
	}
}
