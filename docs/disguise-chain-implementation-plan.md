# Disguise-Chain Request-Fidelity Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make every outbound request from meridian-stable's native-egress byte-structurally identical to a real Claude Code 2.1.198 request, per `docs/disguise-chain-design.md`.

**Architecture:** Capture the *complete* real request (headers + body) with a dump server, then reconcile each user request against that capture — add missing, override wrong, delete extra, never self-inject. Headers replay the captured set; body is rebuilt from the captured template + model-derived rules; identity uses the real account UUID.

**Tech Stack:** Go 1.26, `github.com/refraction-networking/utls`, `github.com/tidwall/gjson`/`sjson`, `github.com/andybalholm/brotli`, `github.com/klauspost/compress/zstd` (both already in go.mod as indirect — promote to direct).

## Global Constraints

- **NEW REPO — do NOT modify meridian-stable.** meridian-stable is the untouched
  running baseline (161, no bans yet) and must stay as-is for later A/B comparison.
  The fixes below go into a **new, separate repo/project** started from a copy of
  meridian-stable's current code (name TBD, e.g. `meridian-mirror`). Deploy the new
  build to a **different** machine; keep 161 on meridian-stable. Compare the two.
- Work in the new repo's `native-egress` package (`cd native-egress`); paths below
  are relative to it (same layout as meridian-stable).
- **Governing principle:** the captured real CC request is the single source of truth. Output field set == capture. Add missing / override wrong / delete extra / **never self-inject**. Only per-request-varying values (user `messages`/`model`, `session_id`, per-request ids) change.
- **Verify against the real CLI, never assume — and CROSS-VALIDATE MULTIPLE TIMES.** Before modifying any simulated field/header/encoding/behavior, capture the real CLI **several times** (varied prompts, multiple invocations, and each relevant model) and only treat a value as ground truth once it is **stable across all samples** (as the 28-tool base was confirmed across 10 requests). A single capture is never enough. **Task 0 builds this cross-validated golden reference first; every later task matches its golden, not a lone observation.** Confirmed so far (to be re-confirmed with more samples in Task 0): real CLI request `accept-encoding: gzip, deflate, br, zstd`; Anthropic response `content-encoding: gzip`; base tools = 28 stable across prompts; real CC 2.1.198 does **not** send `x-client-request-id`. Task 7's golden byte-diff is the final gate.
- **EXCLUDE `cc_prev_req`** and any `BillingPatch`/`PrevReqStore` — confirmed ban-risk, never add.
- Reference implementation to port from: `origin/new-meridian` (`main`) in `/Users/leo/meridian轻量版/new-meridian` — captured in scratchpad as `main-warmup.go`, `main-body_template.go`, `main-sanitize_request.go`. Port logic, strip cc_prev_req.
- After each task: `go build ./...` must pass and `go test ./...` must pass (the pre-existing `TestRelayDegradesOnMissingToken` failure is inherited from a634d43 — leave it; do not let NEW failures appear).
- Commit format: `type: brief description`. No AI attribution lines in commit body (repo convention).

---

### Task 0: Build the cross-validated golden reference (do this FIRST, before any code)

Nothing gets modified until the real-CLI behavior it targets is captured **multiple times** and confirmed **stable**. This task produces the golden reference set in `docs/golden/` that Tasks 1–6 match and Task 7 diffs against. Run inside a deployed container (161 or the fresh machine) as the `claude` user.

**Files:**
- Create: `docs/golden/` (captured artifacts + notes)
- Create: `scripts/capture-golden.sh` (the multi-sample capture harness)

**What to capture and the confirmation bar (a value is ground truth only if identical across all samples):**

- [ ] **Step 1: Request headers — ≥8 samples, varied prompts.** Run `claude -p "<prompt>"` against a local dump server for prompts: `hi`, `list files`, `search web for X`, `write python`, `read+edit a file`, `explain this code`, `run tests`, `fix a bug`. For each, record the full header name set + values. **Confirm:** the header **name set** is identical every time; `user-agent`, `x-app`, all `x-stainless-*`, `anthropic-version`, `anthropic-dangerous-direct-browser-access` values are identical; `accept-encoding` == `gzip, deflate, br, zstd`; `x-client-request-id` is **absent** in all; only `x-claude-code-session-id` varies. Save `docs/golden/headers.json`.

- [ ] **Step 2: Response encoding — ≥3 real calls, stream and non-stream.** With `ANTHROPIC_LOG=debug`, make real `claude` calls and record the response `content-encoding` and `content-type`. **Confirm:** what Anthropic returns for a streaming (`text/event-stream`) response vs a JSON response, and whether `content-encoding: gzip` holds for both. Save `docs/golden/response-encoding.txt`. (So far JSON→gzip is confirmed; streaming must be confirmed here before Task 3 assumes it.)

- [ ] **Step 3: Body structure — ≥5 samples, same model (sonnet-4-6).** From the dump-server captures, record: top-level key order; `system` block count + each block's text prefix + exact `cache_control` (which blocks, ttl); `metadata.user_id` structure (device_id vs account_uuid vs session_id); `context_management`; `output_config`; `thinking`; `max_tokens`; the full base tool name list. **Confirm:** all identical across samples except `messages` and `session_id`; `device_id` is stable across samples (same machine); `account_uuid` equals the real account uuid; `session_id` == that request's `x-claude-code-session-id` header; base tools == the same set every time. Save `docs/golden/body-sonnet.json`.

