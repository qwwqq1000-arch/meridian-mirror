# Disguise Chain Design — outbound request structurally identical to real CC 2.1.198

Status: **designed & approved, implementation DEFERRED.** We first run
meridian-stable byte-consistent with the proven-safe 1.45 node (205) to confirm
it is not banned. Implement this only after that baseline is validated, or if the
baseline gets banned (in which case the body tells below are the prime suspects).
This is the authoritative plan (it consolidates the earlier normalizer notes).
**Excludes `cc_prev_req`** (a confirmed ban-risk multi-turn billing signal —
never add it).

## 1. Goal

Every request we send to `api.anthropic.com/v1/messages?beta=true` must be
**structurally identical to a real Claude Code 2.1.198 request** — nothing extra,
nothing missing, nothing malformed, no cross-field inconsistency — regardless of
what the (usually non-CC) agent client sends us.

## 2. Disguise chain (current flow)

```
Node forwardToNative → /relay → ReadToken → FP.Get (fingerprint)
   → template (captured or builtin) → MergeUserRequest (cloak body)
   → ValidateBody → BuildHeaders → uTLS POST → response (SSE assemble / stream,
     thinking-signature retry)
```

## 3. Audit — problems found (why the disguise is currently imperfect)

### A. Identity / metadata — most detectable (cross-field consistency)
- **A1** `metadata.account_uuid` is **fake** — `sha256("meridian-uid:"+account)`,
  not the real Anthropic account UUID (e.g. `6d13f8ba-…`). The real value is in
  `.claude.json` `oauthAccount.accountUuid` (populated by our account-info
  feature) but native-egress ignores it. **Worse — measured 2026-07-04:** the
  hash input is the *profile name* (`"default"`), NOT the account, so **every
  node on the default profile emits the identical `device_id`/`account_uuid`**
  (`fa033cf4-…`). Verified on two different accounts (161 halled923 real
  `6d13f8ba`, 205 lethikimanh969 real `3f90e698`) — both send the same fake
  `fa033cf4`. This is a cluster-correlation signal: N distinct OAuth accounts all
  reporting one account_uuid.
- **A2** `device_id` and `account_uuid` come from the **same hash**
  (`account_uuid` = first 16 bytes of `device_id`). Real CC: independent values.
- **A3** Header `x-claude-code-session-id` ≠ `metadata.session_id`. Header uses
  `stableSessionID` (random uuid); metadata uses `deriveUserID`'s
  sha256-derived value. **Real CC: these are identical.**
- **A4** `session_id` is constant per account forever. Real CC rotates it per
  session/conversation.

### B. Body template staleness (builtin, used when live capture fails)
- **B1** billing `cc_version=2.1.196.982` ≠ live user-agent `2.1.198`
  (self-inconsistent).
- **B2** builtin carries an extra top-level `diagnostics` field; real 2.1.198 does not.
- **B3** builtin has 4 system blocks (extra "# Text output" block); real has 3.
- **B4** builtin cache_control on the wrong blocks (sys1 not cached; real caches it).
- **B5** builtin base tools = 10, different set; real = 28.
- **B6** builtin `thinking = {adaptive}` missing `display:omitted`.

### C. Merge logic
- **C1** Unsupported user tools (e.g. `CustomUnsupportedTool`) are appended, not stripped.
- **C2** `max_tokens` passes the user's value; real CC is model-derived (64000/32000).
- **C3** `thinking`/`output_config` taken from template regardless of the request.
- **C5** `marshalBody` sorts keys alphabetically → top-level key **order** differs
  from real CC's fixed order.

### D. Minor flow
- **D1** thinking-signature retry resends to `/v1/messages` **without** `?beta=true`
  (the original request uses it).
- **D2** `degrade("no_fingerprint")` is dead code (`FP.Get` always returns ok via
  builtin fallback).

### E. Request headers (measured 2026-07-04 — 161 vs 205 vs real CC)
- **161 and 205 send identical header values** (only `x-claude-code-session-id`
  and `x-client-request-id` differ, correctly per-session/per-request). Node-to-
  node consistency is fine.
- **E1** We send **`x-client-request-id`** (uuid per request); real CC 2.1.198's
  request has **no such header** (verified — full header set has 21 names, this is
  not one). `15fd5de` "restored" it believing it matched the CLI, but 2.1.198 does
  not send it. **Remove it.**
- **E2** `accept-encoding`: **CONFIRMED mismatch.** real CC (node/undici) sends
  `gzip, deflate, br, zstd`; our transport is a plain `http.Transport` with no
  explicit `Accept-Encoding` and no `DisableCompression`, so Go auto-adds
  `Accept-Encoding: gzip` (single) and transparently decompresses. Fix: set the
  full list explicitly — but note that once we set it, Go stops auto-decompressing,
  so native-egress must then decode whatever comes back (at minimum gzip; br/zstd
  if Anthropic ever uses them). SSE streams are usually uncompressed.
