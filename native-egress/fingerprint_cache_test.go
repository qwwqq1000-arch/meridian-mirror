package main

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestFPCacheHitAndExpiry(t *testing.T) {
	calls := 0
	c := NewFPCache(time.Minute, func(string) (string, error) {
		calls++
		return sampleDebug, nil
	})
	t0 := time.Unix(1000, 0)
	fp, ok := c.Get("acct", "/cfg", t0)
	if !ok || fp["x-app"] != "cli" {
		t.Fatalf("first get failed: %v %#v", ok, fp)
	}
	if _, ok := c.Get("acct", "/cfg", t0.Add(30*time.Second)); !ok || calls != 1 {
		t.Fatalf("should be cached, calls=%d", calls)
	}
	// After the TTL the cached fp is REUSED, not re-captured: re-capturing on the
	// request path hangs the request and can fall back to the stale builtin fp.
	if _, ok := c.Get("acct", "/cfg", t0.Add(2*time.Minute)); !ok || calls != 1 {
		t.Fatalf("must reuse cached fp past TTL without recapture, calls=%d", calls)
	}
}

func TestFPCaptureFailureFallsBackToBuiltin(t *testing.T) {
	c := NewFPCache(time.Minute, func(string) (string, error) { return "", errors.New("boom") })
	fp, ok := c.Get("a", "/c", time.Unix(1, 0))
	if !ok {
		t.Fatal("capture failure must fall back to built-in fingerprint")
	}
	if !strings.HasPrefix(fp["user-agent"], "claude-cli/") {
		t.Fatalf("built-in fingerprint must have claude-cli UA, got: %s", fp["user-agent"])
	}
}
