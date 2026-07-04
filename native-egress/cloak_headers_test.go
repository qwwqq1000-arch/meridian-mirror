package main

import "testing"

func TestBuildHeaders(t *testing.T) {
	fp := Fingerprint{"user-agent": "claude-cli/2.1.185", "x-app": "cli", "anthropic-beta": "claude-code-20250219"}
	h := BuildHeaders(fp, "tok123", "sess-1", "req-1", false, "")
	if h.Get("user-agent") != "claude-cli/2.1.185" {
		t.Fatalf("ua: %q", h.Get("user-agent"))
	}
	if h.Get("authorization") != "Bearer tok123" {
		t.Fatalf("auth: %q", h.Get("authorization"))
	}
	if h.Get("x-claude-code-session-id") != "sess-1" || h.Get("x-client-request-id") != "req-1" {
		t.Fatal("session/request id not set")
	}
	if h.Get("x-stainless-retry-count") != "0" {
		t.Fatal("retry-count")
	}
	// Always SSE — NE assembles to JSON for non-stream clients
	if h.Get("accept") != "text/event-stream" {
		t.Fatalf("accept: %q (should always be SSE)", h.Get("accept"))
	}
	if h.Get("connection") != "" {
		t.Fatal("connection header must not be set (real CC omits it)")
	}
	if h.Get("accept-encoding") != "" {
		t.Fatal("accept-encoding must not be set (real CC omits it)")
	}
}

func TestBuildHeadersStreamAccept(t *testing.T) {
	h := BuildHeaders(Fingerprint{"user-agent": "claude-cli/x"}, "t", "s", "r", true, "")
	if h.Get("accept") != "text/event-stream" {
		t.Fatalf("stream accept: %q", h.Get("accept"))
	}
}

func TestBuildHeadersUnionsClientBeta(t *testing.T) {
	fp := Fingerprint{"anthropic-beta": "claude-code-20250219,oauth-2025-04-20"}
	h := BuildHeaders(fp, "t", "s", "r", false, "structured-outputs-2025-12-15,oauth-2025-04-20")
	got := h.Get("anthropic-beta")
	want := "claude-code-20250219,oauth-2025-04-20,structured-outputs-2025-12-15"
	if got != want {
		t.Fatalf("beta union:\n got: %q\nwant: %q", got, want)
	}
}

func TestBuildHeadersNoBetaInjectionWithoutClient(t *testing.T) {
	fp := Fingerprint{"anthropic-beta": "claude-code-20250219"}
	h := BuildHeaders(fp, "t", "s", "r", false, "")
	got := h.Get("anthropic-beta")
	if got != "claude-code-20250219" {
		t.Fatalf("expected only captured beta, got: %q", got)
	}
}

func TestUnionAnthropicBeta(t *testing.T) {
	if got := unionAnthropicBeta("a, b", "b ,c", ""); got != "a,b,c" {
		t.Fatalf("union: %q", got)
	}
	if got := unionAnthropicBeta("", "x"); got != "x" {
		t.Fatalf("union empty fp: %q", got)
	}
}
