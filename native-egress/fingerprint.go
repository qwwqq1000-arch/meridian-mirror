package main

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

type Fingerprint map[string]string

var excluded = map[string]bool{
	"authorization": true, "x-claude-code-session-id": true,
	"x-stainless-retry-count": true, "content-length": true,
	"host": true, "connection": true,
	// accept-encoding is NOT excluded: real CC (undici) sends
	// "gzip, deflate, br, zstd"; we capture and replay it verbatim.
}

var (
	headersBlockRe = regexp.MustCompile(`(?s)headers:\s*\{(.*?)\}`)
	headerPairRe   = regexp.MustCompile(`(?:"([^"\n]+)"|([A-Za-z0-9-]+))\s*:\s*"([^"]*)"`)
)

func osEnviron() []string { return os.Environ() }

// ParseFingerprint extracts the complete header set from ANTHROPIC_LOG=debug
// output, dropping per-request/transport headers. ok=false unless a genuine
// claude-cli user-agent is present.
func ParseFingerprint(debugLog string) (Fingerprint, bool) {
	block := headersBlockRe.FindStringSubmatch(debugLog)
	if block == nil {
		return nil, false
	}
	fp := Fingerprint{}
	for _, m := range headerPairRe.FindAllStringSubmatch(block[1], -1) {
		key := strings.ToLower(m[1])
		if key == "" {
			key = strings.ToLower(m[2])
		}
		if key == "" || excluded[key] {
			continue
		}
		fp[key] = m[3]
	}
	if ua := fp["user-agent"]; ua == "" || !strings.HasPrefix(ua, "claude-cli/") {
		return nil, false
	}
	return fp, true
}

type fpEntry struct {
	fp         Fingerprint
	capturedAt time.Time
}

type FPCache struct {
	ttl     time.Duration
	capture func(configDir string) (string, error)
	mu      sync.Mutex
	entries map[string]fpEntry
}

func NewFPCache(ttl time.Duration, capture func(string) (string, error)) *FPCache {
	return &FPCache{ttl: ttl, capture: capture, entries: map[string]fpEntry{}}
}

// builtinFP is a hard-coded fingerprint derived from a real Claude Code 2.1.187
// session. Used as an immediate fallback so new nodes don't need a live CC
// request to activate the native path.
var builtinFP = Fingerprint{
	"user-agent":                            "claude-cli/2.1.196 (external, sdk-cli)",
	"anthropic-version":                     "2023-06-01",
	"anthropic-beta":                        "claude-code-20250219,oauth-2025-04-20,interleaved-thinking-2025-05-14,thinking-token-count-2026-05-13,context-management-2025-06-27,prompt-caching-scope-2026-01-05,advisor-tool-2026-03-01,advanced-tool-use-2025-11-20,effort-2025-11-24,extended-cache-ttl-2025-04-11,cache-diagnosis-2026-04-07",
	"anthropic-dangerous-direct-browser-access": "true",
	"x-stainless-lang":                      "js",
	"x-stainless-os":                        "Linux",
	"x-stainless-arch":                      "x64",
	"x-stainless-runtime":                   "node",
	"x-stainless-runtime-version":           "v26.3.0",
	"x-stainless-package-version":           "0.94.0",
	"x-stainless-timeout":                   "600",
	"x-app":                                 "cli",
}

func (c *FPCache) Get(account, configDir string, now time.Time) (Fingerprint, bool) {
	c.mu.Lock()
	e, ok := c.entries[account]
	c.mu.Unlock()
	if ok {
		// Always reuse the cached fingerprint — NEVER re-capture on the request
		// path. Warmup captures the real live fp once at startup. Re-running the
		// CLI here after the TTL would (a) BLOCK the request for multiple seconds
		// (the "requests hang" symptom), (b) stall native-egress so Node's fetch
		// to it times out (relay=degrade:fetch failed), and (c) on capture failure
		// (rate-limit/timeout) fall back to the stale hard-coded builtinFP — a
		// 2.1.187 fingerprint that carries x-client-request-id and omits the
		// x-stainless-* headers, so the outbound request stops matching real
		// 2.1.198 (the "extra/missing headers" symptom). A slightly-aged real
		// 2.1.198 fp beats all three. The pinned CLI version is identical across
		// restarts, so staleness is moot; POST /warmup refreshes it explicitly.
		if now.Sub(e.capturedAt) > c.ttl {
			logDD("fingerprint past ttl — reused (no request-path recapture)")
		}
		return e.fp, true
	}

	// Cold start only (a request arrived before warmup populated the cache):
	// capture once so the node is operational; warmup overwrites this shortly.
	log, err := c.capture(configDir)
	if err == nil {
		if fp, ok := ParseFingerprint(log); ok {
			c.mu.Lock()
			c.entries[account] = fpEntry{fp: fp, capturedAt: now}
			c.mu.Unlock()
			return fp, true
		}
	}
	logDD("cold-start fingerprint capture failed, using built-in fallback")
	c.mu.Lock()
	c.entries[account] = fpEntry{fp: builtinFP, capturedAt: now}
	c.mu.Unlock()
	return builtinFP, true
}

// Peek returns the first cached fingerprint (any account). Used by DatadogEmitter
// to read version/betas/node_version without importing lock internals.
func (c *FPCache) Peek() Fingerprint {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range c.entries {
		return e.fp
	}
	return nil
}

// defaultCapture returns a capture func that runs the real CLI with
// ANTHROPIC_LOG=debug to surface its outgoing headers. The returned func
// uses its per-call configDir argument (from the relay request's
// X-Native-Config-Dir), falling back to the server-startup configDir only
// when the caller passes "".
func defaultCapture(claudePath, fallbackConfigDir string) func(string) (string, error) {
	return func(configDir string) (string, error) {
		dir := configDir
		if dir == "" {
			dir = fallbackConfigDir
		}
		cmd := exec.Command(claudePath, "-p", "hi")
		cmd.Env = append(append([]string{}, osEnviron()...),
			"ANTHROPIC_LOG=debug", "CLAUDE_CONFIG_DIR="+resolveConfigDir(dir))
		out, _ := cmd.CombinedOutput()
		return string(out), nil
	}
}
