# Native Go Egress Subsystem — Design

**Date:** 2026-06-21
**Status:** Draft — pending review
**Topic:** Rewrite Meridian's native (transparent) forwarding as a Go subsystem that replicates a genuine Claude Code request across all three layers — TLS fingerprint (uTLS), HTTP headers, and body — so that requests relayed through an upstream gateway (new-api) reach `api.anthropic.com` with a complete, authentic Claude Code fingerprint and are not rate-limited/flagged.

---

## 1. Problem & Evidence

Native forwarding from Node (`fetch`/undici) is rejected by Anthropic. Empirically, on the same account at the same instant:

| Path | Result |
|------|--------|
| Native (Node fetch, complete HTTP headers incl. `x-claude-code-session-id`) | **429** |
| SDK passthrough (real `claude` binary) | **200** |

The SDK 200 right after the native 429 rules out a global account limit. With the HTTP headers already complete and still 429'd, the remaining difference is **below HTTP**: the **TLS/HTTP2 transport fingerprint (JA3/JA4)**. Node's TLS stack produces a ClientHello unlike the real `claude` binary's, so Anthropic differentiates the native path.

**Reference:** `router-for-me/CLIProxyAPI` (Go) solves this with `github.com/refraction-networking/utls` — a custom RoundTripper using `tls.UClient(conn, cfg, tls.HelloChrome_Auto)` (Chrome JA3) + manual HTTP/2. **uTLS is Go-only; Node has no equivalent.** Hence: a Go egress subsystem.

**Our advantage over CLIProxyAPI:** CLIProxyAPI hard-pins header values to a specific CLI version (they go stale on CC upgrades). We **dynamically capture** the header fingerprint from the local CLI (`ANTHROPIC_LOG=debug claude -p hi` → parse the real outgoing `headers{}` block), so headers/body auto-follow CC version bumps.

---

## 2. Goals & Non-Goals

**Goals**
- A Go subsystem (`native-egress`) that owns the full native path: OAuth token read/refresh, fingerprint capture, three-layer disguise (TLS + headers + body), forward, stream back.
- Native is **additive and non-breaking**: the existing `internal` and SDK `passthrough` modes are untouched.
- **Prefer-native-with-degrade**: when native is enabled, try native; on *any* native failure, degrade to the existing SDK passthrough so the request still completes.
- Header/body fingerprint **dynamically captured** from the local CLI (auto-follows CC upgrades).
- Full **observability**: per native request, the complete outgoing headers (token redacted) + body + upstream result are visible.

**Non-Goals**
- **Sensitive-word / content obfuscation** — explicitly excluded. That evades Anthropic's content-safety moderation (a different category from looking like a legitimate client) and is out of scope.
- A custom per-version uTLS ClientHelloID matching `claude.exe` exactly — uTLS has no `claude-cli` preset and `claude.exe` is Bun/BoringSSL; we use `HelloChrome_Auto` as the proven approximation (see §3).
- Rewriting Meridian wholesale in Go. Only the native path moves to Go.

---

## 3. The Three Disguise Layers (Go subsystem)

**Layer 1 — TLS (uTLS).**
Custom `http.RoundTripper`: raw TCP dial → `tls.UClient(conn, &tls.Config{ServerName: host}, tls.HelloChrome_Auto)` → manual `http2.Transport.NewClientConn` over the uTLS conn, with per-host H2 connection caching. **Honest limitation:** this is a *fixed Chrome* fingerprint, NOT dynamically captured — uTLS has only preset ClientHelloIDs (Chrome/Firefox/Safari/iOS), no `claude-cli`. Chrome is the closest approximation to the CLI's Node/OpenSSL-family TLS and is what CLIProxyAPI uses in production. This layer is the reason native can pass at all; it remains a cat-and-mouse approximation.

**Layer 2 — Headers (dynamically captured — our advantage).**
- Capture: spawn `ANTHROPIC_LOG=debug claude -p hi` with the profile's `CLAUDE_CONFIG_DIR` (a real authenticated request); parse the complete `headers{}` block from the debug log (every header, keys quoted or bare). Yields the genuine `user-agent`, full `anthropic-beta`, `x-app`, all `x-stainless-*`, `accept`, `anthropic-version`, etc.
- Cache by `(account, claude-binary-version)`; TTL configurable (`MERIDIAN_NATIVE_FINGERPRINT_TTL`, default `5m`). CC upgrade → recapture → fingerprint follows automatically.
- Per-request additions the envelope omits: `authorization: Bearer <oauth>`, `x-claude-code-session-id` (**stable per account**, cached), `x-client-request-id` (fresh UUID per request), `x-stainless-retry-count: 0`, `Accept`/`Accept-Encoding` per stream vs non-stream, `Connection: keep-alive`.

