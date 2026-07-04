package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

var errAlways = &alwaysErr{}

type alwaysErr struct{}

func (e *alwaysErr) Error() string { return "always error" }

func writeTempCreds(t *testing.T, token string) string {
	t.Helper()
	dir := t.TempDir()
	creds := map[string]any{
		"claudeAiOauth": map[string]any{
			"accessToken":  token,
			"refreshToken": "refresh-xyz",
			"expiresAt":    time.Now().Add(time.Hour).UnixMilli(),
		},
	}
	b, _ := json.Marshal(creds)
	if err := os.WriteFile(filepath.Join(dir, ".credentials.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func relayReqRaw(dir, account string, body []byte) *http.Request {
	req := httptest.NewRequest("POST", "/relay", bytes.NewReader(body))
	req.Header.Set("X-Native-Config-Dir", dir)
	req.Header.Set("X-Native-Account", account)
	return req
}

func relayReqStream(dir, account string, body []byte) *http.Request {
	req := relayReqRaw(dir, account, body)
	req.Header.Set("X-Native-Stream", "1")
	return req
}

const minimalSSE = `event: message_start
data: {"type":"message_start","message":{"id":"msg_test","type":"message","role":"assistant","content":[],"model":"claude-opus-4-6","stop_reason":null,"usage":{"input_tokens":1,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}

event: message_stop
data: {"type":"message_stop"}

`

func sseResponse() *http.Response {
	return &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": {"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(minimalSSE)),
	}
}

func TestRelayFallsBackToBuiltinFingerprint(t *testing.T) {
	dir := writeTempCreds(t, "tok-fp-test")
	var gotUA string
	deps := RelayDeps{
		Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
			gotUA = r.Header.Get("User-Agent")
			return sseResponse(), nil
		}),
		FP:        NewFPCache(time.Minute, func(string) (string, error) { return "", errAlways }),
		SessionID: func(string) string { return "s" },
		Now:       time.Now,
	}
	rec := httptest.NewRecorder()
	req := relayReqRaw(dir, "a", []byte(`{"messages":[]}`))
	relayHandler(deps)(rec, req)
	if rec.Header().Get("X-Degrade") == "1" {
		t.Fatalf("should not degrade — builtin fingerprint should be used, got reason: %s", rec.Header().Get("X-Degrade-Reason"))
	}
	if !strings.HasPrefix(gotUA, "claude-cli/") {
		t.Fatalf("expected builtin UA, got %q", gotUA)
	}
}

func TestRelayForwardsWithCloak(t *testing.T) {
	var gotAuth, gotUA string
	deps := RelayDeps{
		Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
			gotAuth = r.Header.Get("authorization")
			gotUA = r.Header.Get("user-agent")
			return sseResponse(), nil
		}),
		FP:        NewFPCache(time.Minute, func(string) (string, error) { return sampleDebug, nil }),
		SessionID: func(string) string { return "s" },
		Now:       time.Now,
	}
	dir := writeTempCreds(t, "tok-abc")
	rec := httptest.NewRecorder()
	req := relayReqStream(dir, "a", []byte(`{"messages":[]}`))
	relayHandler(deps)(rec, req)
	if gotAuth != "Bearer tok-abc" {
		t.Fatalf("auth: %q", gotAuth)
	}
	if gotUA == "Go-http-client/1.1" || gotUA == "" {
		t.Fatalf("ua not cloaked: %q", gotUA)
	}
}

func TestRelayForwardsBodyVerbatim(t *testing.T) {
	raw := []byte(`{"model":"x","system":[{"type":"text","text":"You are Claude Code, Anthropic's official CLI for Claude, running within the Claude Agent SDK.","cache_control":{"type":"ephemeral"}}],"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"a<b>c&d","signature":"dGVzdC1zaWduYXR1cmUtZGF0YQ=="}]}]}`)
	var sent []byte
	deps := RelayDeps{
		Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
			sent, _ = io.ReadAll(r.Body)
			return sseResponse(), nil
		}),
		FP:        NewFPCache(time.Minute, func(string) (string, error) { return sampleDebug, nil }),
		SessionID: func(string) string { return "s" },
		Now:       time.Now,
	}
	dir := writeTempCreds(t, "tok")
	rec := httptest.NewRecorder()
	relayHandler(deps)(rec, relayReqStream(dir, "a", raw))
	if string(sent) != string(raw) {
		t.Fatalf("body must be verbatim.\n got: %s\nwant: %s", sent, raw)
	}
}

