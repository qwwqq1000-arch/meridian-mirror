package main

import "testing"

const sampleDebug = `
[log_x] sending request {
  url: "https://api.anthropic.com/v1/messages?beta=true",
  headers: {
    accept: "application/json",
    "anthropic-beta": "claude-code-20250219,oauth-2025-04-20,effort-2025-11-24",
    "anthropic-version": "2023-06-01",
    "user-agent": "claude-cli/2.1.185 (external, cli, agent-sdk/0.3.183)",
    "x-app": "cli",
    "x-stainless-os": "Linux",
    "x-stainless-arch": "x64",
    "x-stainless-retry-count": "0",
    authorization: "Bearer secret"
  }
}`

func TestParseFingerprint(t *testing.T) {
	fp, ok := ParseFingerprint(sampleDebug)
	if !ok {
		t.Fatal("expected ok")
	}
	if fp["user-agent"] != "claude-cli/2.1.185 (external, cli, agent-sdk/0.3.183)" {
		t.Fatalf("ua: %q", fp["user-agent"])
	}
	if fp["x-app"] != "cli" || fp["x-stainless-os"] != "Linux" {
		t.Fatalf("missing headers: %#v", fp)
	}
	if _, bad := fp["authorization"]; bad {
		t.Fatal("authorization must be excluded")
	}
	if _, bad := fp["x-stainless-retry-count"]; bad {
		t.Fatal("retry-count must be excluded (per-request)")
	}
}

func TestParseFingerprintRejectsNonCLI(t *testing.T) {
	if _, ok := ParseFingerprint(`headers: { "user-agent": "Go-http-client/1.1" }`); ok {
		t.Fatal("non-claude-cli UA must be rejected")
	}
	if _, ok := ParseFingerprint("no headers"); ok {
		t.Fatal("missing block must be rejected")
	}
}
