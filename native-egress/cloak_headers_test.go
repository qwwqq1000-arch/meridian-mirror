package main

import "testing"

func TestBuildHeaders(t *testing.T) {
	fp := Fingerprint{"user-agent": "claude-cli/2.1.185", "x-app": "cli", "anthropic-beta": "claude-code-20250219"}
	h := BuildHeaders(fp, "tok123", "sess-1", false, "")
	if h.Get("user-agent") != "claude-cli/2.1.185" {
		t.Fatalf("ua: %q", h.Get("user-agent"))
	}
	if h.Get("authorization") != "Bearer tok123" {
		t.Fatalf("auth: %q", h.Get("authorization"))
	}
	if h.Get("x-claude-code-session-id") != "sess-1" {
		t.Fatal("session id not set")
	}
	if h.Get("x-stainless-retry-count") != "0" {
		t.Fatal("retry-count")
	}
	// Real CC 2.1.198 sends accept: application/json (golden capture).
	if h.Get("accept") != "application/json" {
		t.Fatalf("accept: %q", h.Get("accept"))
	}
	if h.Get("connection") != "" {
		t.Fatal("connection header must not be set (real CC omits it)")
	}
}

func TestBuildHeadersNoClientRequestID(t *testing.T) {
	fp := Fingerprint{
		"user-agent":      "claude-cli/2.1.198 (external, sdk-cli)",
		"accept-encoding": "gzip, deflate, br, zstd",
		"x-app":           "cli",
	}
	h := BuildHeaders(fp, "tok", "sess-1", true, "")
	if h.Get("x-client-request-id") != "" {
		t.Fatal("must not send x-client-request-id")
	}
	if h.Get("accept-encoding") != "gzip, deflate, br, zstd" {
		t.Fatalf("accept-encoding not replayed: %q", h.Get("accept-encoding"))
	}
	if h.Get("x-claude-code-session-id") != "sess-1" {
		t.Fatal("session id not set")
	}
	if h.Get("user-agent") != "claude-cli/2.1.198 (external, sdk-cli)" {
		t.Fatal("user-agent not replayed")
	}
}

func TestBuildHeadersUnionsClientBeta(t *testing.T) {
	fp := Fingerprint{"anthropic-beta": "claude-code-20250219,oauth-2025-04-20"}
	h := BuildHeaders(fp, "t", "s", false, "structured-outputs-2025-12-15,oauth-2025-04-20")
	got := h.Get("anthropic-beta")
	want := "claude-code-20250219,oauth-2025-04-20,structured-outputs-2025-12-15"
	if got != want {
		t.Fatalf("beta union:\n got: %q\nwant: %q", got, want)
	}
}

func TestBuildHeadersNoBetaInjectionWithoutClient(t *testing.T) {
	fp := Fingerprint{"anthropic-beta": "claude-code-20250219"}
	h := BuildHeaders(fp, "t", "s", false, "")
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
