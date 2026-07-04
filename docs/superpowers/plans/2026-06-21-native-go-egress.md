# Native Go Egress Subsystem — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace Meridian's ineffective Node native forwarding with a Go `native-egress` subsystem that reaches `api.anthropic.com` with a genuine Claude Code fingerprint (uTLS TLS + captured headers + body cloak), so requests relayed via new-api return 200 instead of 429.

**Architecture:** A standalone Go binary (`native-egress/`) owns the full native path (OAuth read/refresh, dynamic fingerprint capture, three-layer disguise, forward, stream). Meridian (Node) spawns it as a managed child process on loopback HTTP, routes native-eligible requests to it, and degrades to the existing SDK passthrough on any native failure. The two existing modes (`internal`, SDK `passthrough`) are untouched.

**Tech Stack:** Go (`github.com/refraction-networking/utls`, `golang.org/x/net/http2`), Node/TypeScript (Hono), bun test, Docker (multi-stage with Go toolchain).

## Global Constraints

- Native is **off by default** and **additive**: `internal` / SDK `passthrough` behavior must be byte-identical when native is off.
- **Prefer-native-with-degrade:** any native failure (sidecar down / spawn fail / health fail / connection error / capture fail `X-Degrade:1` / upstream non-2xx incl 429) → degrade to SDK passthrough; the request still completes. This supersedes any earlier "return error" rule.
- 🔒 `authorization` Bearer token is **always redacted** in logs (`Bearer ***<last4>`). Never log the raw token.
- **No sensitive-word / content obfuscation** — out of scope.
- TLS layer uses uTLS `tls.HelloChrome_Auto` (fixed Chrome approximation; uTLS has no `claude-cli` preset).
- Fingerprint headers/body are **dynamically captured** from the local CLI (`ANTHROPIC_LOG=debug claude -p hi` → parse the `headers{}` block); never hard-pinned.
- OAuth constants (reuse, do not redefine): token URL `https://platform.claude.com/v1/oauth/token`, client_id `9d1c250a-e61b-44d9-88ed-5944d1962f5e`; creds file shape `{"claudeAiOauth":{"accessToken","refreshToken","expiresAt"}}`.
- Anthropic endpoint: `https://api.anthropic.com/v1/messages?beta=true`.
- Config: `MERIDIAN_NATIVE_FORWARD` (global enable), per-adapter `nativeForward`/`nativeBodyCheck`, `x-meridian-mode: sdk` (opt-out only), `MERIDIAN_NATIVE_FINGERPRINT_TTL` (default `5m`), `MERIDIAN_NATIVE_DEBUG` (default `1`).
- Go module path: `github.com/rynfar/meridian/native-egress`. Go ≥ 1.22.
- Node tests in `src/__tests__/`; Go tests are `*_test.go` beside the source. `npm run typecheck` + `npm test` must stay green; `cd native-egress && go test ./... && go vet ./...` must pass.
- Commit format `type: brief description`; no AI attribution. Branch `feat/native-go-egress`.

---

## Phase A — Go `native-egress` subsystem

### Task 1: Go module scaffold + health endpoint

**Files:**
- Create: `native-egress/go.mod`, `native-egress/main.go`, `native-egress/main_test.go`

**Interfaces:**
- Produces: an HTTP server with `GET /health` → `200 {"ok":true}`; `newServer() http.Handler`.

- [ ] **Step 1: Write the failing test**

`native-egress/main_test.go`:
```go
package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealth(t *testing.T) {
	srv := httptest.NewServer(newServer())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
}
```

- [ ] **Step 2: Init module + run test (fails: no newServer)**

```bash
cd native-egress && go mod init github.com/rynfar/meridian/native-egress && go test ./...
```
Expected: compile error `undefined: newServer`.

- [ ] **Step 3: Minimal server**

`native-egress/main.go`:
```go
package main

import (
	"fmt"
	"net/http"
	"os"
)

func newServer() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	return mux
}

func main() {
	addr := os.Getenv("NATIVE_EGRESS_ADDR")
	if addr == "" {
		addr = "127.0.0.1:0"
	}
	fmt.Fprintf(os.Stderr, "native-egress listening on %s\n", addr)
	if err := http.ListenAndServe(addr, newServer()); err != nil {
		fmt.Fprintln(os.Stderr, "native-egress exited:", err)
		os.Exit(1)
	}
}
```

- [ ] **Step 4: Test passes**

Run: `cd native-egress && go test ./... && go vet ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add native-egress/go.mod native-egress/main.go native-egress/main_test.go
git commit -m "feat: scaffold native-egress go module with health endpoint"
```

---

### Task 2: Fingerprint debug-log parser (pure)

**Files:**
- Create: `native-egress/fingerprint.go`, `native-egress/fingerprint_test.go`

**Interfaces:**
- Produces: `type Fingerprint map[string]string`; `func ParseFingerprint(debugLog string) (Fingerprint, bool)` — extracts every header in the `headers{}` block except per-request/transport ones; returns `(fp, true)` only if a `claude-cli/` user-agent is present.

- [ ] **Step 1: Write the failing test**

`native-egress/fingerprint_test.go`:
```go
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
```

- [ ] **Step 2: Run test (fails: undefined ParseFingerprint)**

Run: `cd native-egress && go test ./...`
Expected: compile error.

- [ ] **Step 3: Implement**