- [ ] **Step 4: Session rotation — ≥3 separate invocations.** Run `claude -p hi` three times (three separate sessions). **Confirm:** `session_id` (and the matching header) DIFFERS across invocations while `device_id` and `account_uuid` stay the same. Record how the CLI derives it (per-session). Save `docs/golden/session-behavior.txt`.

- [ ] **Step 5: Per-model rules — capture each model.** Run `claude -p hi --model <m>` (or set the model) for a new-gen model (e.g. sonnet-5 / opus-4-8) and haiku. **Confirm** the model-derived values: `max_tokens` (expect 64000 new / 32000 haiku), `thinking` (adaptive+omitted new / enabled+budget haiku), `output_config` (effort:high new / absent haiku). Only code `modelMaxTokens/modelThinking/modelOutputConfig` (Task 6) to whatever these captures actually show. Save `docs/golden/body-per-model.json`.

- [ ] **Step 6: Commit the golden set**

```bash
git add docs/golden/ scripts/capture-golden.sh
git commit -m "test: cross-validated golden reference from multi-sample real-CLI captures"
```

**Gate:** if any value is NOT stable across samples, do not hardcode it — capture more, or make that field capture-driven (from the live template) rather than rule-driven. Only stable, cross-validated values become code.

---

### Task 1: Capture the complete real request via dump server

Replace the `NODE_OPTIONS`/`ANTHROPIC_LOG=debug` capture (which misses transport headers and fails body capture on CC 2.1.198) with a local dump server that receives the genuine `claude -p hi` request and records **all** headers + the full body.

**Files:**
- Modify: `native-egress/fingerprint.go` (the `excluded` map)
- Modify: `native-egress/warmup.go` (`warmupTemplate` → use `captureAll`)
- Test: `native-egress/warmup_capture_test.go` (create)

**Interfaces:**
- Produces: `captureAll(claudePath, configDir string) (Fingerprint, []byte)` — returns the captured header fingerprint and the raw request body.

- [ ] **Step 1: Stop excluding `accept-encoding` from capture**

In `fingerprint.go`, the `excluded` map currently drops `accept-encoding` so we never learn undici's value. Keep the truly per-request/transport headers excluded, but capture `accept-encoding` so it can be replayed.

```go
var excluded = map[string]bool{
	"authorization": true, "x-claude-code-session-id": true,
	"x-stainless-retry-count": true, "content-length": true,
	"host": true, "connection": true,
	// accept-encoding is NOT excluded: real CC (undici) sends
	// "gzip, deflate, br, zstd"; we capture and replay it verbatim.
}
```

- [ ] **Step 2: Write the failing test for `captureAll`**

Create `native-egress/warmup_capture_test.go`:

```go
package main

import (
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
```

Add the test helper `writeExec` at the bottom of the same file:

```go
func writeExec(path, content string) error {
	if err := osWriteFile(path, []byte(content), 0755); err != nil {
		return err
	}
	return nil
}
```

If `osWriteFile` is not already a package alias, use `os.WriteFile` directly and add `import "os"`.

- [ ] **Step 3: Run the test to verify it fails**

Run: `cd native-egress && go test -run TestCaptureAllGetsHeadersAndBody ./...`
Expected: FAIL (`captureAll` undefined).

- [ ] **Step 4: Add `captureAll` to `warmup.go`**

Port from `main-warmup.go`. Add imports `io`, `net`, `net/http`, `strings`, `sync`. Replace the body of `warmupTemplate` to call `captureAll` and store both fingerprint and body:

```go
// captureAll runs `claude -p hi` with ANTHROPIC_BASE_URL pointed at a local
// server so we capture BOTH the request headers (→ fingerprint) and the full
// body (→ template) from one genuine request. No real API call is made.
func captureAll(claudePath, configDir string) (Fingerprint, []byte) {
	var mu sync.Mutex
	var capturedBody []byte
	var capturedFP Fingerprint

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		if len(body) > len(capturedBody) {
			capturedBody = body
			fp := Fingerprint{}
			for k, vals := range r.Header {
				kl := strings.ToLower(k)
				if excluded[kl] || len(vals) == 0 {
					continue
				}
				fp[kl] = vals[0]
			}
			if ua := fp["user-agent"]; ua != "" && strings.HasPrefix(ua, "claude-cli/") {
				capturedFP = fp
			}
		}
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`{"id":"msg_warmup","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"model":"claude-sonnet-4-6","stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":10,"output_tokens":1}}`))
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		warmupLog("warmup: listen error: %v", err)
		return nil, nil
	}
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	defer srv.Close()

	addr := ln.Addr().String()
	cmd := exec.Command(claudePath, "-p", "hi")
	cmd.Env = append(append([]string{}, osEnviron()...),
		"ANTHROPIC_BASE_URL=http://"+addr,
		"CLAUDE_CONFIG_DIR="+resolveConfigDir(configDir),
	)
	cmd.CombinedOutput()

	mu.Lock()
	defer mu.Unlock()
	return capturedFP, capturedBody
}
```

- [ ] **Step 5: Rewrite `warmupTemplate` to use `captureAll`**

Replace the NODE_OPTIONS logic in `warmupTemplate` with:

```go
func warmupTemplate(claudePath, configDir string, fpCache *FPCache, btCache *BodyTemplateCache) bool {
	start := time.Now()
	fp, bodyData := captureAll(claudePath, configDir)
	if fp == nil {
		warmupLog("warmup: fingerprint capture failed (CC not logged in?)")
		return false
	}
	fpCache.mu.Lock()
	fpCache.entries["default"] = fpEntry{fp: fp, capturedAt: time.Now()}
	fpCache.mu.Unlock()

	fpVersion := ExtractVersionFromUA(fp["user-agent"])
	warmupLog("warmup: fingerprint learned (CC %s)", fpVersion)

	if len(bodyData) == 0 {
		warmupLog("warmup: body capture empty — using builtin body template")
		return true
	}
	btCache.LearnFromCC(bodyData, fpVersion, fp["anthropic-beta"], fp["x-stainless-runtime-version"])
	warmupLog("warmup: body template learned (%d bytes)", len(bodyData))
	return true
}
```

Remove the now-unused `warmupPreloadJS` const and `countTemplateTools` if it becomes unused (check with `go vet`). Keep `warmupLoop`, `warmupKick`, `TriggerWarmup` unchanged.

- [ ] **Step 6: Run tests + build**

Run: `cd native-egress && go build ./... && go test -run TestCaptureAllGetsHeadersAndBody ./...`
Expected: build OK, test PASS.

- [ ] **Step 7: Commit**

```bash
git add native-egress/fingerprint.go native-egress/warmup.go native-egress/warmup_capture_test.go
git commit -m "feat(native-egress): capture full real request (headers+body) via dump server; keep accept-encoding"
```

---

### Task 2: Replay captured headers verbatim; stop self-injecting

`BuildHeaders` must emit exactly the captured header set (plus the token and the per-request session id), with **no** `x-client-request-id` (real CC 2.1.198 does not send it) and `accept-encoding` coming from the capture.

**Files:**
- Modify: `native-egress/cloak_headers.go` (`BuildHeaders`)
- Modify: `native-egress/relay.go` (drop the `clientRequestID` argument at the call site)
- Test: `native-egress/cloak_headers_test.go` (add cases)

**Interfaces:**
- Produces: `BuildHeaders(fp Fingerprint, token, sessionID string, stream bool, clientBeta string) http.Header` (note: `clientRequestID` parameter removed).

- [ ] **Step 1: Write the failing test**

Add to `native-egress/cloak_headers_test.go`:

```go
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
```

- [ ] **Step 2: Run to verify failure**

Run: `cd native-egress && go test -run TestBuildHeadersNoClientRequestID ./...`
Expected: FAIL (compile error: too many args, or x-client-request-id present).

- [ ] **Step 3: Rewrite `BuildHeaders`**

```go
func BuildHeaders(fp Fingerprint, token, sessionID string, stream bool, clientBeta string) http.Header {
	h := http.Header{}
	for k, v := range fp { // replay the captured header set verbatim
		h.Set(k, v)
	}
	if beta := unionAnthropicBeta(h.Get("anthropic-beta"), clientBeta); beta != "" {
		h.Set("anthropic-beta", beta)
	}
	// Per-request values real CC itself sets/varies:
	h.Set("authorization", "Bearer "+token)
	h.Set("content-type", "application/json")
	h.Set("x-stainless-retry-count", "0")
	h.Set("x-claude-code-session-id", sessionID)
	h.Set("accept", "application/json")
	// NOTE: no x-client-request-id — real CC 2.1.198 does not send it.
	// accept-encoding is left as captured (undici's "gzip, deflate, br, zstd").
	return h
}

func BuildHeadersApiKey(fp Fingerprint, apiKey, sessionID string, stream bool, clientBeta string) http.Header {
	h := BuildHeaders(fp, "", sessionID, stream, clientBeta)
	h.Del("authorization")
	h.Set("x-api-key", apiKey)
	return h
}
```

(If `BuildHeadersApiKey` does not exist in this build, skip it.)

- [ ] **Step 4: Update the call site in `relay.go`**

Change line ~67 from `BuildHeaders(fp, token, d.SessionID(account), uuid.NewString(), true, clientBeta)` to:

```go
headers := BuildHeaders(fp, token, sessionID, true, clientBeta)
```

where `sessionID` is defined earlier (Task 5 threads it). For now, add `sessionID := d.SessionID(account)` above the call. Remove the now-unused `uuid` import if nothing else uses it (check `go build`).

- [ ] **Step 5: Run tests + build**

Run: `cd native-egress && go build ./... && go test -run 'TestBuildHeaders' ./...`
Expected: build OK, PASS.

- [ ] **Step 6: Commit**

```bash
git add native-egress/cloak_headers.go native-egress/relay.go native-egress/cloak_headers_test.go
git commit -m "fix(native-egress): replay captured headers; drop self-injected x-client-request-id; keep accept-encoding"
```

---

### Task 3: Decode compressed responses (we now advertise br/zstd)

**Verified against the real CLI (2026-07-04, `ANTHROPIC_LOG=debug`):** the real CLI
sends `accept-encoding: gzip, deflate, br, zstd`; Anthropic responds
`content-encoding: gzip` (with `vary: Accept-Encoding`); undici transparently
decompresses. So we must match: advertise the full list (Task 2) **and decompress
what comes back** — NOT force `identity` (that was the old `6737efc` workaround
that deviated from the CLI).

**Avoid the historical double-decode (`6737efc`):** the old bug was decoding in Go
and *also* forwarding the `content-encoding` header downstream so undici decoded
again. Rule: after decoding, **never forward `content-encoding`/`content-length`**.
The streaming path already strips them; the non-stream path builds its own response
headers (so it never forwards them) but must read through `decodedBody`.

Once we set `accept-encoding` explicitly, Go's transport no longer auto-decompresses, so we decode per `Content-Encoding` ourselves (gzip is what Anthropic actually sends; handle deflate/br/zstd too for safety).

**Files:**
- Create: `native-egress/decompress.go`
- Modify: `native-egress/relay.go` (wrap `resp.Body` before assembling/streaming)
- Modify: `native-egress/go.mod` (promote brotli + klauspost/compress to direct)
- Test: `native-egress/decompress_test.go`

**Interfaces:**
- Produces: `decodedBody(resp *http.Response) io.Reader` — returns a reader that transparently decodes the response per its `Content-Encoding` (identity/gzip/deflate/br/zstd).

- [ ] **Step 1: Write the failing test**

Create `native-egress/decompress_test.go`:

```go
package main

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"testing"
)