func TestRelayNonStreamAssemblesSSE(t *testing.T) {
	deps := RelayDeps{
		Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
			if r.Header.Get("Accept") != "application/json" {
				t.Fatal("upstream request accept header must match real CLI")
			}
			return sseResponse(), nil
		}),
		FP:        NewFPCache(time.Minute, func(string) (string, error) { return sampleDebug, nil }),
		SessionID: func(string) string { return "s" },
		Now:       time.Now,
	}
	dir := writeTempCreds(t, "tok-ns")
	rec := httptest.NewRecorder()
	req := relayReqRaw(dir, "a", []byte(`{"messages":[]}`))
	relayHandler(deps)(rec, req)
	if rec.Header().Get("X-Degrade") == "1" {
		t.Fatalf("unexpected degrade: %s", rec.Header().Get("X-Degrade-Reason"))
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("non-stream response should be JSON, got: %q", ct)
	}
	var msg map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &msg); err != nil {
		t.Fatalf("response not valid JSON: %s\nbody: %s", err, rec.Body.String())
	}
	if msg["id"] != "msg_test" {
		t.Fatalf("assembled message id: %v", msg["id"])
	}
	content, _ := msg["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("content blocks: %d", len(content))
	}
	block, _ := content[0].(map[string]any)
	if block["text"] != "ok" {
		t.Fatalf("text: %q", block["text"])
	}
}

func TestRedactAuth(t *testing.T) {
	h := http.Header{}
	h.Set("Authorization", "Bearer supersecrettoken1234")
	h.Set("Content-Type", "application/json")
	redacted := RedactAuth(h)
	if redacted.Get("Authorization") != "Bearer ***1234" {
		t.Fatalf("unexpected redaction: %q", redacted.Get("Authorization"))
	}
	if h.Get("Authorization") != "Bearer supersecrettoken1234" {
		t.Fatal("original header was mutated")
	}
	if redacted.Get("Content-Type") != "application/json" {
		t.Fatal("other headers must be preserved")
	}
}

func TestRelayDegradesOnUpstreamNon2xx(t *testing.T) {
	deps := RelayDeps{
		Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 401, Body: http.NoBody, Header: http.Header{}}, nil
		}),
		FP:        NewFPCache(time.Minute, func(string) (string, error) { return sampleDebug, nil }),
		SessionID: func(string) string { return "s" },
		Now:       time.Now,
	}
	dir := writeTempCreds(t, "tok-xyz")
	rec := httptest.NewRecorder()
	req := relayReqRaw(dir, "a", []byte(`{"messages":[]}`))
	relayHandler(deps)(rec, req)
	if rec.Header().Get("X-Degrade") != "1" {
		t.Fatalf("expected degrade on 401, got code %d", rec.Code)
	}
	if rec.Header().Get("X-Degrade-Reason") != "upstream_401" {
		t.Fatalf("expected upstream_401 reason, got %q", rec.Header().Get("X-Degrade-Reason"))
	}
}

func TestRelayDegradesOnMissingToken(t *testing.T) {
	deps := RelayDeps{
		Transport: rtFunc(func(*http.Request) (*http.Response, error) { t.Fatal("must not forward"); return nil, nil }),
		FP:        NewFPCache(time.Minute, func(string) (string, error) { return sampleDebug, nil }),
		SessionID: func(string) string { return "s" },
		Now:       time.Now,
	}
	dir := t.TempDir()
	rec := httptest.NewRecorder()
	req := relayReqRaw(dir, "a", []byte(`{"messages":[]}`))
	relayHandler(deps)(rec, req)
	if rec.Header().Get("X-Degrade") != "1" {
		t.Fatalf("expected degrade on missing token, got code %d", rec.Code)
	}
}
