package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// assembleSSEToMessage reads a full SSE stream from an Anthropic streaming
// response and reconstructs the final non-streaming Message JSON.
//
// This allows NE to always stream from upstream (fast TTFB, natural for the
// fingerprint) but return a regular JSON body to clients that asked for
// non-streaming.
func assembleSSEToMessage(r io.Reader) ([]byte, error) {
	var msg map[string]any
	var contentBlocks []any
	var usage map[string]any
	var lastSSEError []byte
	gotStop := false

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 512*1024), 2*1024*1024)

	var eventType string
	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "event: ") {
			eventType = strings.TrimPrefix(line, "event: ")
			continue
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := []byte(strings.TrimPrefix(line, "data: "))

		switch eventType {
		case "error":
			lastSSEError = append([]byte(nil), data...)

		case "message_start":
			var envelope struct {
				Message map[string]any `json:"message"`
			}
			if json.Unmarshal(data, &envelope) == nil && envelope.Message != nil {
				msg = envelope.Message
				if u, ok := msg["usage"].(map[string]any); ok {
					usage = u
				}
				if cb, ok := msg["content"].([]any); ok {
					contentBlocks = cb
				} else {
					contentBlocks = []any{}
				}
			}

		case "content_block_start":
			var evt struct {
				Index        int            `json:"index"`
				ContentBlock map[string]any `json:"content_block"`
			}
			if json.Unmarshal(data, &evt) == nil && evt.ContentBlock != nil {
				for len(contentBlocks) <= evt.Index {
					contentBlocks = append(contentBlocks, map[string]any{})
				}
				contentBlocks[evt.Index] = evt.ContentBlock
			}

		case "content_block_delta":
			var evt struct {
				Index int            `json:"index"`
				Delta map[string]any `json:"delta"`
			}
			if json.Unmarshal(data, &evt) == nil && evt.Delta != nil {
				if evt.Index < len(contentBlocks) {
					block, _ := contentBlocks[evt.Index].(map[string]any)
					if block != nil {
						applyDelta(block, evt.Delta)
					}
				}
			}

		case "content_block_stop":
			// nothing to do

		case "message_delta":
			var evt struct {
				Delta map[string]any `json:"delta"`
				Usage map[string]any `json:"usage"`
			}
			if json.Unmarshal(data, &evt) == nil {
				if msg != nil && evt.Delta != nil {
					for k, v := range evt.Delta {
						msg[k] = v
					}
				}
				if evt.Usage != nil {
					if usage == nil {
						usage = map[string]any{}
					}
					for k, v := range evt.Usage {
						usage[k] = v
					}
				}
			}

		case "message_stop":
			gotStop = true
		}
	}

	// Got message_start — return whatever we assembled, even if truncated.
	if msg != nil {
		if !gotStop {
			if sr, _ := msg["stop_reason"].(string); sr == "" {
				msg["stop_reason"] = "truncated"
			}
		}
		msg["content"] = contentBlocks
		if usage != nil {
			msg["usage"] = usage
		}
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(msg); err != nil {
			return nil, err
		}
		b := buf.Bytes()
		if len(b) > 0 && b[len(b)-1] == '\n' {
			b = b[:len(b)-1]
		}
		return b, nil
	}

	// No message_start but got an upstream error event — return error bytes.
	if lastSSEError != nil {
		return lastSSEError, fmt.Errorf("upstream SSE error")
	}

	// Surface the real reason the stream ended before any message_start: a read
	// error (context canceled, network reset, buffer exceeded) is distinct from a
	// clean EOF where the upstream sent 200 then closed with an empty body.
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("sse read: %w", err)
	}
	return nil, io.ErrUnexpectedEOF
}

func applyDelta(block, delta map[string]any) {
	deltaType, _ := delta["type"].(string)
	switch deltaType {
	case "text_delta":
		if text, ok := delta["text"].(string); ok {
			existing, _ := block["text"].(string)
			block["text"] = existing + text
		}
	case "thinking_delta":
		if thinking, ok := delta["thinking"].(string); ok {
			existing, _ := block["thinking"].(string)
			block["thinking"] = existing + thinking
		}
	case "signature_delta":
		if sig, ok := delta["signature"].(string); ok {
			block["signature"] = sig
		}
	case "input_json_delta":
		if partial, ok := delta["partial_json"].(string); ok {
			existing, _ := block["input"].(string)
			block["input"] = existing + partial
		}
	}
}