func TestDecodedBodyGzip(t *testing.T) {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	gw.Write([]byte("hello-sse"))
	gw.Close()
	resp := &http.Response{
		Header: http.Header{"Content-Encoding": []string{"gzip"}},
		Body:   io.NopCloser(bytes.NewReader(buf.Bytes())),
	}
	out, _ := io.ReadAll(decodedBody(resp))
	if string(out) != "hello-sse" {
		t.Fatalf("gzip not decoded: %q", out)
	}
}

func TestDecodedBodyIdentity(t *testing.T) {
	resp := &http.Response{
		Header: http.Header{},
		Body:   io.NopCloser(bytes.NewReader([]byte("plain"))),
	}
	out, _ := io.ReadAll(decodedBody(resp))
	if string(out) != "plain" {
		t.Fatalf("identity mangled: %q", out)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd native-egress && go test -run TestDecodedBody ./...`
Expected: FAIL (`decodedBody` undefined).

- [ ] **Step 3: Implement `decompress.go`**

```go
package main

import (
	"compress/flate"
	"compress/gzip"
	"io"
	"net/http"
	"strings"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

// decodedBody returns a reader that transparently decodes resp.Body according
// to its Content-Encoding. Unknown/identity encodings pass through unchanged.
func decodedBody(resp *http.Response) io.Reader {
	enc := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Encoding")))
	switch enc {
	case "", "identity":
		return resp.Body
	case "gzip":
		if r, err := gzip.NewReader(resp.Body); err == nil {
			return r
		}
	case "deflate":
		return flate.NewReader(resp.Body)
	case "br":
		return brotli.NewReader(resp.Body)
	case "zstd":
		if r, err := zstd.NewReader(resp.Body); err == nil {
			return r.IOReadCloser()
		}
	}
	return resp.Body
}
```

- [ ] **Step 4: Promote deps to direct and tidy**

Run: `cd native-egress && go get github.com/andybalholm/brotli@v1.0.6 github.com/klauspost/compress@v1.17.4 && go mod tidy`

- [ ] **Step 5: Use `decodedBody` in `relay.go`**

In the non-stream path, replace `assembleSSEToMessage(resp.Body)` with `assembleSSEToMessage(decodedBody(resp))`. In the streaming loop, replace `resp.Body.Read(buf)` with a reader obtained once before the loop: `respReader := decodedBody(resp)` then `respReader.Read(buf)`. Keep stripping the `content-encoding` response header we forward to the client (already done).

- [ ] **Step 6: Run tests + build**

Run: `cd native-egress && go build ./... && go test -run TestDecodedBody ./...`
Expected: build OK, PASS.

- [ ] **Step 7: Commit**

```bash
git add native-egress/decompress.go native-egress/decompress_test.go native-egress/relay.go native-egress/go.mod native-egress/go.sum
git commit -m "feat(native-egress): decode gzip/deflate/br/zstd responses (accept-encoding now advertised)"
```

---

### Task 4: Real account identity in metadata

`metadata.user_id` must carry the **real** account UUID (from `.claude.json`), a `device_id` that differs from it, and a `session_id` equal to the header's `x-claude-code-session-id`.

**Files:**
- Create: `native-egress/account_uuid.go`
- Modify: `native-egress/cloak_body.go` (`deriveUserID`)
- Test: `native-egress/account_uuid_test.go`

**Interfaces:**
- Produces: `readAccountUUID(configDir string) string` — the `oauthAccount.accountUuid` from `.claude.json`, or "".
- Produces: `deriveUserID(account, configDir, sessionID string) string` — JSON `{"device_id","account_uuid","session_id"}` with the real account uuid and the given session id.

- [ ] **Step 1: Write the failing test**

Create `native-egress/account_uuid_test.go`:

```go
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeriveUserIDUsesRealAccountUUID(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".claude.json"),
		[]byte(`{"oauthAccount":{"accountUuid":"6d13f8ba-ac25-4ef1-8b62-dec3a9834661"}}`), 0600)

	uid := deriveUserID("default", dir, "sess-xyz")
	var m map[string]string
	if err := json.Unmarshal([]byte(uid), &m); err != nil {
		t.Fatal(err)
	}
	if m["account_uuid"] != "6d13f8ba-ac25-4ef1-8b62-dec3a9834661" {
		t.Fatalf("account_uuid not real: %q", m["account_uuid"])
	}
	if m["session_id"] != "sess-xyz" {
		t.Fatalf("session_id not threaded: %q", m["session_id"])
	}
	if m["device_id"] == m["account_uuid"] || strings.HasPrefix(m["device_id"], strings.ReplaceAll(m["account_uuid"], "-", "")[:8]) {
		t.Fatal("device_id must differ from account_uuid")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd native-egress && go test -run TestDeriveUserIDUsesRealAccountUUID ./...`
Expected: FAIL (signature mismatch / fake uuid).

- [ ] **Step 3: Implement `account_uuid.go`**

```go
package main

import (
	"os"
	"path/filepath"

	"github.com/tidwall/gjson"
)

// readAccountUUID returns the real Anthropic account UUID from
// .claude.json's oauthAccount.accountUuid, or "" if absent.
func readAccountUUID(configDir string) string {
	b, err := os.ReadFile(filepath.Join(resolveConfigDir(configDir), ".claude.json"))
	if err != nil {
		return ""
	}
	return gjson.GetBytes(b, "oauthAccount.accountUuid").String()
}
```

- [ ] **Step 4: Rewrite `deriveUserID` in `cloak_body.go`**

```go
// deriveUserID builds metadata.user_id exactly like real CC:
// {"device_id":<stable per-machine sha256>,"account_uuid":<real uuid>,"session_id":<session>}.
// device_id is machine-stable and independent of account_uuid; session_id is the
// SAME value used in the x-claude-code-session-id header.
func deriveUserID(account, configDir, sessionID string) string {
	accountUUID := readAccountUUID(configDir)
	if accountUUID == "" {
		// Fallback only if the real uuid is not yet populated: derive from account,
		// but keep it distinct from device_id.
		h := sha256.Sum256([]byte("meridian-acct:" + account))
		accountUUID = fmt.Sprintf("%x-%x-%x-%x-%x", h[0:4], h[4:6], h[6:8], h[8:10], h[10:16])
	}
	dh := sha256.Sum256([]byte("meridian-device:" + machineSeed()))
	deviceID := fmt.Sprintf("%x", dh)
	return `{"device_id":"` + deviceID + `","account_uuid":"` + accountUUID + `","session_id":"` + sessionID + `"}`
}

// machineSeed returns a stable per-container seed for device_id (hostname is
// stable within a container's lifetime; real CC's device_id is likewise stable).
func machineSeed() string {
	if h, err := os.Hostname(); err == nil && h != "" {
		return h
	}
	return "meridian-node"
}
```

Add imports `os` (if missing) to `cloak_body.go`. Keep `crypto/sha256`, `fmt`.

- [ ] **Step 5: Run tests + build**

Run: `cd native-egress && go build ./... && go test -run TestDeriveUserIDUsesRealAccountUUID ./...`
Expected: build fails at the OLD `deriveUserID(account)` call site in relay.go — that is fixed in Task 5. Temporarily update the call to `deriveUserID(account, configDir, d.SessionID(account))` to build, then Task 5 finalizes threading. After that edit: build OK, PASS.

- [ ] **Step 6: Commit**

```bash
git add native-egress/account_uuid.go native-egress/account_uuid_test.go native-egress/cloak_body.go native-egress/relay.go
git commit -m "fix(native-egress): metadata uses real account_uuid, device_id != account_uuid, threaded session_id"
```

---

### Task 5: One session id, shared header↔metadata, per-conversation

The header `x-claude-code-session-id` and `metadata.session_id` must be the **same** value, derived per conversation (so it rotates like real CC) rather than one constant per account.

**Files:**
- Modify: `native-egress/relay.go` (compute one session id from the request)
- Modify: `native-egress/main.go` (session id derivation from a per-conversation key)
- Test: `native-egress/relay_session_test.go`

**Interfaces:**
- Consumes: the incoming request may carry a conversation key in header `X-Native-Session-Key` (forwarded by the Node layer); if absent, fall back to per-account stable.
- Produces: `conversationSessionID(account, convKey string) string` — a stable uuid per (account, convKey).

- [ ] **Step 1: Write the failing test**

Create `native-egress/relay_session_test.go`:

```go
package main

import "testing"

func TestConversationSessionIDStableAndPerConversation(t *testing.T) {
	a := conversationSessionID("acct", "conv-1")
	b := conversationSessionID("acct", "conv-1")
	c := conversationSessionID("acct", "conv-2")
	if a != b {
		t.Fatal("same conversation must yield same session id")
	}
	if a == c {
		t.Fatal("different conversations must yield different session ids")
	}
	if len(a) != 36 {
		t.Fatalf("not a uuid: %q", a)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd native-egress && go test -run TestConversationSessionIDStable ./...`
Expected: FAIL (undefined).

- [ ] **Step 3: Implement `conversationSessionID` in `main.go`**

```go
// conversationSessionID returns a stable RFC-4122-shaped id per (account,
// conversation). Real CC keeps one session id per conversation and rotates it
// across conversations; we mirror that instead of one constant per account.
func conversationSessionID(account, convKey string) string {
	if convKey == "" {
		convKey = "default"
	}
	h := sha256.Sum256([]byte("meridian-session:" + account + ":" + convKey))
	return fmt.Sprintf("%x-%x-%x-%x-%x", h[0:4], h[4:6], h[6:8], h[8:10], h[10:16])
}
```

Add imports `crypto/sha256`, `fmt` to `main.go`.

- [ ] **Step 4: Thread it in `relay.go`**

At the top of `relayHandler`, after reading `account`, read the conversation key and compute one session id used for both header and metadata:

```go
convKey := r.Header.Get("X-Native-Session-Key")
sessionID := conversationSessionID(account, convKey)
```

Use `sessionID` in both `BuildHeaders(fp, token, sessionID, true, clientBeta)` and `deriveUserID(account, configDir, sessionID)`. Remove the old `d.SessionID`-based header/metadata mismatch.

- [ ] **Step 5: Run tests + build**

Run: `cd native-egress && go build ./... && go test -run 'TestConversationSessionID|TestDeriveUserID' ./...`
Expected: build OK, PASS. Header and metadata now share `sessionID`.

- [ ] **Step 6: Commit**

```bash
git add native-egress/main.go native-egress/relay.go native-egress/relay_session_test.go
git commit -m "fix(native-egress): single per-conversation session id shared by header and metadata"
```

---

### Task 6: Normalizer — reconcile body against the captured template

Rebuild `MergeUserRequest` to follow the governing principle: template-fixed fields from the capture, model-derived fields forced, only `model`/`messages`/`tool_choice` from the user, base tools always present, non-CC user tools dropped, non-CC top-level fields stripped, and a **fixed key order** on output.

**Files:**
- Create: `native-egress/sanitize_request.go`
- Modify: `native-egress/body_template.go` (`MergeUserRequest`, add `marshalBody`, model helpers)
- Test: `native-egress/normalizer_test.go`

**Interfaces:**
- Consumes: `deriveUserID(account, configDir, sessionID)` (Task 4/5) via the caller.
- Produces: `MergeUserRequest(userBody []byte, tmpl *BodyTemplate, userID string) ([]byte, error)` (signature unchanged; no billing patch).
- Produces helpers: `modelMaxTokens`, `modelThinking`, `modelOutputConfig`, `isNewModel`, `isCCRecognizedTool`, `marshalBody`, `sanitizeToolChoice`, `stripEmptyImageBlocks`.

- [ ] **Step 1: Write the failing test**

Create `native-egress/normalizer_test.go`:

```go
package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func baseTmpl() *BodyTemplate {
	return &BodyTemplate{
		System: []any{
			map[string]any{"type": "text", "text": "x-anthropic-billing-header: cc_version=2.1.198.542; cc_entrypoint=sdk-cli;"},
			map[string]any{"type": "text", "text": "You are a Claude agent, built on Anthropic's Claude Agent SDK.", "cache_control": map[string]any{"type": "ephemeral", "ttl": "1h"}},
		},
		Tools:             []any{map[string]any{"name": "Bash"}, map[string]any{"name": "Read"}},
		ContextManagement: map[string]any{"edits": []any{map[string]any{"type": "clear_thinking_20251015", "keep": "all"}}},
		Stream:            true,
	}
}

func TestNormalizerStripsUnsupportedToolAndForcesModelFields(t *testing.T) {
	user := []byte(`{"model":"claude-sonnet-4-6","max_tokens":8000,"temperature":0.7,
		"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}],
		"tools":[{"name":"Read"},{"name":"CustomUnsupportedTool"},{"name":"mcp__foo__bar"}]}`)
	out, err := MergeUserRequest(user, baseTmpl(), `{"device_id":"d","account_uuid":"a","session_id":"s"}`)
	if err != nil {
		t.Fatal(err)
	}
	var b map[string]any
	json.Unmarshal(out, &b)

	if _, ok := b["temperature"]; ok {
		t.Fatal("temperature (non-CC field) must be stripped")
	}
	if int(b["max_tokens"].(float64)) != 64000 {
		t.Fatalf("max_tokens must be forced to 64000, got %v", b["max_tokens"])
	}
	names := map[string]bool{}
	for _, tl := range b["tools"].([]any) {
		names[tl.(map[string]any)["name"].(string)] = true
	}
	if names["CustomUnsupportedTool"] {
		t.Fatal("unsupported user tool must be dropped")
	}
	if !names["Bash"] || !names["Read"] {
		t.Fatal("base tools must be present")
	}
	if !names["mcp__foo__bar"] {
		t.Fatal("MCP tool (CC-recognized) must be kept")
	}
	th := b["thinking"].(map[string]any)
	if th["type"] != "adaptive" || th["display"] != "omitted" {
		t.Fatalf("thinking must be adaptive+omitted, got %v", th)
	}
}

func TestMarshalBodyKeyOrder(t *testing.T) {
	out, _ := MergeUserRequest(
		[]byte(`{"model":"claude-sonnet-4-6","messages":[]}`), baseTmpl(),
		`{"device_id":"d","account_uuid":"a","session_id":"s"}`)
	s := string(out)
	// real CC order: model, messages, system, tools, metadata, max_tokens, thinking, context_management, output_config, stream
	order := []string{`"model"`, `"messages"`, `"system"`, `"tools"`, `"metadata"`, `"max_tokens"`, `"thinking"`, `"context_management"`, `"output_config"`, `"stream"`}
	last := -1
	for _, k := range order {
		i := strings.Index(s, k)
		if i < 0 {
			continue
		}
		if i < last {
			t.Fatalf("key %s out of order", k)
		}
		last = i
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd native-egress && go test -run 'TestNormalizer|TestMarshalBody' ./...`
Expected: FAIL.

- [ ] **Step 3: Add model helpers + tool recognizer to `body_template.go`**

```go
func modelMaxTokens(model string) int {
	if isNewModel(model) {
		return 64000
	}
	return 32000
}
func isNewModel(model string) bool { return !strings.Contains(model, "haiku") }
func modelThinking(model string) map[string]any {
	if strings.Contains(model, "haiku") {
		return map[string]any{"type": "enabled", "budget_tokens": 31999, "display": "omitted"}
	}
	return map[string]any{"type": "adaptive", "display": "omitted"}
}
func modelOutputConfig(model string) map[string]any {
	if strings.Contains(model, "haiku") {
		return nil
	}
	return map[string]any{"effort": "high"}
}

// isCCRecognizedTool reports whether a user-supplied tool is one real CC would
// carry: an MCP tool (mcp__*) or a name already in the template's base set.
func isCCRecognizedTool(name string, baseNames map[string]bool) bool {
	return strings.HasPrefix(name, "mcp__") || baseNames[name]
}
```

- [ ] **Step 4: Add ordered `marshalBody` to `body_template.go`**

```go
// ccKeyOrder is real CC's top-level key order (verified from a live capture).
var ccKeyOrder = []string{
	"model", "messages", "system", "tools", "metadata",
	"max_tokens", "thinking", "context_management", "output_config", "stream",
}

// marshalBody serializes result with real CC's key order (json.Marshal on a map
// sorts alphabetically, which differs from real CC).
func marshalBody(result map[string]any) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	first := true
	emit := func(k string, v any) error {
		if !first {
			buf.WriteByte(',')
		}
		first = false
		kb, _ := json.Marshal(k)
		buf.Write(kb)
		buf.WriteByte(':')
		vb, err := json.Marshal(v)
		if err != nil {
			return err
		}
		buf.Write(vb)
		return nil
	}
	seen := map[string]bool{}
	for _, k := range ccKeyOrder {
		if v, ok := result[k]; ok {
			if err := emit(k, v); err != nil {
				return nil, err
			}
			seen[k] = true
		}
	}
	for k, v := range result { // any leftover keys (should be none) after the fixed order
		if !seen[k] {
			if err := emit(k, v); err != nil {
				return nil, err
			}
		}
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}
```

Add `"bytes"` to `body_template.go` imports.

- [ ] **Step 5: Rewrite `MergeUserRequest`**

```go
func MergeUserRequest(userBody []byte, tmpl *BodyTemplate, userID string) ([]byte, error) {
	var user map[string]any
	if err := json.Unmarshal(userBody, &user); err != nil {
		return nil, err
	}
	model, _ := user["model"].(string)
	result := make(map[string]any, 12)

	// ① template-fixed (from the capture)
	result["system"] = append([]any{}, tmpl.System...)
	result["metadata"] = map[string]any{"user_id": userID}
	if tmpl.ContextManagement != nil {
		result["context_management"] = tmpl.ContextManagement
	}

	// ④ tools: base set always; append only CC-recognized user tools (deduped)
	baseNames := map[string]bool{}
	merged := append([]any{}, tmpl.Tools...)
	for _, tl := range tmpl.Tools {
		if tm, ok := tl.(map[string]any); ok {
			if n, ok := tm["name"].(string); ok {
				baseNames[n] = true
			}
		}
	}
	if userTools, ok := user["tools"].([]any); ok {
		for _, tl := range userTools {
			tm, ok := tl.(map[string]any)
			if !ok {
				continue
			}
			n, _ := tm["name"].(string)
			if n == "" || baseNames[n] || !isCCRecognizedTool(n, baseNames) {
				continue // dedup base, drop non-CC
			}
			delete(tm, "cache_control")
			merged = append(merged, tl)
			baseNames[n] = true
		}
	}
	if len(merged) > 0 {
		result["tools"] = merged
	}

	// ② model-derived (forced)
	result["max_tokens"] = modelMaxTokens(model)
	result["thinking"] = modelThinking(model)
	if oc := modelOutputConfig(model); oc != nil {
		result["output_config"] = oc
	}

	// ③ user passthrough (only these)
	result["model"] = user["model"]
	result["messages"] = stripEmptyTextBlocks(user["messages"])
	stripEmptyImageBlocks(result["messages"])
	sanitizeToolChoice(user)
	if tc, ok := user["tool_choice"]; ok {
		result["tool_choice"] = tc
	}
	result["stream"] = true
	if s, ok := user["stream"].(bool); ok {
		result["stream"] = s
	}

	ensureCacheControl(result)
	return marshalBody(result)
}
```

Note: `tool_choice` is intentionally NOT in `ccKeyOrder`; `marshalBody`'s leftover loop appends it. If real CC places `tool_choice` in a specific slot, add it to `ccKeyOrder`; the captured golden (Task 7) will reveal the exact slot.

- [ ] **Step 6: Add `sanitize_request.go`**

Port from `main-sanitize_request.go` (both functions are self-contained, no cc_prev_req):

```go
package main

// sanitizeToolChoice normalizes a string tool_choice ("auto"/"any"/"none"/name)
// into the object form the API expects.
func sanitizeToolChoice(body map[string]any) {
	tc, ok := body["tool_choice"].(string)
	if !ok {
		return
	}
	switch tc {
	case "auto", "any", "none":
		body["tool_choice"] = map[string]any{"type": tc}
	default:
		body["tool_choice"] = map[string]any{"type": "tool", "name": tc}
	}
}

// stripEmptyImageBlocks removes image blocks with empty source data that the API
// rejects.
func stripEmptyImageBlocks(msgs any) {
	arr, _ := msgs.([]any)
	for _, m := range arr {
		mm, _ := m.(map[string]any)
		if mm == nil {
			continue
		}
		content, _ := mm["content"].([]any)
		if content == nil {
			continue
		}
		filtered := content[:0]
		for _, c := range content {
			block, _ := c.(map[string]any)
			if block != nil && block["type"] == "image" {
				src, _ := block["source"].(map[string]any)
				if src == nil || src["data"] == "" {
					continue
				}
			}
			filtered = append(filtered, c)
		}
		mm["content"] = filtered
	}
}
```

- [ ] **Step 7: Run tests + build**

Run: `cd native-egress && go build ./... && go test -run 'TestNormalizer|TestMarshalBody' ./...`
Expected: build OK, PASS.

- [ ] **Step 8: Commit**

```bash
git add native-egress/body_template.go native-egress/sanitize_request.go native-egress/normalizer_test.go
git commit -m "feat(native-egress): reconcile body against capture — base tools, model-forced fields, strip non-CC, fixed key order"
```

---

### Task 7: Golden byte-diff acceptance

Verify the whole chain against a freshly captured real CC request.

**Files:**
- Create: `docs/golden/README.md` (how to capture + diff)
- Create: `scripts/golden-diff.sh` (capture real CC + our disguised output, diff)

- [ ] **Step 1: Write the capture+diff script**

Create `scripts/golden-diff.sh` (runs inside a deployed container). It: (a) runs `claude -p hi` against a dump server to save the **real** request; (b) runs a crafted user request through an isolated second native-egress with `MERIDIAN_NATIVE_DEBUG=1` (port 9879) to save the **disguised** request; (c) prints a field-by-field diff of headers (names + values), top-level body key order, `system` blocks (billing version, cache placement), base-tool presence, and `metadata` (real uuid, device_id≠account_uuid, header session_id == metadata session_id). The exact script is the same capture technique used during design (dump server + `logRelay`), assembled into one file.

```sh
#!/bin/sh
# See docs/disguise-chain-design.md §7. Captures real CC + our disguised output
# and prints a structural diff. Run as the claude user inside the container.
set -e
# ... (capture real via dump server → /tmp/real.json;
#      capture ours via second native-egress on :9879 with MERIDIAN_NATIVE_DEBUG=1 → /tmp/ours.json;
#      node diff over: header name set, header values, body key order,
#      system billing cc_version, cache_control placement, base-28 tool presence,
#      metadata.account_uuid == real, device_id != account_uuid,
#      header x-claude-code-session-id == metadata session_id)
echo "PASS if zero structural diff; print each mismatch otherwise."
```

Fill in the capture bodies from the design doc's §7 technique (dump server + second-instance debug). Keep it runnable standalone.

- [ ] **Step 2: Document acceptance in `docs/golden/README.md`**

State the pass criterion: **zero structural diff** — identical header name set (no `x-client-request-id`), `accept-encoding` = `gzip, deflate, br, zstd`, body key order matches, `system[0]` billing `cc_version` equals the live user-agent version, base tools present, `metadata.account_uuid` is the real account uuid, `device_id != account_uuid`, and `x-claude-code-session-id == metadata.session_id`.

- [ ] **Step 3: Commit**

```bash
git add scripts/golden-diff.sh docs/golden/README.md
git commit -m "test: golden byte-diff acceptance for disguise chain"
```

---

### Task 8: Build, deploy to a fresh machine, verify

**Files:** none (deploy + verify).

- [ ] **Step 1: Build the image on the target machine**

Transfer the repo (`git archive HEAD | ssh <host> 'tar -x -C /opt/meridian-stable'`), then `docker build -t meridian:stable .` (add swap if RAM < 2 GB).

- [ ] **Step 2: Deploy**

Point the node's compose at `meridian:stable`, `docker compose up -d proxy`. Configure the egress proxy + import a **suitably aged** account (behavioral factors are out of scope, but a fresh subscription will bias the ban test).

- [ ] **Step 3: Run the golden diff**

`docker exec -u 1000:1000 -e HOME=/home/claude <proxy> sh /app/scripts/golden-diff.sh` (or copy the script in). Expected: **zero structural diff**. Fix any mismatch by returning to the owning task.

- [ ] **Step 4: Confirm live health + one real request**

`/health` healthy; send one real request through the proxy and confirm 200 + a normal streamed response.

---

## Self-Review

- **Spec coverage:** cross-validated ground truth → Task 0 (precedes all code); §5.1 capture → Task 1; §5.2 normalizer → Task 6; §5.3 identity → Tasks 4–5; §5.4 thinking → Task 6 (model-forced, values from Task 0 Step 5); §5.5 fallback/validation → Task 1 (builtin fallback retained) + existing `ValidateBody`; §5.6 headers → Tasks 2–3; §E header findings → Tasks 2–3; §7 acceptance → Tasks 7–8; excluded cc_prev_req → honored throughout (no BillingPatch/PrevReqStore introduced). Every task matches Task 0's golden, not lone observations. Covered.
- **Placeholder scan:** the only prose-only step is Task 7 Step 1's diff body, which references the concrete design-doc §7 technique and lists exact fields to diff; assemble from that. All code steps contain complete code.
- **Type consistency:** `deriveUserID(account, configDir, sessionID)` (Tasks 4–5), `BuildHeaders(fp, token, sessionID, stream, clientBeta)` (Task 2), `MergeUserRequest(userBody, tmpl, userID)` (Task 6), `conversationSessionID(account, convKey)` (Task 5), `decodedBody(resp)` (Task 3), `readAccountUUID(configDir)` (Task 4) — used consistently across tasks and call sites.
