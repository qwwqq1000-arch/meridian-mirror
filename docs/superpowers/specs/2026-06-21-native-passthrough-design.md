# Native Passthrough (Third Mode) — Design

**Date:** 2026-06-21
**Status:** Draft — pending review
**Topic:** A third relay mode that forwards requests verbatim to `api.anthropic.com`, bypassing the Claude Agent SDK, to eliminate token overhead from MCP tool re-wrapping and prompt injection.

---

## 1. Problem

The current **passthrough** mode routes every request through the Agent SDK's `query()`. Because the SDK only accepts built-in tools or MCP tools, Meridian wraps each client tool in an MCP server (`passthroughTools.ts`). For a request this means:

- Tool names gain an `mcp__oc__` prefix that repeats in every `tool_use` / `tool_result` for the life of the conversation.
- `jsonSchemaToZod` is lossy — complex schemas degrade to `z.any()`, so the upstream tool definition differs from what the client sent.
- The SDK injects MCP scaffolding into the system prompt.
- `buildCwdNote` appends an `<env>` + `<meridian-note>` block when client CWD ≠ proxy CWD.

The net effect: wasted tokens and reduced fidelity. The goal of native passthrough is **"forward what the user sent, verbatim"** — no MCP wrapping, no injected prompts.

The architectural fact driving the whole design: **the Agent SDK has no entry point for forwarding arbitrary Anthropic-format tools.** True verbatim forwarding is only possible by bypassing the SDK and calling `api.anthropic.com` directly with the Max OAuth token as a Bearer credential — exactly the pattern `oauthUsage.ts` already uses against `/api/oauth/usage`.

---

## 2. Goals & Non-Goals

**Goals**
- Add a **third, additive** relay mode (`native`) that does not change the two existing modes (`internal`, `passthrough`).
- Forward the request body verbatim (messages, tools, tool_choice untouched).
- Make the request indistinguishable from a genuine Claude Code request at the **fingerprint** level, captured dynamically so it follows version bumps.
- Default to the lowest-risk scope (genuine Claude Code clients only).
- Surface failures honestly (no silent fallback).

