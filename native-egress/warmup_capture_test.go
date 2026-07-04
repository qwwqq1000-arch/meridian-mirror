package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// A fake claude that POSTs a CC-shaped request to $ANTHROPIC_BASE_URL/v1/messages.
func fakeClaudeScript() string {
	return `#!/bin/sh
curl -s -X POST "$ANTHROPIC_BASE_URL/v1/messages" \
  -H 'user-agent: claude-cli/2.1.198 (external, sdk-cli)' \
  -H 'accept-encoding: gzip, deflate, br, zstd' \
  -H 'x-app: cli' \
  --data '{"model":"claude-sonnet-4-6","system":[{"type":"text","text":"You are a Claude agent, built on Anthropic'"'"'s Claude Agent SDK."}],"messages":[]}' >/dev/null`
}

func TestCaptureAllGetsHeadersAndBody(t *testing.T) {
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not available")
	}
	dir := t.TempDir()
	script := dir + "/claude"
	if err := writeExec(script, fakeClaudeScript()); err != nil {
		t.Fatal(err)
	}
	fp, body := captureAll(script, dir)
	if fp == nil || !strings.HasPrefix(fp["user-agent"], "claude-cli/2.1.198") {
		t.Fatalf("fingerprint not captured: %v", fp)
	}
	if fp["accept-encoding"] != "gzip, deflate, br, zstd" {
		t.Fatalf("accept-encoding not captured: %q", fp["accept-encoding"])
	}
	if !strings.Contains(string(body), "claude-sonnet-4-6") {
		t.Fatalf("body not captured: %s", body)
	}
}

func writeExec(path, content string) error {
	return os.WriteFile(path, []byte(content), 0755)
}
