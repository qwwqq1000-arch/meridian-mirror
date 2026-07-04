# Golden reference — cross-validated real CC 2.1.198 (Task 0)

Source: 20 real `claude` requests captured via dump server on 161 (2026-07-04),
8 varied prompts × default model + explicit haiku + explicit opus. Every value
below was **identical across all samples** unless noted as per-request-varying.
This is the reference Tasks 1–6 match and Task 7 diffs against. **Only values
confirmed stable here become code; nothing is assumed.**

## Headers (stable across all 20)

- Header **name set** identical every request. No `x-client-request-id` (absent in
  all 20 — real CC 2.1.198 does not send it).
- `accept-encoding`: `gzip, deflate, br, zstd` (stable).
- `user-agent`: `claude-cli/2.1.198 (external, sdk-cli)`; `x-app: cli`;
  `anthropic-version: 2023-06-01`; `anthropic-dangerous-direct-browser-access: true`;
  `x-stainless-arch: x64`, `x-stainless-lang: js`, `x-stainless-os: Linux`,
  `x-stainless-runtime: node`, `x-stainless-runtime-version: v26.3.0`,
  `x-stainless-package-version: 0.94.0`, `x-stainless-timeout: 600`,
  `x-stainless-retry-count: 0`.
- Per-request only: `x-claude-code-session-id` (== `metadata.session_id`, see below).

## Response encoding (verified earlier via ANTHROPIC_LOG=debug)

- Real CLI sends the full `accept-encoding` above; Anthropic responds
  `content-encoding: gzip` (JSON confirmed; `vary: Accept-Encoding`). Streaming
  (`text/event-stream`) case to be re-confirmed with 2–3 real calls before Task 3
  assumes it. Handle gzip (and defensively deflate/br/zstd), and never forward
  `content-encoding` downstream (avoids the `6737efc` double-decode).

## Identity (metadata.user_id) — cross-validated

- `account_uuid` = the **real** Anthropic account uuid (`6d13f8ba-…` for this
  account) in all 20. → our fake, account-name-derived uuid is wrong.
- `device_id` = stable per machine (one value across all 20), **independent of
  account_uuid** (not the same hash). → our `device_id == account_uuid` is wrong.
- `metadata.session_id` **==** the `x-claude-code-session-id` header in all 20. →
  our header/metadata mismatch is wrong.
- `session_id` rotates per conversation: 10 distinct sessions across 20 requests
  (each `claude -p` invocation = a new session; requests within one invocation
  share it). → our one-constant-per-account session is wrong.

## Body — per-model rules (confirmed)

| model | max_tokens | thinking | output_config |
|---|---|---|---|
| claude-sonnet-5 | 64000 | `{type:adaptive, display:omitted}` | `{effort:high}` |
| claude-opus-4-8 | 64000 | `{type:adaptive, display:omitted}` | `{effort:high}` |
| claude-haiku-4-5-20251001 | 32000 | `{type:enabled, budget_tokens:31999, display:omitted}` | (absent) |

Rule: haiku → 32000 / enabled+budget / no output_config; all others → 64000 /
adaptive / effort:high. `system` = 3 blocks; `stream` varies with the request.

## Body — system blocks (from the initial live capture)

- `[0]` `x-anthropic-billing-header: cc_version=2.1.198.542; cc_entrypoint=sdk-cli;` — **no** cache_control
- `[1]` `You are a Claude agent, built on Anthropic's Claude Agent SDK.` — cache_control `{type:ephemeral, ttl:1h}`
- `[2]` `You are an interactive agent that helps users…` — cache_control `{type:ephemeral, ttl:1h}`

Top-level key order: `model, messages, system, tools, metadata, max_tokens,
thinking, context_management, output_config, stream`.
`context_management`: `{edits:[{type:clear_thinking_20251015, keep:all}]}`.

## Base tools — 28, stable across all non-haiku AND haiku samples

```
Agent, Bash, CronCreate, CronDelete, CronList, DesignSync, Edit, EnterWorktree,
ExitWorktree, Monitor, NotebookEdit, PushNotification, Read, RemoteTrigger,
ReportFindings, ScheduleWakeup, SendMessage, Skill, TaskCreate, TaskGet,
TaskList, TaskOutput, TaskStop, TaskUpdate, WebFetch, WebSearch, Workflow, Write
```

Tools carry `input_schema`; the last tool has no `cache_control`. MCP tools
(`mcp__*`) legitimately add on top when MCP servers are configured; none appeared
here (no MCP configured on this machine).

## Still to capture (Task 0 remainder)

- Streaming response `content-encoding` (2–3 real calls) — confirm whether SSE is
  gzipped or identity, so Task 3 handles the real behavior.