**Non-Goals**
- Wire-format translation for OpenAI-style clients (out of scope; those keep using existing modes).
- Account rotation / multi-account sharing (explicitly rejected — it is the primary cause of bans and violates the project's philosophy).
- Eliminating account risk. Fingerprint spoofing reduces *static* detectability only; behavioral risk is intrinsic and cannot be fully removed.

---

## 3. Risk Statement (read first)

This feature **conflicts with Meridian's stated philosophy** ("everything through the SDK; Anthropic stays in control of caching, limits, and auth"). Since January 2026 Anthropic actively restricts Pro/Max OAuth tokens used outside Claude.ai / Claude Code, returning errors such as *"OAuth authentication is currently not supported."*

A perfect fingerprint does **not** make this safe. Behavioral signals (request volume, tool/prompt shape diverging from genuine CLI usage, account/IP sharing) can still flag an account. The only durable mitigation is to stay within a legitimate single-user envelope.

Consequently the feature is **off by default**, **opt-in**, scoped to genuine Claude Code clients by default, and documented with this risk. Non-CC use requires a second explicit opt-in with a "higher risk" warning.

---

## 4. Mode Selection (per-adapter SDK feature)

Mode lives in the **existing per-adapter feature store** (`sdkFeatures.ts` → `~/.config/meridian/sdk-features.json`), surfaced by the existing SDK Features page. One new `AdapterFeatures` field:

```jsonc
// per adapter, e.g. config["claude-code"]
{ "relayMode": "auto" }   // "auto" | "internal" | "passthrough" | "native"
```

- **`"auto"` is the default for every adapter** and preserves today's behavior exactly: the transform pipeline decides `internal` vs `passthrough`. Existing users see no change.
- `"internal"` / `"passthrough"` pin one of the existing two modes explicitly (override the pipeline-computed `passthrough` boolean).
- `"native"` selects the third mode for that adapter.

The per-adapter granularity replaces a separate "allow non-CC" flag: selecting `native` on the `claude-code` adapter card is the low-risk case; selecting it on a non-CC adapter card (OpenCode, Crush, …) is the explicit higher-risk opt-in, and the Settings UI shows a "higher risk" warning at that point.

**Activation surfaces**
- SDK Features page (`settingsPage.ts`): a `relayMode` dropdown per adapter; selecting `native` on a non-`claude-code` card triggers a "higher risk" confirmation.
- Existing routes `GET/PATCH/DELETE /settings/api/features[/:adapter]` persist it (no new routes — `validateFeatureUpdate` allowlist gains the `relayMode` enum).
- `MERIDIAN_NATIVE_FORWARD=1`: env synonym that forces `relayMode: "native"` (escape hatch / headless).
- `x-meridian-mode: native | sdk` request header: per-request override.

**Eligibility gate** — `shouldNativeForward(mode, profile)` returns true only when:
1. effective relay mode is `native`, AND
2. profile type is `claude-max` or `oauth-token` (must have a real OAuth token; `api` profiles never qualify).

(There is no `claude-code`-only hard gate in code — the adapter scope is expressed by *which adapter card* has `relayMode: native` set. Non-CC selection is allowed but UI-warned, per the user's intent.)

---

## 5. Component: `claudeEnvelope.ts` (dynamic fingerprint capture)

**Purpose:** capture the real HTTP fingerprint the bundled `claude` binary emits, so forwarded requests match the genuine CLI and follow version bumps automatically (no static drift → ban).

**Mechanism (lazy, on first native request):**
1. Spawn the **same** `claude` executable resolved by `models.ts`, with a trivial prompt (`claude -p "hi"` equivalent) and `ANTHROPIC_BASE_URL=http://127.0.0.1:<ephemeral-port>`.
2. Stand up a one-shot loopback HTTP listener that captures the incoming `POST /v1/messages` headers: `user-agent`, the full `anthropic-beta` set, `anthropic-version`, `x-app`, and all `x-stainless-*`.
3. Respond with a minimal valid SSE stream (`message_start` → `message_stop`) so the subprocess exits cleanly and does **not** retry (a retry would pollute `x-stainless-retry-count`).
4. Cache the captured fingerprint, keyed by binary path + version/mtime. Re-capture when the key changes. De-duplicate concurrent captures with an in-flight promise map (same pattern as `tokenRefresh.ts`'s `inflightRefreshByKey`).

**Rules**
- Capture **only non-auth fingerprint headers.** `Authorization` is never captured — `transparentRelay.ts` injects the per-profile OAuth token. Fingerprint headers are emitted unconditionally, so capture works regardless of local auth state.
- `x-stainless-retry-count` and `x-stainless-timeout` are **per-request** — recompute them per relay, do not replay statically.
- **Capture failure** (binary missing, timeout) → `getFingerprint()` returns `null`. There is **no hardcoded baseline fingerprint** — a fixed/stale fingerprint is precisely the "drift → flagged" liability this feature avoids. When the fingerprint is unavailable, the request **degrades to the existing SDK passthrough mode** (see §6 / §7 of the plan): the native branch logs `relay.native_fallback_passthrough` and falls through to the normal SDK path. (Distinct from runtime relay rejection by Anthropic in §6, which returns an error.)

---

## 6. Component: `transparentRelay.ts` (verbatim forward)

**Flow:**
0. The caller (`server.ts`) obtains the fingerprint first via `getFingerprint()`. If it is `null` (capture failed), the caller does **not** invoke `forwardNative` — it degrades to SDK passthrough. So `forwardNative` always receives a real captured `fingerprint` as an input parameter (it does not fetch it itself, and there is no baseline).
1. Read the profile's OAuth access token from the credential store (`tokenRefresh.ts`). On upstream 401, refresh once and retry (mirrors `oauthUsage.ts`).
2. Build outgoing headers = the passed-in `fingerprint` + `Authorization: Bearer <token>`. Strip the client's placeholder `x-api-key`, `host`, and `content-length`.
3. **System identity:** ensure `system[]`'s first block is exactly `You are Claude Code, Anthropic's official CLI for Claude.`
   - Genuine CC client already carries it → leave untouched.
   - Non-CC client → insert via **anchor-based** rewrite. Must not corrupt body text that merely contains a keyword (lesson from opencode issue #17828 — never blind string-replace).
   - **Never replay the full captured CLI system prompt** — it is thousands of tokens and would re-inject exactly the overhead this feature removes. The envelope is for *headers only*; identity injection is one line.
4. Forward the rest of the body **verbatim** (`messages`, `tools`, `tool_choice`, `model`, etc.).
5. Stream the upstream SSE response straight back to the client (zero-buffering pipe). Non-streaming requests pass the JSON body through.
6. On any non-2xx upstream status → **return the error to the client.** No fallback to SDK modes, no circuit breaker.

---

## 7. Behavioral Safety

Fingerprinting addresses static detection; these address behavioral signals:

- **Rate-limit awareness:** before relaying, consult `rateLimitStore` / `oauthUsage` window utilization; back off / queue when near a limit or reset boundary rather than pushing through.
- **Concurrency cap:** reuse the existing server semaphore (`MERIDIAN_MAX_CONCURRENT`).
- **Single-user posture (documented):** one profile = one account = one person on one IP. The docs explicitly advise against account or IP sharing/rotation. This is not enforced in code but is the decisive factor in real-world bans.

Irreducible signals (documented, not "solved"): non-CC tool/prompt shape differs from the genuine CLI catalog — the mitigation is scope (default CC-only), not spoofing.

---

## 8. Module Boundaries

Dependencies flow downward, per `CLAUDE.md` / `ARCHITECTURE.md`:

```
server.ts
  ├─ relayMode.ts               (new, pure) — resolveRelayMode + shouldNativeForward
  └─ transparentRelay.ts        (new)
       ├─ claudeEnvelope.ts      (new) ─→ models.ts
       ├─ tokenRefresh.ts
       └─ (ensureClaudeCodeIdentity — pure, exported from transparentRelay.ts)
sdkFeatures.ts (+ settingsPage.ts) → relayMode per-adapter field
```

- `relayMode.ts`, `claudeEnvelope.ts`, and `transparentRelay.ts` are leaf modules: no imports from `server.ts` or `session/`.
- Integration point in `server.ts` `handleMessages`: after adapter detection + profile resolution, before `buildQueryOptions`:
  ```ts
  if (shouldNativeForward(adapter, profile, ctx)) return forwardNative(...)
  ```
  The SDK path is otherwise untouched.

---

## 9. Testing

- **Unit (pure, no mocks):**
  - Fingerprint header parsing + version-keyed cache logic (`claudeEnvelope`).
  - Anchor-based identity injection: CC client untouched; non-CC gets identity prepended; keyword-in-body not corrupted (#17828 regression).
  - `shouldNativeForward` eligibility matrix (mode × profile type × adapter × nativeAllowNonCc).
- **Integration (mocked fetch/HTTP):**
  - Header assembly: Bearer injected, placeholder `x-api-key`/`host` stripped, fingerprint applied.
  - Streaming SSE pass-through.
  - Non-2xx upstream → error returned to client (no fallback).
  - 401 → single refresh + retry.
- **E2E (manual, real Claude Max):** capture a real fingerprint; round-trip one native request; verify token usage vs SDK passthrough.

---

## 10. Open Questions / Follow-ups

- Exact baseline fingerprint constants for the capture-failure fallback (fill from a first real capture).
- Whether `x-meridian-mode` per-request override should be gated behind the same eligibility checks (assume yes).
- Whether to expose a `/health` indicator showing native mode active + last fingerprint capture time.