**Layer 3 — Body.**
- Inject `You are Claude Code, Anthropic's official CLI for Claude.` as the first `system` text block (if not already present).
- Preserve `cache_control` verbatim (don't strip/flatten); sanitize malformed `cache_control.ttl` to `5m`/`1h` to avoid upstream 400.
- Generate/validate a `metadata.user_id` (fake but well-formed) if absent.
- **No sensitive-word obfuscation.**

URL: `POST https://api.anthropic.com/v1/messages?beta=true`.

---

## 4. Process Model & Node↔Go Boundary

- Go binary `native-egress`, **spawned and managed by Meridian (Node) as a child process** at startup, listening on `127.0.0.1:<ephemeral-port>`.
- **Node front door is unchanged**: adapter detection, profiles, settings, telemetry, and the `internal` + SDK `passthrough` modes all stay in Node.
- For a native-eligible request, Node POSTs to the Go sidecar: the **raw client body**, the **profile descriptor** (`claudeConfigDir`, account id, profile type), and the **stream flag**. Go does everything and **streams the response back**; Node pipes it to the client.
- Go owns: OAuth token read + 401 refresh (per-profile `CLAUDE_CONFIG_DIR`; Linux `.credentials.json`, macOS Keychain), fingerprint capture/cache, the three disguise layers, the forward, and streaming.

---

## 5. Mode Precedence & Degrade (non-breaking)

```
native toggle OFF
  → unchanged: existing internal / SDK passthrough

native toggle ON
  → prefer native (Go sidecar)
      success            → return native result
      ANY native failure → degrade to SDK passthrough; request still completes
```

"Native failure" = Go sidecar not running / spawn failed / health-check failed / connection error / fingerprint capture failed (Go returns `X-Degrade: 1`) / **upstream non-2xx incl. 429**.

> This **supersedes the earlier decision** ("native rejection → return error"). The current rule is: native failure always degrades to SDK so the user is never interrupted.

**Circuit breaker:** after N consecutive native failures (default 3), pause native for a cooldown (default 60s) and serve via SDK directly (no wasted native attempt); after cooldown, retry native once and resume on success. Prevents native-then-SDK double requests while native is persistently unavailable.

---

## 6. Observability (full, default-on for the experimental phase)

Per native request, recorded by Go (the actual sender) and surfaced through Meridian's logs + telemetry:
- **Routing**: `relay.native` / `relay.native_degrade` (with reason) / `relay.native_circuit_open`.
- **Complete outgoing request headers** actually sent to Anthropic (so the disguise is verifiable).
- **Complete body** (system / messages / tools).
- **Upstream result**: status, select response headers, usage, duration.
- **Capture events**: `cli_fingerprint.capture` (captured UA / x-stainless / beta, source, binary version, TTL) / `cli_fingerprint.fail`.

Rules:
- 🔒 `authorization` Bearer token is **always redacted** (`Bearer ***<last4>`). Non-negotiable.
- Go logs to stdout/stderr → piped into Meridian's log stream (`docker logs` / dashboard). Native requests are **also recorded in Meridian telemetry** (fixes the prior native-is-invisible blind spot).
- Verbosity: lightweight summary (path/reason/status/duration/fingerprint source) always on; **full headers + body default-ON** during the experimental phase, gated by `MERIDIAN_NATIVE_DEBUG` (default `1`) so it can be turned down later.

---

## 7. Configuration

- `MERIDIAN_NATIVE_FORWARD=1` — global enable (server-side).
- Per-adapter `nativeForward` (default off) and `nativeBodyCheck` (default on) — existing toggles.
- `x-meridian-mode: sdk` — per-request opt-OUT only (a client can never opt IN).
- `MERIDIAN_NATIVE_FINGERPRINT_TTL` (default `5m`).
- `MERIDIAN_NATIVE_DEBUG` (default `1` experimentally) — full request logging.

---

## 8. Build & Deploy

- `native-egress` Go binary built in the Docker image (Go toolchain at build time), multi-arch `linux/amd64` + `linux/arm64`. Node spawns the platform-matching binary.
- Dev: the binary can be run standalone; Node auto-spawns when present, degrades when absent.
- No change to the npm package's runtime for users who don't enable native (the Go binary is inert unless native is on).

---

## 9. Module / Component Boundaries

```
Node (Meridian front door)
  server.ts → routes native-eligible → nativeClient.ts (loopback HTTP to Go) → degrade-to-SDK on failure
  nativeSupervisor.ts → spawn/health/lifecycle of the Go child process + circuit breaker
Go (native-egress/)
  main.go            → loopback HTTP server (one endpoint: relay)
  fingerprint.go     → ANTHROPIC_LOG=debug capture + parse + (account,version) TTL cache
  cloak_headers.go   → assemble headers (captured envelope + per-request bits)
  cloak_body.go      → identity injection + cache_control sanitize + metadata.user_id
  utls_transport.go  → uTLS Chrome RoundTripper + manual HTTP/2
  oauth.go           → read creds (file/keychain) + 401 refresh
  relay.go           → orchestrate: token → fingerprint → cloak → forward → stream
  log.go             → structured logging (token redaction)
```

Node leaf rules unchanged (`nativeClient.ts` is a thin HTTP client; no SDK/session imports beyond what's needed).

---

## 10. Testing

- **Go unit:** fingerprint debug-log parse; header assembly (UA/x-stainless/session-id/client-request-id, Accept by stream); body cloak (identity, cache_control sanitize, user_id); uTLS handshake against a local TLS server; degrade signal.
- **Node↔Go integration:** native-eligible routes to the sidecar (mock Go server); sidecar unavailable / `X-Degrade` → SDK fallback; circuit breaker opens after N failures.
- **E2E (success criterion):** through the real new-api → Meridian → Go chain, a genuine CC-shaped request reaches `api.anthropic.com` and returns **200 (not 429)**, while the existing SDK modes remain unaffected.

---

## 11. Risk

- TLS uses a Chrome approximation, not an exact `claude-cli` JA3 — proven to work via CLIProxyAPI, but remains cat-and-mouse; Anthropic could tighten and re-flag.
- Adds a Go toolchain to the build + a child-process to the runtime (more deploy complexity).
- Residual behavioral signals (volume, account/IP) remain — keep single-account, one IP, and rate self-awareness; native stays opt-in/off by default.
- Conflicts with Meridian's stated "everything through the SDK" philosophy; native is an experimental, opt-in deviation.

---

## 12. Cleanup / Migration of the Prior (Node) Native Code

The earlier Node-only native attempts are **ineffective** (Node `fetch` can't spoof the TLS fingerprint → 429) and must be cleaned up as part of this work.

**Remove (dead — replaced by the Go subsystem):**
- `src/proxy/transparentRelay.ts` — Node `forwardNative` / `buildRelayHeaders` (Node egress; superseded by Go `relay.go`/`cloak_headers.go`).
- `src/proxy/cliFingerprint.ts` (on the unmerged branch) and any `claudeEnvelope.ts` remnants — Node fingerprint capture; superseded by Go `fingerprint.go`.
- Their unit/integration tests for the Node egress (`transparentRelay-*.test.ts`, `cliFingerprint-*.test.ts`).
- README/ARCHITECTURE prose describing the Node "mirror headers" mechanism — rewrite to the Go subsystem.

**Keep / adapt (still valid Node-side scaffolding):**
- Per-adapter `nativeForward` / `nativeBodyCheck` settings + the SDK Features UI toggles.
- `ccShape.isClaudeCodeShaped` — the anti-forgery body check (Node runs it before delegating to Go).
- `relayMode.nativeEligible` — native eligibility gating.
- The `server.ts` native branch — but it now **delegates to the Go sidecar** (via `nativeClient.ts`) instead of calling the Node `forwardNative`, and degrades to SDK on failure.

**Open PRs / branches:**
- **Close PR #3 (`feat/native-cli-fingerprint`)** — superseded by this Go design; do not merge.
- The merged Node native (PR #1/#2 on `main`) is removed/adapted by the implementation PR for this spec (the dead egress/capture deleted, the scaffolding above retained).

**Test node (23.134.76.22):**
- Restore to safe defaults: remove `MERIDIAN_NATIVE_FORWARD=1` and `OPENCODE_CLAUDE_PROVIDER_DEBUG=1` from `docker-compose.override.yml` (back to stable SDK passthrough), remove `/tmp` test payloads. Re-enable native only after the Go subsystem is deployed.
