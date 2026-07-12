package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// captureTmpl mimics a real captured CC template with raw byte fields populated,
// so MergeUserRequest splices real-CC bytes (byte-perfect key order).
func captureTmpl() *BodyTemplate {
	body := []byte(`{"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.198.542;"},{"type":"text","text":"You are a Claude agent, built on Anthropic's Claude Agent SDK.","cache_control":{"type":"ephemeral","ttl":"1h"}},{"type":"text","text":"interactive","cache_control":{"type":"ephemeral","ttl":"1h"}}],"tools":[{"name":"Bash","description":"run","input_schema":{"type":"object"}},{"name":"Read","description":"read","input_schema":{"type":"object"}}],"context_management":{"edits":[{"type":"clear_thinking_20251015","keep":"all"}]},"thinking":{"type":"adaptive","display":"omitted"}}`)
	t := &BodyTemplate{Stream: true}
	var parsed map[string]any
	json.Unmarshal(body, &parsed)
	t.System = parsed["system"].([]any)
	t.Tools = parsed["tools"].([]any)
	t.ContextManagement = parsed["context_management"]
	var raw map[string]json.RawMessage
	json.Unmarshal(body, &raw)
	t.SystemRaw = raw["system"]
	t.ToolsRaw = raw["tools"]
	t.ContextManagementRaw = raw["context_management"]
	return t
}

const uid = `{"device_id":"d","account_uuid":"a","session_id":"s"}`

func merge(t *testing.T, in string, tmpl *BodyTemplate) []byte {
	out, err := MergeUserRequest([]byte(in), tmpl, uid, true, true)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// Document/image/tool_result blocks must keep the CLIENT's exact byte order.
func TestDocumentBlockOrderPreserved(t *testing.T) {
	in := `{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":[{"type":"document","source":{"type":"base64","media_type":"application/pdf","data":"AAAA"}}]}]}`
	out := string(merge(t, in, captureTmpl()))
	want := `{"type":"document","source":{"type":"base64","media_type":"application/pdf","data":"AAAA"}}`
	if !strings.Contains(out, want) {
		t.Fatalf("document block order not preserved.\nwant substring: %s\ngot: %s", want, out)
	}
}

func TestMessageRoleFirst(t *testing.T) {
	in := `{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`
	out := string(merge(t, in, captureTmpl()))
	if !strings.Contains(out, `{"role":"user","content":`) {
		t.Fatalf("message must be role-first, got: %s", out)
	}
}

func TestThinkingExactBytes(t *testing.T) {
	nonHaiku := string(merge(t, `{"model":"claude-opus-4-8","messages":[]}`, captureTmpl()))
	if !strings.Contains(nonHaiku, `"thinking":{"type":"adaptive","display":"omitted"}`) {
		t.Fatalf("non-haiku thinking bytes wrong: %s", nonHaiku)
	}
	haiku := string(merge(t, `{"model":"claude-haiku-4-5-20251001","messages":[]}`, captureTmpl()))
	if !strings.Contains(haiku, `"thinking":{"budget_tokens":31999,"type":"enabled","display":"omitted"}`) {
		t.Fatalf("haiku thinking bytes wrong: %s", haiku)
	}
	if strings.Contains(haiku, `output_config`) {
		t.Fatalf("haiku must not carry output_config: %s", haiku)
	}
}

func TestOutputConfigNonHaiku(t *testing.T) {
	out := string(merge(t, `{"model":"claude-sonnet-4-6","messages":[]}`, captureTmpl()))
	if !strings.Contains(out, `"output_config":{"effort":"high"}`) {
		t.Fatalf("output_config bytes wrong: %s", out)
	}
}

func TestMultiturnAssistantByteIdentical(t *testing.T) {
	assistant := `{"role":"assistant","content":[{"type":"thinking","thinking":"x<y&z","signature":"SIG=="},{"type":"text","text":""},{"type":"tool_use","id":"t","name":"Bash","input":{}}]}`
	in := `{"model":"claude-opus-4-8","messages":[{"role":"user","content":[{"type":"text","text":"a"}]},` + assistant + `,{"role":"user","content":[{"type":"tool_result","tool_use_id":"t","content":"r"}]}]}`
	out := string(merge(t, in, captureTmpl()))
	// assistant turn preserved byte-for-byte (thinking signature + empty text intact)
	if !strings.Contains(out, assistant) {
		t.Fatalf("assistant turn not byte-identical.\nwant: %s\ngot: %s", assistant, out)
	}
	var b map[string]any
	json.Unmarshal(merge(t, in, captureTmpl()), &b)
	if n := len(b["messages"].([]any)); n != 3 {
		t.Fatalf("expected 3 messages, got %d", n)
	}
}

func TestEmptyUserTextStripped(t *testing.T) {
	in := `{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":[{"type":"text","text":""},{"type":"text","text":"real"}]}]}`
	out := merge(t, in, captureTmpl())
	var b map[string]any
	json.Unmarshal(out, &b)
	content := b["messages"].([]any)[0].(map[string]any)["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("empty text not stripped, content=%v", content)
	}
	if content[0].(map[string]any)["text"] != "real" {
		t.Fatalf("wrong block kept: %v", content[0])
	}
}

func TestUserAllEmptyGetsPlaceholder(t *testing.T) {
	in := `{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":[{"type":"text","text":""}]}]}`
	out := merge(t, in, captureTmpl())
	var b map[string]any
	json.Unmarshal(out, &b)
	content := b["messages"].([]any)[0].(map[string]any)["content"].([]any)
	if len(content) != 1 || content[0].(map[string]any)["text"] != "." {
		t.Fatalf("expected placeholder '.', got %v", content)
	}
}

func TestToolsOrderAndFilter(t *testing.T) {
	in := `{"model":"claude-sonnet-4-6","messages":[],"tools":[{"name":"Read"},{"name":"CustomToolA"},{"name":"mcp__x__y","description":"m","input_schema":{"type":"object"}}]}`
	out := string(merge(t, in, captureTmpl()))
	if !strings.Contains(out, `{"name":"Bash","description":"run","input_schema":{"type":"object"}}`) {
		t.Fatalf("base tool order not preserved (name,description,input_schema): %s", out)
	}
	if !strings.Contains(out, `"mcp__x__y"`) {
		t.Fatal("MCP tool must be kept")
	}
	if strings.Contains(out, "CustomToolA") {
		t.Fatal("non-CC tool must be dropped")
	}
}

func TestSystemSplicedRaw(t *testing.T) {
	tmpl := captureTmpl()
	out := string(merge(t, `{"model":"claude-sonnet-4-6","messages":[]}`, tmpl))
	if !strings.Contains(out, `"system":`+string(tmpl.SystemRaw)) {
		t.Fatalf("system not spliced verbatim.\nraw: %s\ngot: %s", tmpl.SystemRaw, out)
	}
	// context_management spliced with type,keep order
	if !strings.Contains(out, `"context_management":{"edits":[{"type":"clear_thinking_20251015","keep":"all"}]}`) {
		t.Fatalf("context_management order not preserved: %s", out)
	}
}

func TestFoldSystemPreservesRoleFirst(t *testing.T) {
	in := `{"model":"claude-sonnet-4-6","system":"AGENT RULES","messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`
	out := string(merge(t, in, captureTmpl()))
	if !strings.Contains(out, `{"role":"user","content":[{"type":"text","text":"AGENT RULES"}`) {
		t.Fatalf("folded system not first block, role-first: %s", out)
	}
}