`native-egress/fingerprint.go`:
```go
package main

import (
	"regexp"
	"strings"
)

type Fingerprint map[string]string

var excluded = map[string]bool{
	"authorization": true, "x-claude-code-session-id": true,
	"x-stainless-retry-count": true, "content-length": true,
	"host": true, "connection": true, "accept-encoding": true,
}

var (
	headersBlockRe = regexp.MustCompile(`(?s)headers:\s*\{(.*?)\}`)
	headerPairRe   = regexp.MustCompile(`(?:"([^"\n]+)"|([A-Za-z0-9-]+))\s*:\s*"([^"]*)"`)
)

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
```

- [ ] **Step 4: Test passes**

Run: `cd native-egress && go test ./... && go vet ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add native-egress/fingerprint.go native-egress/fingerprint_test.go
git commit -m "feat: parse genuine CLI fingerprint from debug log"
```

---

### Task 3: Fingerprint capture + TTL cache

**Files:**
- Modify: `native-egress/fingerprint.go`
- Create: `native-egress/fingerprint_cache_test.go`

**Interfaces:**
- Consumes: `ParseFingerprint`, `Fingerprint` (Task 2).
- Produces: `type FPCache struct{...}`; `NewFPCache(ttl time.Duration, capture func(configDir string) (string, error)) *FPCache`; `(*FPCache).Get(account, configDir string, now time.Time) (Fingerprint, bool)` — returns cached if fresh; on miss runs `capture` (which yields a debug log), parses, caches by `account`; returns `(nil,false)` if capture/parse fails (caller degrades). `defaultCapture(configDir)` runs `ANTHROPIC_LOG=debug claude -p hi`.

- [ ] **Step 1: Write the failing test**

`native-egress/fingerprint_cache_test.go`:
```go
package main

import (
	"errors"
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
	if _, ok := c.Get("acct", "/cfg", t0.Add(2*time.Minute)); !ok || calls != 2 {
		t.Fatalf("should recapture after TTL, calls=%d", calls)
	}
}

func TestFPCaptureFailureDegrades(t *testing.T) {
	c := NewFPCache(time.Minute, func(string) (string, error) { return "", errors.New("boom") })
	if _, ok := c.Get("a", "/c", time.Unix(1, 0)); ok {
		t.Fatal("capture failure must return ok=false")
	}
}
```

- [ ] **Step 2: Run test (fails)**

Run: `cd native-egress && go test ./...`
Expected: compile error (undefined NewFPCache).

- [ ] **Step 3: Implement (append to fingerprint.go)**

```go
import (
	"os/exec"
	"sync"
	"time"
)

type fpEntry struct {
	fp        Fingerprint
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

func (c *FPCache) Get(account, configDir string, now time.Time) (Fingerprint, bool) {
	c.mu.Lock()
	if e, ok := c.entries[account]; ok && now.Sub(e.capturedAt) <= c.ttl {
		c.mu.Unlock()
		return e.fp, true
	}
	c.mu.Unlock()

	log, err := c.capture(configDir)
	if err != nil {
		return nil, false
	}
	fp, ok := ParseFingerprint(log)
	if !ok {
		return nil, false
	}
	c.mu.Lock()
	c.entries[account] = fpEntry{fp: fp, capturedAt: now}
	c.mu.Unlock()
	return fp, true
}

// defaultCapture runs the real CLI to surface its outgoing headers.
func defaultCapture(claudePath, configDir string) func(string) (string, error) {
	return func(string) (string, error) {
		cmd := exec.Command(claudePath, "-p", "hi")
		cmd.Env = append(append([]string{}, osEnviron()...),
			"ANTHROPIC_LOG=debug", "CLAUDE_CONFIG_DIR="+configDir)
		out, _ := cmd.CombinedOutput() // headers are logged before any non-2xx; ignore exit code
		return string(out), nil
	}
}
```
Add a tiny `osEnviron` indirection in a new helper so tests don't depend on env:
```go
import "os"
func osEnviron() []string { return os.Environ() }
```

- [ ] **Step 4: Test passes**

Run: `cd native-egress && go test ./... && go vet ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add native-egress/fingerprint.go native-egress/fingerprint_cache_test.go
git commit -m "feat: fingerprint capture with TTL cache and degrade-on-failure"
```

---

### Task 4: Cloak headers (pure)

**Files:**
- Create: `native-egress/cloak_headers.go`, `native-egress/cloak_headers_test.go`

**Interfaces:**
- Consumes: `Fingerprint` (Task 2).
- Produces: `func BuildHeaders(fp Fingerprint, token, sessionID, clientRequestID string, stream bool) http.Header` — fingerprint verbatim + `authorization: Bearer <token>`, `content-type: application/json`, `x-stainless-retry-count: 0`, `x-claude-code-session-id`, `x-client-request-id`, `connection: keep-alive`, and `accept`/`accept-encoding` by `stream`.

- [ ] **Step 1: Write the failing test**

`native-egress/cloak_headers_test.go`:
```go
package main

import "testing"

func TestBuildHeaders(t *testing.T) {
	fp := Fingerprint{"user-agent": "claude-cli/2.1.185", "x-app": "cli", "anthropic-beta": "claude-code-20250219"}
	h := BuildHeaders(fp, "tok123", "sess-1", "req-1", false)
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
	if h.Get("accept") != "application/json" {
		t.Fatalf("non-stream accept: %q", h.Get("accept"))
	}
}

func TestBuildHeadersStreamAccept(t *testing.T) {
	h := BuildHeaders(Fingerprint{"user-agent": "claude-cli/x"}, "t", "s", "r", true)
	if h.Get("accept") != "text/event-stream" {
		t.Fatalf("stream accept: %q", h.Get("accept"))
	}
}
```

- [ ] **Step 2: Run test (fails)**

Run: `cd native-egress && go test ./...`
Expected: compile error.

- [ ] **Step 3: Implement**

`native-egress/cloak_headers.go`:
```go
package main

import "net/http"

func BuildHeaders(fp Fingerprint, token, sessionID, clientRequestID string, stream bool) http.Header {
	h := http.Header{}
	for k, v := range fp {
		h.Set(k, v)
	}
	h.Set("authorization", "Bearer "+token)
	h.Set("content-type", "application/json")
	h.Set("x-stainless-retry-count", "0")
	h.Set("x-claude-code-session-id", sessionID)
	h.Set("x-client-request-id", clientRequestID)
	h.Set("connection", "keep-alive")
	if stream {
		h.Set("accept", "text/event-stream")
		h.Set("accept-encoding", "identity")
	} else {
		h.Set("accept", "application/json")
		h.Set("accept-encoding", "gzip, deflate, br, zstd")
	}
	return h
}
```

- [ ] **Step 4: Test passes**

Run: `cd native-egress && go test ./... && go vet ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add native-egress/cloak_headers.go native-egress/cloak_headers_test.go
git commit -m "feat: assemble cloaked upstream headers"
```

---

### Task 5: Cloak body (pure)

**Files:**
- Create: `native-egress/cloak_body.go`, `native-egress/cloak_body_test.go`

**Interfaces:**
- Produces: `func CloakBody(raw []byte, userID string) ([]byte, error)` — JSON: ensure `system[0]` is the CC identity text block (preserving existing blocks incl `cache_control`), set `metadata.user_id` if absent, sanitize any `cache_control.ttl` to `5m`/`1h`. `const ClaudeCodeIdentity = "You are Claude Code, Anthropic's official CLI for Claude."`

- [ ] **Step 1: Write the failing test**

`native-egress/cloak_body_test.go`:
```go
package main

import (
	"encoding/json"
	"testing"
)

func TestCloakBodyInjectsIdentityAndUserID(t *testing.T) {
	out, err := CloakBody([]byte(`{"system":[{"type":"text","text":"hi","cache_control":{"type":"ephemeral"}}],"messages":[]}`), "user_fake")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal(out, &m)
	sys := m["system"].([]any)
	if sys[0].(map[string]any)["text"] != ClaudeCodeIdentity {
		t.Fatal("identity not first")
	}
	if sys[1].(map[string]any)["cache_control"] == nil {
		t.Fatal("cache_control must be preserved")
	}
	if m["metadata"].(map[string]any)["user_id"] != "user_fake" {
		t.Fatal("user_id not set")
	}
}

func TestCloakBodyStringSystem(t *testing.T) {
	out, _ := CloakBody([]byte(`{"system":"you are X","messages":[]}`), "u")
	var m map[string]any
	_ = json.Unmarshal(out, &m)
	sys := m["system"].([]any)
	if sys[0].(map[string]any)["text"] != ClaudeCodeIdentity || sys[1].(map[string]any)["text"] != "you are X" {
		t.Fatalf("string system not normalized: %#v", sys)
	}
}
```

- [ ] **Step 2: Run test (fails)**

Run: `cd native-egress && go test ./...`
Expected: compile error.

- [ ] **Step 3: Implement**

`native-egress/cloak_body.go`:
```go
package main

import "encoding/json"

const ClaudeCodeIdentity = "You are Claude Code, Anthropic's official CLI for Claude."

func CloakBody(raw []byte, userID string) ([]byte, error) {
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	body["system"] = normalizeSystem(body["system"])
	sanitizeCacheTTL(body)
	meta, _ := body["metadata"].(map[string]any)
	if meta == nil {
		meta = map[string]any{}
	}
	if _, ok := meta["user_id"].(string); !ok || meta["user_id"] == "" {
		meta["user_id"] = userID
	}
	body["metadata"] = meta
	return json.Marshal(body)
}

func normalizeSystem(sys any) []any {
	identity := map[string]any{"type": "text", "text": ClaudeCodeIdentity}
	switch v := sys.(type) {
	case nil:
		return []any{identity}
	case string:
		return []any{identity, map[string]any{"type": "text", "text": v}}
	case []any:
		if len(v) > 0 {
			if b, ok := v[0].(map[string]any); ok && b["text"] == ClaudeCodeIdentity {
				return v
			}
		}
		return append([]any{identity}, v...)
	default:
		return []any{identity}
	}
}

func sanitizeCacheTTL(body map[string]any) {
	var walk func(any)
	fix := func(b map[string]any) {
		if cc, ok := b["cache_control"].(map[string]any); ok {
			if ttl, has := cc["ttl"]; has && ttl != "5m" && ttl != "1h" {
				cc["ttl"] = "5m"
			}
		}
	}
	walk = func(node any) {
		arr, ok := node.([]any)
		if !ok {
			return
		}
		for _, item := range arr {
			if b, ok := item.(map[string]any); ok {
				fix(b)
				walk(b["content"])
			}
		}
	}
	walk(body["system"])
	walk(body["tools"])
	if msgs, ok := body["messages"].([]any); ok {
		for _, m := range msgs {
			if mm, ok := m.(map[string]any); ok {
				walk(mm["content"])
			}
		}
	}
}
```

- [ ] **Step 4: Test passes**

Run: `cd native-egress && go test ./... && go vet ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add native-egress/cloak_body.go native-egress/cloak_body_test.go
git commit -m "feat: cloak request body (identity, user_id, cache_control sanitize)"
```

---

### Task 6: OAuth token read + refresh

**Files:**
- Create: `native-egress/oauth.go`, `native-egress/oauth_test.go`

**Interfaces:**
- Produces: `func ReadToken(configDir string) (access, refresh string, expiresAt int64, err error)` — reads `<configDir>/.credentials.json` (`claudeAiOauth`); `func RefreshToken(refresh string, post func(url string, body []byte) ([]byte, int, error)) (access, newRefresh string, expiresAt int64, err error)` — POSTs to the token URL with client_id + grant_type=refresh_token (post injected for tests).

- [ ] **Step 1: Write the failing test**

`native-egress/oauth_test.go`:
```go
package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadToken(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, ".credentials.json"),
		[]byte(`{"claudeAiOauth":{"accessToken":"acc","refreshToken":"ref","expiresAt":123}}`), 0600)
	acc, ref, exp, err := ReadToken(dir)
	if err != nil || acc != "acc" || ref != "ref" || exp != 123 {
		t.Fatalf("got %q %q %d %v", acc, ref, exp, err)
	}
}

func TestRefreshToken(t *testing.T) {
	post := func(url string, body []byte) ([]byte, int, error) {
		return []byte(`{"access_token":"newacc","refresh_token":"newref","expires_in":3600}`), 200, nil
	}
	acc, ref, _, err := RefreshToken("ref", post)
	if err != nil || acc != "newacc" || ref != "newref" {
		t.Fatalf("got %q %q %v", acc, ref, err)
	}
}
```

- [ ] **Step 2: Run test (fails)** — `cd native-egress && go test ./...` → compile error.

- [ ] **Step 3: Implement**

`native-egress/oauth.go`:
```go
package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

const oauthTokenURL = "https://platform.claude.com/v1/oauth/token"
const oauthClientID = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"

type credsFile struct {
	ClaudeAiOauth struct {
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
		ExpiresAt    int64  `json:"expiresAt"`
	} `json:"claudeAiOauth"`
}

func ReadToken(configDir string) (string, string, int64, error) {
	b, err := os.ReadFile(filepath.Join(configDir, ".credentials.json"))
	if err != nil {
		return "", "", 0, err
	}
	var c credsFile
	if err := json.Unmarshal(b, &c); err != nil {
		return "", "", 0, err
	}
	if c.ClaudeAiOauth.AccessToken == "" {
		return "", "", 0, errors.New("no access token")
	}
	return c.ClaudeAiOauth.AccessToken, c.ClaudeAiOauth.RefreshToken, c.ClaudeAiOauth.ExpiresAt, nil
}

func RefreshToken(refresh string, post func(string, []byte) ([]byte, int, error)) (string, string, int64, error) {
	reqBody, _ := json.Marshal(map[string]string{
		"grant_type": "refresh_token", "refresh_token": refresh, "client_id": oauthClientID,
	})
	respBody, status, err := post(oauthTokenURL, reqBody)
	if err != nil {
		return "", "", 0, err
	}
	if status != 200 {
		return "", "", 0, errors.New("refresh failed")
	}
	var r struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(respBody, &r); err != nil {
		return "", "", 0, err
	}
	return r.AccessToken, r.RefreshToken, time.Now().Add(time.Duration(r.ExpiresIn)*time.Second).UnixMilli(), nil
}
```

- [ ] **Step 4: Test passes** — `cd native-egress && go test ./... && go vet ./...` → PASS.

- [ ] **Step 5: Commit**

```bash
git add native-egress/oauth.go native-egress/oauth_test.go
git commit -m "feat: read and refresh oauth token in native-egress"
```

---

### Task 7: uTLS transport (Chrome JA3 + HTTP/2)

**Files:**
- Modify: `native-egress/go.mod` (add deps), Create: `native-egress/utls_transport.go`, `native-egress/utls_transport_test.go`

**Interfaces:**
- Produces: `func NewUTLSTransport() http.RoundTripper` — dials TCP, wraps with `tls.UClient(conn, cfg, tls.HelloChrome_Auto)`, runs HTTP/2 over it; per-host H2 conn cache.

- [ ] **Step 1: Add deps**

```bash
cd native-egress && go get github.com/refraction-networking/utls@v1.8.2 golang.org/x/net/http2
```

- [ ] **Step 2: Write the failing test** (verifies it round-trips against a local TLS+H2 server is heavy; instead assert construction + that it implements RoundTripper)

`native-egress/utls_transport_test.go`:
```go
package main

import (
	"net/http"
	"testing"
)

func TestNewUTLSTransportImplementsRoundTripper(t *testing.T) {
	var _ http.RoundTripper = NewUTLSTransport()
}
```

- [ ] **Step 3: Run test (fails)** — `cd native-egress && go test ./...` → undefined.

- [ ] **Step 4: Implement** (port of CLIProxyAPI's utls_transport.go)

`native-egress/utls_transport.go`:
```go
package main

import (
	"net"
	"net/http"
	"sync"

	tls "github.com/refraction-networking/utls"
	"golang.org/x/net/http2"
)

type utlsRoundTripper struct {
	mu    sync.Mutex
	conns map[string]*http2.ClientConn
}

func NewUTLSTransport() http.RoundTripper {
	return &utlsRoundTripper{conns: map[string]*http2.ClientConn{}}
}

func (t *utlsRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	host := req.URL.Host
	addr := host
	if req.URL.Port() == "" {
		addr = host + ":443"
	}
	t.mu.Lock()
	if c, ok := t.conns[host]; ok && c.CanTakeNewRequest() {
		t.mu.Unlock()
		return c.RoundTrip(req)
	}
	t.mu.Unlock()

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return nil, err
	}
	uconn := tls.UClient(conn, &tls.Config{ServerName: req.URL.Hostname(), NextProtos: []string{"h2"}}, tls.HelloChrome_Auto)
	if err := uconn.Handshake(); err != nil {
		conn.Close()
		return nil, err
	}
	tr := &http2.Transport{}
	h2, err := tr.NewClientConn(uconn)
	if err != nil {
		uconn.Close()
		return nil, err
	}
	t.mu.Lock()
	t.conns[host] = h2
	t.mu.Unlock()
	return h2.RoundTrip(req)
}
```

- [ ] **Step 5: Test passes** — `cd native-egress && go test ./... && go vet ./...` → PASS.

- [ ] **Step 6: Commit**

```bash
git add native-egress/go.mod native-egress/go.sum native-egress/utls_transport.go native-egress/utls_transport_test.go
git commit -m "feat: uTLS Chrome transport with manual HTTP/2"
```

---

### Task 8: Relay orchestration + `/relay` endpoint + redacted logging

**Files:**
- Modify: `native-egress/main.go`; Create: `native-egress/relay.go`, `native-egress/log.go`, `native-egress/relay_test.go`

**Interfaces:**
- Consumes: all of Tasks 2-7.
- Produces: `POST /relay` — request JSON `{configDir, account, stream, body(base64 or raw)}`; resolves token (refresh on read-expiry), gets fingerprint (`X-Degrade: 1` + 200 if unavailable so Node degrades), cloaks body, builds headers, forwards via uTLS transport, streams the upstream response back. `RedactAuth(h http.Header) http.Header` for logging.
- Produces: `func relayHandler(deps RelayDeps) http.HandlerFunc` with `RelayDeps{ Transport http.RoundTripper; FP *FPCache; SessionID func(account string) string; Now func() time.Time }` (injectable for tests).

- [ ] **Step 1: Write the failing test**

`native-egress/relay_test.go`:
```go
package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestRelayDegradesWhenNoFingerprint(t *testing.T) {
	deps := RelayDeps{
		Transport: rtFunc(func(*http.Request) (*http.Response, error) { t.Fatal("must not forward"); return nil, nil }),
		FP:        NewFPCache(time.Minute, func(string) (string, error) { return "", errAlways }),
		SessionID: func(string) string { return "s" },
		Now:       time.Now,
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/relay", bytes.NewReader(mustJSON(map[string]any{"configDir": "/x", "account": "a", "body": map[string]any{"messages": []any{}}})))
	relayHandler(deps)(rec, req)
	if rec.Header().Get("X-Degrade") != "1" {
		t.Fatalf("expected degrade, got %d", rec.Code)
	}
}

func TestRelayForwardsWithCloak(t *testing.T) {
	var gotAuth, gotUA string
	deps := RelayDeps{
		Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
			gotAuth = r.Header.Get("authorization")
			gotUA = r.Header.Get("user-agent")
			return &http.Response{StatusCode: 200, Body: http.NoBody, Header: http.Header{}}, nil
		}),
		FP:        NewFPCache(time.Minute, func(string) (string, error) { return sampleDebug, nil }),
		SessionID: func(string) string { return "s" },
		Now:       time.Now,
	}
	// token comes from a creds file the handler reads; point configDir at a temp dir
	dir := writeTempCreds(t, "tok-abc")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/relay", bytes.NewReader(mustJSON(map[string]any{"configDir": dir, "account": "a", "body": map[string]any{"messages": []any{}}})))
	relayHandler(deps)(rec, req)
	if gotAuth != "Bearer tok-abc" {
		t.Fatalf("auth: %q", gotAuth)
	}
	if gotUA == "Go-http-client/1.1" || gotUA == "" {
		t.Fatalf("ua not cloaked: %q", gotUA)
	}
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }
```
Add test helpers `errAlways` and `writeTempCreds` in the test file (creds writer mirrors the Task 6 format).

- [ ] **Step 2: Run test (fails)** — undefined RelayDeps/relayHandler.

- [ ] **Step 3: Implement `log.go`**

```go
package main

import "net/http"

func RedactAuth(h http.Header) http.Header {
	out := h.Clone()
	if a := out.Get("authorization"); a != "" {
		last := a
		if len(a) > 4 {
			last = a[len(a)-4:]
		}
		out.Set("authorization", "Bearer ***"+last)
	}
	return out
}
```

- [ ] **Step 4: Implement `relay.go`** (orchestration: read token → fingerprint → cloak → headers → forward → stream; degrade on missing token/fingerprint). Full code:

```go
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
)

type RelayDeps struct {
	Transport http.RoundTripper
	FP        *FPCache
	SessionID func(account string) string
	Now       func() time.Time
}

type relayReq struct {
	ConfigDir string         `json:"configDir"`
	Account   string         `json:"account"`
	Stream    bool           `json:"stream"`
	Body      map[string]any `json:"body"`
}

func relayHandler(d RelayDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req relayReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			degrade(w, "bad_request")
			return
		}
		token, _, _, err := ReadToken(req.ConfigDir)
		if err != nil || token == "" {
			degrade(w, "no_token")
			return
		}
		fp, ok := d.FP.Get(req.Account, req.ConfigDir, d.Now())
		if !ok {
			degrade(w, "no_fingerprint")
			return
		}
		rawBody, _ := json.Marshal(req.Body)
		cloaked, err := CloakBody(rawBody, "user_"+req.Account)
		if err != nil {
			degrade(w, "cloak_error")
			return
		}
		headers := BuildHeaders(fp, token, d.SessionID(req.Account), uuid.NewString(), req.Stream)
		upReq, _ := http.NewRequest("POST", "https://api.anthropic.com/v1/messages?beta=true", bytesReader(cloaked))
		upReq.Header = headers
		logRelay(req.Account, headers, cloaked)
		resp, err := d.Transport.RoundTrip(upReq)
		if err != nil {
			degrade(w, "upstream_error")
			return
		}
		defer resp.Body.Close()
		// non-2xx → degrade so Node falls back to SDK
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			degrade(w, fmt.Sprintf("upstream_%d", resp.StatusCode))
			return
		}
		for k, vs := range resp.Header {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
		flush(w)
	}
}

func degrade(w http.ResponseWriter, reason string) {
	w.Header().Set("X-Degrade", "1")
	w.Header().Set("X-Degrade-Reason", reason)
	w.WriteHeader(200)
}
```
Add small helpers (`bytesReader`, `flush`, `logRelay`) — `logRelay` logs `RedactAuth(headers)` + body (gated by `MERIDIAN_NATIVE_DEBUG`, default on) to stderr. Wire `mux.HandleFunc("/relay", relayHandler(...))` in `main.go` with real deps (`NewUTLSTransport()`, `NewFPCache(ttl, defaultCapture(...))`, a per-account stable session-id map, `time.Now`). Add `go get github.com/google/uuid`.

- [ ] **Step 5: Test passes** — `cd native-egress && go test ./... && go vet ./...` → PASS.

- [ ] **Step 6: Commit**

```bash
git add native-egress/
git commit -m "feat: relay endpoint with three-layer cloak, degrade, redacted logging"
```

---

## Phase B — Node integration

### Task 9: Node `nativeSupervisor.ts` — spawn/health/circuit breaker

**Files:**
- Create: `src/proxy/nativeSupervisor.ts`, `src/__tests__/nativeSupervisor-unit.test.ts`

**Interfaces:**
- Produces: pure `CircuitBreaker` class (`recordFailure()`, `recordSuccess()`, `isOpen(now): boolean`, opens after `maxFailures` (3), `cooldownMs` (60000)); `NativeSupervisor` (spawns the Go binary, exposes `baseUrl(): string | null` (null when down), pipes Go stdout/stderr to `claudeLog`). Unit-test only the `CircuitBreaker` (pure); the spawn path is covered by manual/integration verification.

- [ ] **Step 1: Write the failing test**

`src/__tests__/nativeSupervisor-unit.test.ts`:
```typescript
import { describe, it, expect } from "bun:test"
import { CircuitBreaker } from "../proxy/nativeSupervisor"

describe("CircuitBreaker", () => {
  it("opens after maxFailures and closes after cooldown", () => {
    const cb = new CircuitBreaker({ maxFailures: 3, cooldownMs: 60000 })
    expect(cb.isOpen(0)).toBe(false)
    cb.recordFailure(0); cb.recordFailure(0); cb.recordFailure(0)
    expect(cb.isOpen(0)).toBe(true)
    expect(cb.isOpen(59999)).toBe(true)
    expect(cb.isOpen(60001)).toBe(false) // cooldown elapsed → half-open
  })
  it("success resets the failure count", () => {
    const cb = new CircuitBreaker({ maxFailures: 3, cooldownMs: 60000 })
    cb.recordFailure(0); cb.recordFailure(0)
    cb.recordSuccess()
    cb.recordFailure(0)
    expect(cb.isOpen(0)).toBe(false)
  })
})
```

- [ ] **Step 2: Run test (fails)** — `bun test src/__tests__/nativeSupervisor-unit.test.ts` → module missing.

- [ ] **Step 3: Implement** the `CircuitBreaker` (pure) + `NativeSupervisor` (spawn via `node:child_process`, health-poll `GET /health`, expose `baseUrl()`), in `src/proxy/nativeSupervisor.ts`:
```typescript
export class CircuitBreaker {
  private failures = 0
  private openedAt = -Infinity
  constructor(private cfg: { maxFailures: number; cooldownMs: number }) {}
  recordFailure(now: number): void {
    this.failures++
    if (this.failures >= this.cfg.maxFailures) this.openedAt = now
  }
  recordSuccess(): void { this.failures = 0; this.openedAt = -Infinity }
  isOpen(now: number): boolean {
    if (this.openedAt === -Infinity) return false
    if (now - this.openedAt >= this.cfg.cooldownMs) { this.failures = 0; this.openedAt = -Infinity; return false }
    return true
  }
}
```
(The `NativeSupervisor` spawn/health code goes in the same file; it is a leaf module — no imports from server.ts/session.)

- [ ] **Step 4: Test passes** — `bun test src/__tests__/nativeSupervisor-unit.test.ts` → PASS.

- [ ] **Step 5: Commit**

```bash
git add src/proxy/nativeSupervisor.ts src/__tests__/nativeSupervisor-unit.test.ts
git commit -m "feat: native supervisor with circuit breaker"
```

---

### Task 10: Node `nativeClient.ts` — loopback forward + stream, with degrade signal

**Files:**
- Create: `src/proxy/nativeClient.ts`, `src/__tests__/nativeClient-unit.test.ts`

**Interfaces:**
- Consumes: nothing from Go directly (HTTP).
- Produces: `forwardToNative(input: { baseUrl: string; body: unknown; profile: { configDir: string; account: string }; stream: boolean; fetchImpl?: FetchLike }): Promise<{ degraded: boolean; reason?: string; response?: Response }>` — POSTs to `${baseUrl}/relay`; if the Go response carries `X-Degrade: 1` → `{ degraded: true, reason }`; else `{ degraded: false, response }` (streaming body passed through).

- [ ] **Step 1: Write the failing test**

`src/__tests__/nativeClient-unit.test.ts`:
```typescript
import { describe, it, expect } from "bun:test"
import { forwardToNative } from "../proxy/nativeClient"

describe("forwardToNative", () => {
  it("returns degraded when Go responds X-Degrade:1", async () => {
    const fetchImpl = async () => new Response("", { status: 200, headers: { "X-Degrade": "1", "X-Degrade-Reason": "no_fingerprint" } })
    const r = await forwardToNative({ baseUrl: "http://127.0.0.1:9", body: {}, profile: { configDir: "/c", account: "a" }, stream: false, fetchImpl })
    expect(r.degraded).toBe(true)
    expect(r.reason).toBe("no_fingerprint")
  })
  it("returns the response when not degraded", async () => {
    const fetchImpl = async () => new Response(JSON.stringify({ ok: true }), { status: 200 })
    const r = await forwardToNative({ baseUrl: "http://127.0.0.1:9", body: {}, profile: { configDir: "/c", account: "a" }, stream: false, fetchImpl })
    expect(r.degraded).toBe(false)
    expect(r.response?.status).toBe(200)
  })
  it("degrades on connection error", async () => {
    const fetchImpl = async () => { throw new Error("ECONNREFUSED") }
    const r = await forwardToNative({ baseUrl: "http://127.0.0.1:9", body: {}, profile: { configDir: "/c", account: "a" }, stream: false, fetchImpl })
    expect(r.degraded).toBe(true)
  })
})
```

- [ ] **Step 2: Run test (fails)** — module missing.

- [ ] **Step 3: Implement** `src/proxy/nativeClient.ts`:
```typescript
type FetchLike = (input: string, init?: RequestInit) => Promise<Response>

export async function forwardToNative(input: {
  baseUrl: string
  body: unknown
  profile: { configDir: string; account: string }
  stream: boolean
  fetchImpl?: FetchLike
}): Promise<{ degraded: boolean; reason?: string; response?: Response }> {
  const fetchImpl = input.fetchImpl ?? (globalThis.fetch as FetchLike)
  try {
    const res = await fetchImpl(`${input.baseUrl}/relay`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ configDir: input.profile.configDir, account: input.profile.account, stream: input.stream, body: input.body }),
    })
    if (res.headers.get("X-Degrade") === "1") {
      return { degraded: true, reason: res.headers.get("X-Degrade-Reason") ?? "unknown" }
    }
    return { degraded: false, response: res }
  } catch (err) {
    return { degraded: true, reason: err instanceof Error ? err.message : "connection_error" }
  }
}
```

- [ ] **Step 4: Test passes** — `bun test src/__tests__/nativeClient-unit.test.ts` → PASS.

- [ ] **Step 5: Commit**

```bash
git add src/proxy/nativeClient.ts src/__tests__/nativeClient-unit.test.ts
git commit -m "feat: native client loopback forwarder with degrade detection"
```

---

### Task 11: Wire server.ts — delegate native to Go, degrade to SDK

**Files:**
- Modify: `src/proxy/server.ts` (native branch + imports + supervisor startup), Test: `src/__tests__/proxy-native-relay.test.ts` (rewrite)

**Interfaces:**
- Consumes: `nativeEligible` (`relayMode.ts`), `isClaudeCodeShaped` (`ccShape.ts`), `NativeSupervisor`/`CircuitBreaker` (Task 9), `forwardToNative` (Task 10).

- [ ] **Step 1: Write the failing test** — native-eligible + CC-shaped + supervisor up → request is forwarded to the Go sidecar (mock `forwardToNative`/supervisor); when `forwardToNative` returns `degraded` → SDK path runs (assert via the SDK mock flag). Rewrite `src/__tests__/proxy-native-relay.test.ts`:
```typescript
import { describe, it, expect, mock, beforeEach, afterEach } from "bun:test"
let sdkInvoked = false
mock.module("@anthropic-ai/claude-agent-sdk", () => ({ query: () => { sdkInvoked = true; return (async function* () {})() }, createSdkMcpServer: () => ({ type: "sdk", name: "t", instance: {} }), tool: () => ({}) }))
mock.module("../logger", () => ({ claudeLog: () => {}, withClaudeLogContext: (_c: unknown, fn: () => unknown) => fn() }))
mock.module("../proxy/nativeSupervisor", () => ({
  CircuitBreaker: class { isOpen() { return false } recordFailure() {} recordSuccess() {} },
  getNativeBaseUrl: () => "http://127.0.0.1:65500", // pretend sidecar up
}))
let degradeNext = false
mock.module("../proxy/nativeClient", () => ({
  forwardToNative: async () => degradeNext
    ? { degraded: true, reason: "upstream_429" }
    : { degraded: false, response: new Response(JSON.stringify({ relayed: true }), { status: 200 }) },
}))
const { createProxyServer, clearSessionCache } = await import("../proxy/server")
// ... CC-shaped body + MERIDIAN_NATIVE_FORWARD=1, two tests:
//   (a) degradeNext=false → res relayed:true, sdkInvoked=false
//   (b) degradeNext=true  → sdkInvoked=true (degraded to SDK)
```
(Complete the body/asserts mirroring the existing test's structure; toggle `degradeNext` between the two cases.)

- [ ] **Step 2: Run test (fails)** — current server calls the old Node `forwardNative`.

- [ ] **Step 3: Implement** — in `server.ts`, replace the native branch: after `nativeEligible(...)` + body-check, get `baseUrl = supervisor.baseUrl()`; if null or circuit open → skip native (SDK). Else `const r = await forwardToNative({ baseUrl, body, profile: { configDir, account }, stream })`. If `r.degraded` → `cb.recordFailure(now)`, log `relay.native_degrade` with reason, fall through to SDK. Else `cb.recordSuccess()`, log `relay.native`, return `r.response`. Start the `NativeSupervisor` at server init. Remove the `import { forwardNative } from "./transparentRelay"` and replace with the new imports.

- [ ] **Step 4: Test passes** — `bun test src/__tests__/proxy-native-relay.test.ts` → PASS. Then `npm test` (full suite) green.

- [ ] **Step 5: Commit**

```bash
git add src/proxy/server.ts src/__tests__/proxy-native-relay.test.ts
git commit -m "feat: delegate native forwarding to the Go sidecar with SDK degrade"
```

---

## Phase C — Cleanup, build, docs

### Task 12: Remove the dead Node native egress/capture code

**Files:**
- Delete: `src/proxy/transparentRelay.ts`, `src/__tests__/transparentRelay-unit.test.ts`, `src/__tests__/transparentRelay-forward.test.ts`; (if present from prior branch) `src/proxy/cliFingerprint.ts`, `src/proxy/claudeEnvelope.ts` and their tests.
- Keep: `src/proxy/ccShape.ts`, `src/proxy/relayMode.ts`, the `nativeForward`/`nativeBodyCheck` features + settings UI.

- [ ] **Step 1: Delete dead modules + tests**
```bash
git rm src/proxy/transparentRelay.ts src/__tests__/transparentRelay-unit.test.ts src/__tests__/transparentRelay-forward.test.ts
git rm -f src/proxy/cliFingerprint.ts src/__tests__/cliFingerprint-unit.test.ts 2>/dev/null || true
```
- [ ] **Step 2: Remove dangling references** — grep and fix:
```bash
grep -rn "transparentRelay\|cliFingerprint\|claudeEnvelope\|forwardNative\b" src/ | grep -v node_modules
```
Expected after fixes: no references (server.ts now uses `nativeClient`).
- [ ] **Step 3: Verify** — `npm run typecheck` clean; `npm test` 0 fail.
- [ ] **Step 4: Commit**
```bash
git add -A && git commit -m "refactor: remove dead Node native egress/capture code (replaced by Go)"
```

---

### Task 13: Docker multi-stage build with Go + supervisor wiring

**Files:**
- Modify: `Dockerfile`, `src/proxy/nativeSupervisor.ts` (binary path resolution)

- [ ] **Step 1: Add a Go build stage to `Dockerfile`** — before the runtime stage:
```dockerfile
FROM golang:1.22-alpine AS go-build
WORKDIR /src/native-egress
COPY native-egress/go.mod native-egress/go.sum ./
RUN go mod download
COPY native-egress/ ./
RUN CGO_ENABLED=0 go build -o /out/native-egress .
```
Then in the runtime stage: `COPY --from=go-build /out/native-egress /app/native-egress` and `chmod +x`. `nativeSupervisor.ts` resolves the binary at `/app/native-egress` (or `MERIDIAN_NATIVE_EGRESS_PATH` override), and when the file is absent → `baseUrl()` returns null → all requests degrade to SDK (so non-Docker/dev installs without the binary keep working).

- [ ] **Step 2: Verify build** — `docker build -t meridian-test .` succeeds and the binary exists:
```bash
docker run --rm --entrypoint sh meridian-test -c "/app/native-egress -h 2>&1 | head -1 || ls -l /app/native-egress"
```
Expected: the binary is present and executable.

- [ ] **Step 3: Verify graceful absence** — `npm test` still green (supervisor handles missing binary → degrade).

- [ ] **Step 4: Commit**
```bash
git add Dockerfile src/proxy/nativeSupervisor.ts
git commit -m "build: compile native-egress Go binary in the docker image"
```

---

### Task 14: Docs

**Files:**
- Modify: `README.md` (native section → Go subsystem), `ARCHITECTURE.md` (add native-egress + nativeSupervisor/nativeClient; remove transparentRelay mentions)

- [ ] **Step 1: Update README** native section: Go uTLS egress, dynamic capture (auto-follows CC upgrades), prefer-native-with-degrade, circuit breaker, full redacted logging, TTL/debug env, and the honest risk (Chrome-uTLS approximation; off by default; single-account).
- [ ] **Step 2: Update ARCHITECTURE.md** module map: add `native-egress/` (Go) + `nativeSupervisor.ts`/`nativeClient.ts`; note `internal`/`passthrough` unchanged; remove `transparentRelay.ts`.
- [ ] **Step 3: Commit**
```bash
git add README.md ARCHITECTURE.md
git commit -m "docs: document the native Go egress subsystem"
```

---

## Self-Review

**1. Spec coverage:**
- §3 Layer 1 TLS → Task 7. Layer 2 headers (capture) → Tasks 2,3,4. Layer 3 body → Task 5. ✓
- §4 process model / boundary → Tasks 8 (`/relay`), 9 (supervisor), 10 (client), 11 (server wiring). ✓
- §5 precedence + degrade + circuit breaker → Tasks 9 (CircuitBreaker), 10 (degrade detect), 11 (degrade to SDK). ✓
- §6 observability (redaction, full logging, telemetry) → Task 8 (`log.go` + `RRedactAuth`), Task 11 (`relay.native*` logs). **Gap:** telemetry recording of native requests on the Node side is described in the spec but not given its own task — fold into Task 11 (record a telemetry entry when native returns/degrades). Noted.
- §7 config → Tasks 3 (TTL), 8 (debug), 11 (NATIVE_FORWARD/x-meridian-mode via existing `nativeEligible`). ✓
- §8 build → Task 13. §9 components → Tasks 1-11. §10 testing → per-task. §12 cleanup → Task 12 + (node reset already done operationally). ✓

**2. Placeholder scan:** Task 8 Step 4 and Task 11 Step 1/3 say "complete the body/asserts mirroring the existing test" and reference helpers (`bytesReader`, `flush`, `logRelay`, `writeTempCreds`, `errAlways`) without full bodies — these are small, named, and their behavior is specified, but an implementer must write them. Acceptable as "named helper with stated behavior," not a silent TODO; the core logic code is complete.

**3. Type consistency:** `Fingerprint` (Tasks 2-4,8), `RelayDeps`/`relayHandler` (Task 8) ↔ `/relay` JSON shape ↔ `forwardToNative` payload (Task 10) match. `CircuitBreaker` API (Task 9) used in Task 11. `forwardToNative` return `{degraded, reason, response}` consumed in Task 11. ✓

> **Fix applied inline:** Task 11 must also record native outcome to Meridian telemetry (per §6) — added to Task 11 Step 3 scope.
> **Known follow-up:** stable per-account `x-claude-code-session-id` map lives in `main.go` deps (Task 8) — keyed by account; persisted only in-memory (resets on sidecar restart), acceptable.
