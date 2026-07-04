package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAssembleSSEBasicText(t *testing.T) {
	sse := `event: message_start
data: {"type":"message_start","message":{"id":"msg_01","type":"message","role":"assistant","content":[],"model":"claude-opus-4-6","stop_reason":null,"usage":{"input_tokens":10,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello "}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"world"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}

event: message_stop
data: {"type":"message_stop"}

`
	result, err := assembleSSEToMessage(strings.NewReader(sse))
	if err != nil {
		t.Fatal(err)
	}
	var msg map[string]any
	if err := json.Unmarshal(result, &msg); err != nil {
		t.Fatalf("invalid JSON: %s", err)
	}
	if msg["id"] != "msg_01" {
		t.Fatalf("id: %v", msg["id"])
	}
	if msg["stop_reason"] != "end_turn" {
		t.Fatalf("stop_reason: %v", msg["stop_reason"])
	}
	content, _ := msg["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("content blocks: %d", len(content))
	}
	block, _ := content[0].(map[string]any)
	if block["text"] != "Hello world" {
		t.Fatalf("text: %q", block["text"])
	}
	usage, _ := msg["usage"].(map[string]any)
	if usage["output_tokens"] != float64(5) {
		t.Fatalf("output_tokens: %v", usage["output_tokens"])
	}
	if usage["input_tokens"] != float64(10) {
		t.Fatalf("input_tokens: %v", usage["input_tokens"])
	}
}

func TestAssembleSSEThinkingAndToolUse(t *testing.T) {
	sse := `event: message_start
data: {"type":"message_start","message":{"id":"msg_02","type":"message","role":"assistant","content":[],"model":"claude-opus-4-6","stop_reason":null,"usage":{"input_tokens":50,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Let me think..."}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"SIG123=="}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_01","name":"bash","input":{}}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"command\":"}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"ls\"}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":1}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":20}}

event: message_stop
data: {"type":"message_stop"}

`
	result, err := assembleSSEToMessage(strings.NewReader(sse))
	if err != nil {
		t.Fatal(err)
	}
	var msg map[string]any
	if err := json.Unmarshal(result, &msg); err != nil {
		t.Fatalf("invalid JSON: %s", err)
	}
	content, _ := msg["content"].([]any)
	if len(content) != 2 {
		t.Fatalf("content blocks: %d", len(content))
	}
	think, _ := content[0].(map[string]any)
	if think["thinking"] != "Let me think..." {
		t.Fatalf("thinking: %q", think["thinking"])
	}
	if think["signature"] != "SIG123==" {
		t.Fatalf("signature: %q", think["signature"])
	}
	tool, _ := content[1].(map[string]any)
	if tool["name"] != "bash" {
		t.Fatalf("tool name: %v", tool["name"])
	}
	// input is accumulated as string from input_json_delta
	if tool["input"] != `{"command":"ls"}` {
		t.Fatalf("tool input: %q", tool["input"])
	}
}

func TestAssembleSSETruncated(t *testing.T) {
	// Stream cut off after some content but before message_stop
	sse := `event: message_start
data: {"type":"message_start","message":{"id":"msg_trunc","type":"message","role":"assistant","content":[],"model":"claude-opus-4-6","stop_reason":null,"usage":{"input_tokens":100,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partial output"}}

`
	result, err := assembleSSEToMessage(strings.NewReader(sse))
	if err != nil {
		t.Fatalf("expected no error for partial message, got: %v", err)
	}
	var msg map[string]any
	if err := json.Unmarshal(result, &msg); err != nil {
		t.Fatalf("invalid JSON: %s", err)
	}
	if msg["stop_reason"] != "truncated" {
		t.Fatalf("expected stop_reason=truncated, got: %v", msg["stop_reason"])
	}
	content, _ := msg["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("content blocks: %d", len(content))
	}
	block, _ := content[0].(map[string]any)
	if block["text"] != "partial output" {
		t.Fatalf("text: %q", block["text"])
	}
}

func TestAssembleSSEUpstreamError(t *testing.T) {
	// Upstream sends error event instead of message
	sse := `event: error
data: {"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}

`
	result, err := assembleSSEToMessage(strings.NewReader(sse))
	if err == nil {
		t.Fatal("expected error for upstream SSE error")
	}
	if result == nil {
		t.Fatal("expected error bytes to be returned")
	}
	var errMsg map[string]any
	if err := json.Unmarshal(result, &errMsg); err != nil {
		t.Fatalf("invalid error JSON: %s", err)
	}
	errObj, _ := errMsg["error"].(map[string]any)
	if errObj["type"] != "overloaded_error" {
		t.Fatalf("error type: %v", errObj["type"])
	}
}

func TestAssembleSSEEmptyStream(t *testing.T) {
	// Completely empty stream
	result, err := assembleSSEToMessage(strings.NewReader(""))
	if err == nil {
		t.Fatal("expected error for empty stream")
	}
	if result != nil {
		t.Fatal("expected nil result for empty stream")
	}
}

func TestForceStreamTrue(t *testing.T) {
	body := []byte(`{"model":"x","stream":false,"messages":[]}`)
	result := forceStreamTrue(body)
	if !strings.Contains(string(result), `"stream":true`) {
		t.Fatalf("expected stream:true, got: %s", result)
	}
	// Already true — no change
	already := []byte(`{"model":"x","stream":true,"messages":[]}`)
	result2 := forceStreamTrue(already)
	if string(result2) != string(already) {
		t.Fatalf("should not change: %s", result2)
	}
}