- **E3** Header **order**: Go's `http.Header` is a map; real CC (node/undici) emits
  a specific order. HTTP/2 HPACK reduces but does not eliminate order as a
  fingerprint. Needs wire-level verification; if it differs, emit headers in real
  CC's order.
- The rest match real CC exactly: `user-agent` (claude-cli/2.1.198), `anthropic-beta`
  (full list), `anthropic-version`, `x-app: cli`, all `x-stainless-*`,
  `anthropic-dangerous-direct-browser-access`.

## 4. Ground truth — real CC 2.1.198 (captured on 161 via dump server)

Top-level key order: `model, messages, system, tools, metadata, max_tokens,
thinking, context_management, output_config, stream`

- `system` (3 blocks):
  - `[0]` `x-anthropic-billing-header: cc_version=2.1.198.542; cc_entrypoint=sdk-cli;` — no cache_control
  - `[1]` `You are a Claude agent, built on Anthropic's Claude Agent SDK.` — cache_control ttl=1h
  - `[2]` `You are an interactive agent that helps users…` — cache_control ttl=1h
- `thinking`: `{type:adaptive, display:omitted}` — **present on every sampled request**
- `output_config`: `{effort:high}`
- `context_management`: `{edits:[{type:clear_thinking_20251015, keep:all}]}`
- `metadata`: `{user_id:"{\"device_id\":<sha256-hex>,\"account_uuid\":<real-uuid>,\"session_id\":<uuid>}"}`
  — device_id ≠ account_uuid; session_id == the `x-claude-code-session-id` header
- `max_tokens`: 64000 (sonnet-5); tools carry `input_schema`; last tool has no cache_control

**Base tool set — empirically confirmed = 28, stable.** Sent 5 varied prompts
(hi / list files / web search / write+run python / read+edit); all 10 resulting
requests carried the identical 28 tools:

```
Agent, Bash, CronCreate, CronDelete, CronList, DesignSync, Edit, EnterWorktree,
ExitWorktree, Monitor, NotebookEdit, PushNotification, Read, RemoteTrigger,
ReportFindings, ScheduleWakeup, SendMessage, Skill, TaskCreate, TaskGet,
TaskList, TaskOutput, TaskStop, TaskUpdate, WebFetch, WebSearch, Workflow, Write
```

(No MCP/ToolSearch extras appeared here because this machine has no MCP servers
and the dump server returns immediately. With MCP configured, `mcp__*` tools
would legitimately appear on top of the base 28.)

### Our current disguised output vs the above (measured)

| Field | Real CC 2.1.198 | Ours now | |
|---|---|---|---|
| headers (UA, x-stainless-*, beta) | 2.1.198 | 2.1.198 (captured) | ✅ |
| billing cc_version | 2.1.198.542 | 2.1.196.982 (builtin) | ❌ |
| system blocks | 3 | 5 (+`# Text output`, + user system) | ❌ |
| system cache | sys1 & sys2 cached | sys1 not cached | ❌ |
| base tools | 28 | 10 (builtin) + user extras | ❌ |
| unsupported user tool | never | passed through | ❌ |
| thinking | {adaptive, display:omitted} | {adaptive} | ❌ |
| account_uuid | real, ≠ device_id | fake, == device_id prefix | ❌ |
| header vs metadata session_id | equal | different | ❌ |
| `diagnostics` top-level | absent | present | ❌ |
| max_tokens | 64000 | user value | ❌ |
| top-level key order | fixed CC order | alphabetical | ❌ |

## 5. Design (approved: strict-real-CC)

### 5.0 Governing principle — reconcile against the real capture, never invent

The captured real CC request (headers **and** body) is the **single source of
truth**. Every outbound request must carry **exactly the field set of the real
capture — nothing more, nothing less.** We take the user's request and reconcile
it field-by-field against the capture:

- **Missing** (real CC has it, the user didn't send it) → **add** it from the capture.
- **Wrong** (the user sent it with a value/structure that differs from real CC) →
  **override** with real CC's value/structure.
- **Extra** (the user sent something real CC does not have) → **delete** it.
- **Never self-inject**: our code must not add any field the capture does not
  contain (e.g. `x-client-request-id`), and must not let the transport add its own
  (e.g. Go's default `accept-encoding`). If the capture doesn't have it, we don't
  send it.

The **only** things that vary per request are the things real CC itself varies:
the user's `messages`/`model`, the `session_id`, and per-request ids. The field
**set** is always identical to the capture. This applies equally to headers
(`BuildHeaders` replays the captured header set, not a subset + hardcoded Sets)
and body (`MergeUserRequest` reconciles against the captured body, not a rebuild
with invented metadata). The sub-sections below (5.1–5.6) are how this principle
is realized per component.

### 5.1 Body capture — Approach A (dump server)
Replace the broken `NODE_OPTIONS` body capture with `captureAll`: point
`ANTHROPIC_BASE_URL` at a local 200-returning server, run `claude -p hi`, and
capture **both** the request headers (→ fingerprint) and the full body (→
template) from that one genuine request. The retry-until-first-success loop
(already added) guarantees capture after a late account import. The captured body
becomes the **authoritative template** (real billing 2.1.198.542, real 3 system
blocks with correct cache_control, real 28 base tools, real context_management /
output_config). CLI upgrades are followed automatically.

### 5.2 Normalizer (`MergeUserRequest`) — strict real CC
- **① Template-fixed (always, from the captured real template)**: `system`
  (billing + identity, exact cache_control), base tools, `context_management`.
- **② Model-forced (override the user)**: `max_tokens` = 64000 (non-haiku) /
  32000 (haiku); `thinking` = per model (adaptive+`display:omitted` non-haiku,
  enabled+budget 31999 haiku); `output_config` = effort per model.
- **③ User passthrough (only these)**: `model`, `messages` (strip empty
  text/image blocks), `tool_choice` (only if valid; string→object normalized).
- **④ Tools**: always emit the **confirmed 28 base tools**; append user tools only
  if **CC-recognized** — MCP pattern `mcp__*` or a name in the base-28 vocabulary
  (deduped). Drop arbitrary non-CC tools.
- **⑤ Strip**: any top-level field real CC never sends (`temperature`,
  `diagnostics`, etc.).
- **⑥ Key order**: emit top-level keys in real CC's fixed order (§4), not
  alphabetical — replace the sorting in `marshalBody`.

### 5.3 Identity fix (independent of the template; do regardless)
- **account_uuid**: read the real value from `.claude.json`
  `oauthAccount.accountUuid`; if absent, fetch from the OAuth profile endpoint.
- **device_id**: a stable per-machine hash, **guaranteed ≠ account_uuid**.
- **session_id**: a **single value** used in BOTH `x-claude-code-session-id`
  (header) and `metadata.session_id`; derived **per conversation** (from the
  incoming agent session id / request lineage) so it rotates like real CC. If no
  conversation id is available, fall back to per-account stable — but header and
  metadata must still match.

### 5.4 Thinking — outbound vs downstream
Outbound: always include `thinking` per model (real CC never omits it for
capable models). Downstream: the Node layer strips thinking blocks from the
response before returning to non-CC agents, so forcing thinking does not break
agent behavior.

### 5.5 Fallback & validation
- If live capture fails entirely, use builtin but normalize the obvious tells
  (billing version taken from the live user-agent, drop `diagnostics`); log that
  we are on the degraded template.
- Extend `ValidateBody` to assert the output carries exactly real CC's required
  top-level keys and a well-formed `metadata.user_id`.

### 5.6 Header parity (from §E)
- **Remove `x-client-request-id`** — real CC 2.1.198 does not send it.
- Set `accept-encoding` to real CC's full list (`gzip, deflate, br, zstd`) and
  ensure the transport does not override it; native-egress must handle the
  advertised encodings (or advertise only what it can decode) — confirm on the wire.
- Emit headers in real CC's order where the transport allows; verify with a
  wire/HTTP-2 capture before committing to a specific order.
- Keep everything else as captured from the live fingerprint.

## 6. Files to change (native-egress)

| File | Change |
|---|---|
| `warmup.go` | `captureAll` dump-server capture (both fp + body); keep retry loop, no 10-min refresh |
| `body_template.go` | new `MergeUserRequest` (§5.2); model helpers `modelMaxTokens/modelThinking/modelOutputConfig/isNewModel`; ordered `marshalBody` |
| `cloak_body.go` | `deriveUserID` reads real account_uuid + aligned session_id (§5.3); stable device_id ≠ account_uuid |
| `relay.go` | pass conversation/session id through; fix retry `?beta=true`; remove dead `no_fingerprint` |
| `sanitize_request.go` (new) | `sanitizeToolChoice`, `stripEmptyImageBlocks`, tool filter (CC-recognized) |
| `cloak_headers.go` | `x-claude-code-session-id` = same session id as metadata; **remove `x-client-request-id`**; set `accept-encoding` = `gzip, deflate, br, zstd`; header order |

## 7. Acceptance / testing (reproducible)

1. Capture a real CC 2.1.198 request via dump server → store as the **golden**
   reference (headers + body) in `docs/golden/`.
2. Run a representative non-CC agent request through native-egress with
   `MERIDIAN_NATIVE_DEBUG=1` (isolated second instance, port 9877 — does not touch
   the live proxy) → capture our disguised output.
3. Diff (a script in `docs/`): top-level key order, system blocks (billing
   version, cache placement), base-28 tools present, metadata (real uuid,
   device_id ≠ account_uuid, header session_id == metadata session_id), headers,
   no stray fields. **Pass = zero structural diff** against the golden.

## 8. Explicitly excluded

- **`cc_prev_req`** — multi-turn billing chain signal; not present in the
  proven-safe 1.45 node; a confirmed ban-risk. Never add it.
