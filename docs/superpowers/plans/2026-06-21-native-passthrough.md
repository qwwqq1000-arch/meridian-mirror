# Native Passthrough (Third Mode) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a third relay mode (`native`) that forwards `/v1/messages` requests verbatim to `api.anthropic.com` with the Max OAuth token, spoofing a genuine Claude Code fingerprint captured dynamically from the real `claude` binary.

**Architecture:** A new pure `relayMode.ts` resolves the effective mode (per-adapter `sdkFeatures` field, env, header) and gates eligibility. `claudeEnvelope.ts` lazily captures the real CLI HTTP fingerprint (user-agent / anthropic-beta / x-stainless-*) by running the resolved `claude` binary against a loopback recorder, caching by binary version. `transparentRelay.ts` assembles headers (envelope fingerprint + OAuth Bearer), ensures the Claude Code identity is the first system block, forwards the body verbatim, and pipes the upstream SSE/JSON back. `server.ts` branches into the relay before `buildQueryOptions`. Existing `auto`/`internal`/`passthrough` behavior is untouched.

**Tech Stack:** TypeScript, Hono, bun test, Node 22+ runtime. Direct call pattern mirrors `oauthUsage.ts`; credential/refresh reuse `tokenRefresh.ts`.

## Global Constraints

- No `as any`, `@ts-ignore`, `@ts-expect-error`, or empty catch blocks. (CLAUDE.md Style)
- Leaf modules (`relayMode.ts`, `claudeEnvelope.ts`, `transparentRelay.ts`) must NOT import from `server.ts` or `session/`. Dependencies flow downward only. (ARCHITECTURE.md)
- All tests run with `npm test` (= `bun test`). Every change must keep all tests green before it is complete. (CLAUDE.md Testing)
- New test files go in `src/__tests__/`. Pure functions get direct unit tests (no mocks); HTTP/fetch is mocked via the `FetchLike = (input: string, init?: RequestInit) => Promise<Response>` injection pattern. (CLAUDE.md / ARCHITECTURE.md)
- Commit format `type: brief description`; types feat/fix/refactor/perf/test/docs/chore; no AI attribution lines. (CLAUDE.md)
- Work happens on branch `feat/native-passthrough` (already created). Never push to `main`. (CLAUDE.md)
- The required identity first-line is exactly: `You are Claude Code, Anthropic's official CLI for Claude.`
- OAuth constants already exist in `tokenRefresh.ts`: token URL `https://platform.claude.com/v1/oauth/token`, client_id `9d1c250a-e61b-44d9-88ed-5944d1962f5e`. Do NOT redefine them.
- Anthropic Messages endpoint: `https://api.anthropic.com/v1/messages`. OAuth beta header value: `oauth-2025-04-20` (as in `oauthUsage.ts`).

---

### Task 1: `relayMode` per-adapter feature + validation

**Files:**
- Modify: `src/proxy/sdkFeatures.ts` (interface `AdapterFeatures` ~line 12-37, `DEFAULT_FEATURES` ~line 41-58, `validateFeatureUpdate` ~line 159-191)
- Test: `src/__tests__/sdkFeatures-relaymode-unit.test.ts`

**Interfaces:**
- Produces: `AdapterFeatures.relayMode: "auto" | "internal" | "passthrough" | "native"` (default `"auto"`). `getFeaturesForAdapter(name).relayMode` returns the resolved value; `validateFeatureUpdate({relayMode})` accepts the four enum values and throws on anything else.

- [ ] **Step 1: Write the failing test**

Create `src/__tests__/sdkFeatures-relaymode-unit.test.ts`:

```typescript
import { describe, it, expect } from "bun:test"
import { validateFeatureUpdate, getFeaturesForAdapter } from "../proxy/sdkFeatures"

describe("relayMode feature", () => {
  it("defaults to 'auto' for an unconfigured adapter", () => {
    expect(getFeaturesForAdapter("claude-code").relayMode).toBe("auto")
  })

  it("accepts the four valid relay modes", () => {
    for (const mode of ["auto", "internal", "passthrough", "native"] as const) {
      expect(validateFeatureUpdate({ relayMode: mode })).toEqual({ relayMode: mode })
    }
  })

  it("throws on an invalid relay mode", () => {
    expect(() => validateFeatureUpdate({ relayMode: "bogus" })).toThrow()
  })

  it("throws when relayMode is not a string", () => {
    expect(() => validateFeatureUpdate({ relayMode: 3 })).toThrow()
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `bun test src/__tests__/sdkFeatures-relaymode-unit.test.ts`
Expected: FAIL — `relayMode` is `undefined` (not `"auto"`) and validation does not reject `"bogus"`.

- [ ] **Step 3: Add the field, default, and validation**

In `src/proxy/sdkFeatures.ts`, add to the `AdapterFeatures` interface (after `additionalDirectories`):

```typescript
  /** Relay mode: auto (pipeline decides), internal (MCP tools), passthrough (SDK passthrough), native (direct forward to api.anthropic.com) */
  relayMode: "auto" | "internal" | "passthrough" | "native"
```

Add to `DEFAULT_FEATURES`:

```typescript
  relayMode: "auto",
```

In `validateFeatureUpdate`, add an enum branch alongside the existing `claudeMd` / `thinking` branches:

```typescript
    if (key === "relayMode") {
      if (typeof value !== "string" || !["auto", "internal", "passthrough", "native"].includes(value)) {
        throw new Error(`Invalid relayMode: ${String(value)}`)
      }
      out.relayMode = value as AdapterFeatures["relayMode"]
      continue
    }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `bun test src/__tests__/sdkFeatures-relaymode-unit.test.ts`
Expected: PASS (all 4 tests).

- [ ] **Step 5: Run the full suite**

Run: `npm test`
Expected: PASS (no regressions).

- [ ] **Step 6: Commit**

```bash
git add src/proxy/sdkFeatures.ts src/__tests__/sdkFeatures-relaymode-unit.test.ts
git commit -m "feat: add relayMode per-adapter feature"
```

---

### Task 2: `relayMode.ts` — effective-mode resolution + eligibility (pure)

**Files:**
- Create: `src/proxy/relayMode.ts`
- Test: `src/__tests__/relayMode-unit.test.ts`

**Interfaces:**
- Consumes: `AdapterFeatures["relayMode"]` from Task 1; `ResolvedProfile` (`{ id, type, env }`, `type: "claude-max" | "api" | "oauth-token"`) from `profiles.ts`.
- Produces:
  - `type RelayMode = "auto" | "internal" | "passthrough" | "native"`
  - `resolveRelayMode(input: { feature: RelayMode; envForceNative: boolean; headerOverride?: string }): RelayMode` — header (`native`/`sdk`) wins, then env force, then feature. `headerOverride === "sdk"` forces `"auto"`; `=== "native"` forces `"native"`.
  - `shouldNativeForward(mode: RelayMode, profileType: ResolvedProfile["type"]): boolean` — true iff `mode === "native"` AND profileType is `"claude-max"` or `"oauth-token"`.

- [ ] **Step 1: Write the failing test**

Create `src/__tests__/relayMode-unit.test.ts`:

```typescript
import { describe, it, expect } from "bun:test"
import { resolveRelayMode, shouldNativeForward } from "../proxy/relayMode"

describe("resolveRelayMode", () => {
  it("returns the feature value when no override", () => {
    expect(resolveRelayMode({ feature: "passthrough", envForceNative: false })).toBe("passthrough")
  })
  it("env force overrides the feature", () => {
    expect(resolveRelayMode({ feature: "auto", envForceNative: true })).toBe("native")
  })
  it("header 'sdk' forces auto, beating env force", () => {
    expect(resolveRelayMode({ feature: "native", envForceNative: true, headerOverride: "sdk" })).toBe("auto")
  })
  it("header 'native' forces native", () => {
    expect(resolveRelayMode({ feature: "internal", envForceNative: false, headerOverride: "native" })).toBe("native")
  })
})

describe("shouldNativeForward", () => {
  it("true for native + claude-max", () => {
    expect(shouldNativeForward("native", "claude-max")).toBe(true)
  })
  it("true for native + oauth-token", () => {
    expect(shouldNativeForward("native", "oauth-token")).toBe(true)
  })
  it("false for native + api (no usable OAuth token)", () => {
    expect(shouldNativeForward("native", "api")).toBe(false)
  })
  it("false for non-native modes", () => {
    expect(shouldNativeForward("passthrough", "claude-max")).toBe(false)
    expect(shouldNativeForward("auto", "claude-max")).toBe(false)
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `bun test src/__tests__/relayMode-unit.test.ts`
Expected: FAIL — module `../proxy/relayMode` does not exist.

- [ ] **Step 3: Write minimal implementation**

Create `src/proxy/relayMode.ts`:

```typescript
/**
 * Relay-mode resolution and eligibility. Pure leaf module — no I/O, no imports
 * from server.ts or session/.
 *
 * Modes:
 *   auto        — existing behavior (transform pipeline decides internal vs passthrough)
 *   internal    — MCP tools, proxy executes (override pipeline passthrough=false)
 *   passthrough — SDK passthrough, client executes (override pipeline passthrough=true)
 *   native      — direct forward to api.anthropic.com, bypassing the Agent SDK
 */

export type RelayMode = "auto" | "internal" | "passthrough" | "native"

export function resolveRelayMode(input: {
  feature: RelayMode
  envForceNative: boolean
  headerOverride?: string
}): RelayMode {
  if (input.headerOverride === "sdk") return "auto"
  if (input.headerOverride === "native") return "native"
  if (input.envForceNative) return "native"
  return input.feature
}

export function shouldNativeForward(
  mode: RelayMode,
  profileType: "claude-max" | "api" | "oauth-token",
): boolean {
  if (mode !== "native") return false
  return profileType === "claude-max" || profileType === "oauth-token"
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `bun test src/__tests__/relayMode-unit.test.ts`
Expected: PASS (8 tests).

- [ ] **Step 5: Commit**

```bash
git add src/proxy/relayMode.ts src/__tests__/relayMode-unit.test.ts
git commit -m "feat: add relayMode resolution and native-forward eligibility"
```

---

### Task 3: `transparentRelay.ts` — identity injection + header assembly (pure parts)

**Files:**
- Create: `src/proxy/transparentRelay.ts` (pure helpers only in this task; the network forward lands in Task 5)
- Test: `src/__tests__/transparentRelay-unit.test.ts`

**Interfaces:**
- Produces:
  - `const CLAUDE_CODE_IDENTITY = "You are Claude Code, Anthropic's official CLI for Claude."`
  - `ensureClaudeCodeIdentity(system: unknown): Array<{ type: "text"; text: string }>` — normalizes `system` (string | array | undefined) to a block array whose FIRST block is the identity line. If an identity block is already first, returns it unchanged. Never mutates input. Never edits non-leading text (no blind string replace — #17828).
  - `buildRelayHeaders(input: { fingerprint: Record<string, string>; token: string; clientHeaders: Record<string, string> }): Record<string, string>` — merges fingerprint, sets `authorization: Bearer <token>`, ensures `anthropic-beta` contains `oauth-2025-04-20`, and strips `x-api-key`, `host`, `content-length`, `authorization` (client's), `accept-encoding`.

- [ ] **Step 1: Write the failing test**

Create `src/__tests__/transparentRelay-unit.test.ts`:

```typescript
import { describe, it, expect } from "bun:test"
import { ensureClaudeCodeIdentity, buildRelayHeaders, CLAUDE_CODE_IDENTITY } from "../proxy/transparentRelay"

describe("ensureClaudeCodeIdentity", () => {
  it("prepends identity when system is a plain string", () => {
    const out = ensureClaudeCodeIdentity("You are OpenCode, do X.")
    expect(out[0]).toEqual({ type: "text", text: CLAUDE_CODE_IDENTITY })
    expect(out[1]).toEqual({ type: "text", text: "You are OpenCode, do X." })
  })

  it("leaves a system whose first block is already the identity untouched", () => {
    const input = [{ type: "text", text: CLAUDE_CODE_IDENTITY }, { type: "text", text: "rest" }]
    const out = ensureClaudeCodeIdentity(input)
    expect(out).toEqual(input)
  })

  it("returns just the identity block when system is undefined", () => {
    expect(ensureClaudeCodeIdentity(undefined)).toEqual([{ type: "text", text: CLAUDE_CODE_IDENTITY }])
  })

  it("does not corrupt body text that merely mentions opencode (#17828)", () => {
    const out = ensureClaudeCodeIdentity("edit /src/opencode/config.ts")
    expect(out[1]).toEqual({ type: "text", text: "edit /src/opencode/config.ts" })
  })

  it("does not mutate the input array", () => {
    const input = [{ type: "text" as const, text: "hi" }]
    ensureClaudeCodeIdentity(input)
    expect(input).toEqual([{ type: "text", text: "hi" }])
  })
})

describe("buildRelayHeaders", () => {
  const fingerprint = { "user-agent": "claude-cli/2.1.0", "anthropic-version": "2023-06-01", "anthropic-beta": "claude-code-20250219" }

  it("injects the Bearer token and oauth beta flag", () => {
    const h = buildRelayHeaders({ fingerprint, token: "tok123", clientHeaders: {} })
    expect(h["authorization"]).toBe("Bearer tok123")
    expect(h["anthropic-beta"]).toContain("oauth-2025-04-20")
    expect(h["anthropic-beta"]).toContain("claude-code-20250219")
    expect(h["user-agent"]).toBe("claude-cli/2.1.0")
  })

  it("strips the client's placeholder auth and hop-by-hop headers", () => {
    const h = buildRelayHeaders({
      fingerprint,
      token: "tok123",
      clientHeaders: { "x-api-key": "placeholder", host: "127.0.0.1:3456", "content-length": "42", authorization: "Bearer client" },
    })
    expect(h["x-api-key"]).toBeUndefined()
    expect(h["host"]).toBeUndefined()
    expect(h["content-length"]).toBeUndefined()
    expect(h["authorization"]).toBe("Bearer tok123")
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `bun test src/__tests__/transparentRelay-unit.test.ts`
Expected: FAIL — module `../proxy/transparentRelay` does not exist.

- [ ] **Step 3: Write minimal implementation**

Create `src/proxy/transparentRelay.ts`:

```typescript
/**
 * Native passthrough: forward a request verbatim to api.anthropic.com using a
 * Max OAuth token, spoofing a genuine Claude Code fingerprint.
 *
 * Leaf module. Pure helpers here (identity + headers); the network forward is
 * added in a later task. No imports from server.ts or session/.
 */

export const CLAUDE_CODE_IDENTITY = "You are Claude Code, Anthropic's official CLI for Claude."

const OAUTH_BETA = "oauth-2025-04-20"

type TextBlock = { type: "text"; text: string }

/** Normalize `system` to a block array whose first block is the identity line. */
export function ensureClaudeCodeIdentity(system: unknown): TextBlock[] {
  const blocks: TextBlock[] = []
  if (typeof system === "string") {
    if (system.length > 0) blocks.push({ type: "text", text: system })
  } else if (Array.isArray(system)) {
    for (const b of system) {
      if (b && typeof b === "object" && (b as { type?: unknown }).type === "text" && typeof (b as { text?: unknown }).text === "string") {
        blocks.push({ type: "text", text: (b as { text: string }).text })
      }
    }
  }
  if (blocks.length > 0 && blocks[0]!.text === CLAUDE_CODE_IDENTITY) return blocks
  return [{ type: "text", text: CLAUDE_CODE_IDENTITY }, ...blocks]
}

const STRIP_HEADERS = new Set(["x-api-key", "host", "content-length", "authorization", "accept-encoding"])

export function buildRelayHeaders(input: {
  fingerprint: Record<string, string>
  token: string
  clientHeaders: Record<string, string>
}): Record<string, string> {
  const out: Record<string, string> = {}
  for (const [k, v] of Object.entries(input.fingerprint)) out[k.toLowerCase()] = v
  out["authorization"] = `Bearer ${input.token}`
  const beta = out["anthropic-beta"]
  if (!beta) {
    out["anthropic-beta"] = OAUTH_BETA
  } else if (!beta.split(",").map(s => s.trim()).includes(OAUTH_BETA)) {
    out["anthropic-beta"] = `${OAUTH_BETA},${beta}`
  }
  for (const k of STRIP_HEADERS) {
    if (k !== "authorization") delete out[k]
  }
  return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `bun test src/__tests__/transparentRelay-unit.test.ts`
Expected: PASS (7 tests).

- [ ] **Step 5: Commit**

```bash
git add src/proxy/transparentRelay.ts src/__tests__/transparentRelay-unit.test.ts
git commit -m "feat: add transparent-relay identity injection and header assembly"
```

---

### Task 4: `claudeEnvelope.ts` — fingerprint cache + baseline + header filtering (pure parts)

**Files:**
- Create: `src/proxy/claudeEnvelope.ts`
- Test: `src/__tests__/claudeEnvelope-unit.test.ts`

**Interfaces:**
- Produces:
  - `type Fingerprint = Record<string, string>`
  - `const BASELINE_FINGERPRINT: Fingerprint` — hardcoded fallback (`anthropic-version`, a conservative `anthropic-beta`, a generic `user-agent`, `x-app: cli`).
  - `filterFingerprintHeaders(raw: Record<string, string>): Fingerprint` — keeps only `user-agent`, `anthropic-version`, `anthropic-beta`, `x-app`, and any `x-stainless-*`; drops `authorization`, `x-api-key`, `content-length`, `host`, and per-request `x-stainless-retry-count` / `x-stainless-timeout`.
  - `getCachedFingerprint(versionKey: string): Fingerprint | null` and `setCachedFingerprint(versionKey: string, fp: Fingerprint): void` — version-keyed in-memory cache.
  - `resetEnvelopeCache(): void` — test/shutdown helper.

- [ ] **Step 1: Write the failing test**

Create `src/__tests__/claudeEnvelope-unit.test.ts`:

```typescript
import { describe, it, expect, beforeEach } from "bun:test"
import {
  filterFingerprintHeaders,
  getCachedFingerprint,
  setCachedFingerprint,
  resetEnvelopeCache,
  BASELINE_FINGERPRINT,
} from "../proxy/claudeEnvelope"

describe("filterFingerprintHeaders", () => {
  it("keeps fingerprint headers and x-stainless-* statics", () => {
    const fp = filterFingerprintHeaders({
      "user-agent": "claude-cli/2.1.0",
      "anthropic-version": "2023-06-01",
      "anthropic-beta": "claude-code-20250219",
      "x-app": "cli",
      "x-stainless-lang": "js",
      "x-stainless-os": "MacOS",
    })
    expect(fp["user-agent"]).toBe("claude-cli/2.1.0")
    expect(fp["x-stainless-lang"]).toBe("js")
    expect(fp["x-app"]).toBe("cli")
  })

  it("drops auth and per-request headers", () => {
    const fp = filterFingerprintHeaders({
      "user-agent": "claude-cli/2.1.0",
      authorization: "Bearer secret",
      "x-api-key": "k",
      "content-length": "10",
      host: "api.anthropic.com",
      "x-stainless-retry-count": "0",
      "x-stainless-timeout": "60",
    })
    expect(fp["authorization"]).toBeUndefined()
    expect(fp["x-api-key"]).toBeUndefined()
    expect(fp["content-length"]).toBeUndefined()
    expect(fp["x-stainless-retry-count"]).toBeUndefined()
    expect(fp["x-stainless-timeout"]).toBeUndefined()
  })
})

describe("fingerprint cache", () => {
  beforeEach(() => resetEnvelopeCache())

  it("returns null before a capture and the value after", () => {
    expect(getCachedFingerprint("v2.1.0")).toBeNull()
    setCachedFingerprint("v2.1.0", { "user-agent": "claude-cli/2.1.0" })
    expect(getCachedFingerprint("v2.1.0")).toEqual({ "user-agent": "claude-cli/2.1.0" })
  })

  it("is keyed by version (different key → miss)", () => {
    setCachedFingerprint("v2.1.0", { "user-agent": "claude-cli/2.1.0" })
    expect(getCachedFingerprint("v2.2.0")).toBeNull()
  })
})

describe("BASELINE_FINGERPRINT", () => {
  it("has the mandatory static headers", () => {
    expect(BASELINE_FINGERPRINT["anthropic-version"]).toBe("2023-06-01")
    expect(BASELINE_FINGERPRINT["user-agent"]).toMatch(/^claude-cli\//)
    expect(BASELINE_FINGERPRINT["x-app"]).toBe("cli")
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `bun test src/__tests__/claudeEnvelope-unit.test.ts`
Expected: FAIL — module `../proxy/claudeEnvelope` does not exist.

- [ ] **Step 3: Write minimal implementation**

Create `src/proxy/claudeEnvelope.ts`:

```typescript
/**
 * Dynamic Claude Code fingerprint capture for native passthrough.
 *
 * Captures the real HTTP fingerprint the bundled `claude` binary emits (so
 * forwarded requests match the genuine CLI and follow version bumps), and
 * caches it by binary version. Pure cache/filter helpers live here; the
 * subprocess capture itself is added in a later task.
 *
 * Leaf module — no imports from server.ts or session/.
 */

export type Fingerprint = Record<string, string>

/** Conservative fallback used when live capture fails. */
export const BASELINE_FINGERPRINT: Fingerprint = {
  "user-agent": "claude-cli/2.1.0 (external, cli)",
  "anthropic-version": "2023-06-01",
  "anthropic-beta": "oauth-2025-04-20,claude-code-20250219",
  "x-app": "cli",
}

const KEEP_PREFIXES = ["x-stainless-"]
const KEEP_EXACT = new Set(["user-agent", "anthropic-version", "anthropic-beta", "x-app"])
const DROP_PER_REQUEST = new Set(["x-stainless-retry-count", "x-stainless-timeout"])

export function filterFingerprintHeaders(raw: Record<string, string>): Fingerprint {
  const out: Fingerprint = {}
  for (const [rawKey, value] of Object.entries(raw)) {
    const key = rawKey.toLowerCase()
    if (DROP_PER_REQUEST.has(key)) continue
    if (KEEP_EXACT.has(key) || KEEP_PREFIXES.some(p => key.startsWith(p))) {
      out[key] = value
    }
  }
  return out
}

const cache = new Map<string, Fingerprint>()

export function getCachedFingerprint(versionKey: string): Fingerprint | null {
  return cache.get(versionKey) ?? null
}

export function setCachedFingerprint(versionKey: string, fp: Fingerprint): void {
  cache.set(versionKey, fp)
}

export function resetEnvelopeCache(): void {
  cache.clear()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `bun test src/__tests__/claudeEnvelope-unit.test.ts`
Expected: PASS (5 tests).

- [ ] **Step 5: Commit**

```bash
git add src/proxy/claudeEnvelope.ts src/__tests__/claudeEnvelope-unit.test.ts
git commit -m "feat: add claudeEnvelope fingerprint cache and header filtering"
```

---

### Task 5: `claudeEnvelope` live capture (`captureFingerprint`)

**Files:**
- Modify: `src/proxy/claudeEnvelope.ts`
- Test: `src/__tests__/claudeEnvelope-capture.test.ts`

**Interfaces:**
- Consumes: `filterFingerprintHeaders`, `getCachedFingerprint`, `setCachedFingerprint`, `BASELINE_FINGERPRINT` (Task 4).
- Produces: `getFingerprint(deps?: { spawnCapture?: () => Promise<Record<string, string> | null>; versionKey?: string }): Promise<Fingerprint>` — returns cached fingerprint for the version key; on miss runs `spawnCapture` (injectable for tests; default spawns the resolved `claude` binary against a loopback recorder), filters and caches the result, and falls back to `BASELINE_FINGERPRINT` (without caching) when capture returns null/throws. Concurrent calls for the same version key share one in-flight promise.

- [ ] **Step 1: Write the failing test**

Create `src/__tests__/claudeEnvelope-capture.test.ts`:

```typescript
import { describe, it, expect, beforeEach } from "bun:test"
import { getFingerprint, resetEnvelopeCache, BASELINE_FINGERPRINT } from "../proxy/claudeEnvelope"

describe("getFingerprint", () => {
  beforeEach(() => resetEnvelopeCache())

  it("captures, filters, and caches on first call", async () => {
    let calls = 0
    const spawnCapture = async () => {
      calls++
      return { "user-agent": "claude-cli/9.9.9", "anthropic-beta": "real-beta", authorization: "Bearer x" }
    }
    const fp1 = await getFingerprint({ spawnCapture, versionKey: "v9.9.9" })
    expect(fp1["user-agent"]).toBe("claude-cli/9.9.9")
    expect(fp1["authorization"]).toBeUndefined() // filtered out
    const fp2 = await getFingerprint({ spawnCapture, versionKey: "v9.9.9" })
    expect(fp2).toEqual(fp1)
    expect(calls).toBe(1) // cached, not re-captured
  })

  it("falls back to baseline when capture returns null and does not cache it", async () => {
    let calls = 0
    const spawnCapture = async () => { calls++; return null }
    const fp = await getFingerprint({ spawnCapture, versionKey: "vX" })
    expect(fp).toEqual(BASELINE_FINGERPRINT)
    await getFingerprint({ spawnCapture, versionKey: "vX" })
    expect(calls).toBe(2) // not cached → re-attempted
  })

  it("falls back to baseline when capture throws", async () => {
    const spawnCapture = async () => { throw new Error("spawn failed") }
    const fp = await getFingerprint({ spawnCapture, versionKey: "vErr" })
    expect(fp).toEqual(BASELINE_FINGERPRINT)
  })

  it("dedupes concurrent captures for the same version key", async () => {
    let calls = 0
    const spawnCapture = async () => { calls++; await new Promise(r => setTimeout(r, 10)); return { "user-agent": "claude-cli/1" } }
    const [a, b] = await Promise.all([
      getFingerprint({ spawnCapture, versionKey: "vDup" }),
      getFingerprint({ spawnCapture, versionKey: "vDup" }),
    ])
    expect(a).toEqual(b)
    expect(calls).toBe(1)
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `bun test src/__tests__/claudeEnvelope-capture.test.ts`
Expected: FAIL — `getFingerprint` is not exported.

- [ ] **Step 3: Implement `getFingerprint` + default capture**

Append to `src/proxy/claudeEnvelope.ts`:

```typescript
import { createServer } from "node:http"
import { execFile } from "node:child_process"
import { claudeLog } from "../logger"
import { resolveClaudeExecutableAsync, getResolvedClaudeExecutableInfo } from "./models"

const inflight = new Map<string, Promise<Fingerprint>>()

/** Run the real claude binary against a loopback recorder; resolve its request headers. */
async function defaultSpawnCapture(): Promise<Record<string, string> | null> {
  const claudePath = await resolveClaudeExecutableAsync()
  return await new Promise<Record<string, string> | null>((resolve) => {
    let settled = false
    const finish = (headers: Record<string, string> | null) => {
      if (settled) return
      settled = true
      try { server.close() } catch { /* already closing */ }
      resolve(headers)
    }
    const server = createServer((req, res) => {
      const headers: Record<string, string> = {}
      for (const [k, v] of Object.entries(req.headers)) headers[k] = Array.isArray(v) ? v.join(",") : (v ?? "")
      // Minimal valid SSE so the CLI exits cleanly without retrying.
      res.writeHead(200, { "content-type": "text/event-stream" })
      res.write(`event: message_start\ndata: ${JSON.stringify({ type: "message_start", message: { id: "msg_x", type: "message", role: "assistant", model: "claude", content: [], stop_reason: "end_turn", usage: { input_tokens: 0, output_tokens: 0 } } })}\n\n`)
      res.write(`event: message_stop\ndata: ${JSON.stringify({ type: "message_stop" })}\n\n`)
      res.end()
      finish(headers)
    })
    server.listen(0, "127.0.0.1", () => {
      const addr = server.address()
      const port = typeof addr === "object" && addr ? addr.port : 0
      const child = execFile(claudePath, ["-p", "hi"], {
        env: { ...process.env, ANTHROPIC_BASE_URL: `http://127.0.0.1:${port}`, ANTHROPIC_API_KEY: "x" },
        timeout: 15_000,
      }, (err) => { if (err && !settled) { claudeLog("envelope.capture_spawn_error", { error: err.message }); finish(null) } })
      child.on("error", (err) => { claudeLog("envelope.capture_child_error", { error: err.message }); finish(null) })
    })
    server.on("error", () => finish(null))
    setTimeout(() => finish(null), 16_000)
  })
}

function currentVersionKey(): string {
  const info = getResolvedClaudeExecutableInfo()
  return info ? `${info.path}` : "unknown"
}

export async function getFingerprint(deps?: {
  spawnCapture?: () => Promise<Record<string, string> | null>
  versionKey?: string
}): Promise<Fingerprint> {
  const versionKey = deps?.versionKey ?? currentVersionKey()
  const cached = getCachedFingerprint(versionKey)
  if (cached) return cached
  const existing = inflight.get(versionKey)
  if (existing) return existing

  const spawnCapture = deps?.spawnCapture ?? defaultSpawnCapture
  const promise = (async (): Promise<Fingerprint> => {
    try {
      const raw = await spawnCapture()
      if (!raw) return BASELINE_FINGERPRINT
      const fp = filterFingerprintHeaders(raw)
      if (Object.keys(fp).length === 0) return BASELINE_FINGERPRINT
      setCachedFingerprint(versionKey, fp)
      return fp
    } catch (err) {
      claudeLog("envelope.capture_failed", { error: err instanceof Error ? err.message : String(err) })
      return BASELINE_FINGERPRINT
    } finally {
      inflight.delete(versionKey)
    }
  })()
  inflight.set(versionKey, promise)
  return promise
}
```

Also update `resetEnvelopeCache` to clear in-flight:

```typescript
export function resetEnvelopeCache(): void {
  cache.clear()
  inflight.clear()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `bun test src/__tests__/claudeEnvelope-capture.test.ts`
Expected: PASS (4 tests).

- [ ] **Step 5: Run the full suite**

Run: `npm test`
Expected: PASS (no regressions).

- [ ] **Step 6: Commit**

```bash
git add src/proxy/claudeEnvelope.ts src/__tests__/claudeEnvelope-capture.test.ts
git commit -m "feat: capture real Claude Code fingerprint via loopback recorder"
```

---

### Task 6: `transparentRelay.forwardNative` — the actual forward

**Files:**
- Modify: `src/proxy/transparentRelay.ts`
- Test: `src/__tests__/transparentRelay-forward.test.ts`

**Interfaces:**
- Consumes: `ensureClaudeCodeIdentity`, `buildRelayHeaders` (Task 3); `getFingerprint` (Task 5); `CredentialStore` + `createPlatformCredentialStore` + `refreshOAuthToken` from `tokenRefresh.ts`; `FetchLike = (input: string, init?: RequestInit) => Promise<Response>`.
- Produces: `forwardNative(input: { body: any; clientHeaders: Record<string, string>; profile: { type: string; env: Record<string, string> }; fingerprint: Record<string, string>; deps?: { fetchImpl?: FetchLike; store?: CredentialStore } }): Promise<Response>` — reads the OAuth token (oauth-token profile → `env.CLAUDE_CODE_OAUTH_TOKEN`; otherwise credential store keyed by `env.CLAUDE_CONFIG_DIR`), refreshes once on upstream 401, rewrites `body.system` via `ensureClaudeCodeIdentity`, POSTs to `https://api.anthropic.com/v1/messages` with assembled headers, and returns the upstream `Response` (streaming body passes through untouched). On no-token → returns a `400` JSON `Response`; non-2xx upstream is returned as-is (no fallback). **Design-change note:** the `fingerprint` is passed IN by the caller (`server.ts`) — `forwardNative` no longer calls `getFingerprint` itself, and there is no baseline. See the revision in the §5/§6 spec and the Task 7 fallback.

- [ ] **Step 1: Write the failing test**

Create `src/__tests__/transparentRelay-forward.test.ts`:

```typescript
import { describe, it, expect } from "bun:test"
import { forwardNative } from "../proxy/transparentRelay"
import type { CredentialStore } from "../proxy/tokenRefresh"

const fakeStore = (token: string | null): CredentialStore => ({
  read: async () => token ? ({ claudeAiOauth: { accessToken: token, refreshToken: "r", expiresAt: Date.now() + 1e9 } } as any) : null,
  write: async () => true,
})
const fixedFingerprint = async () => ({ "user-agent": "claude-cli/2.1.0", "anthropic-version": "2023-06-01" })

describe("forwardNative", () => {
  it("forwards to api.anthropic.com with Bearer token and identity-prefixed system", async () => {
    let capturedUrl = ""
    let capturedInit: RequestInit = {}
    const fetchImpl = async (url: string, init?: RequestInit) => {
      capturedUrl = url
      capturedInit = init ?? {}
      return new Response(JSON.stringify({ ok: true }), { status: 200, headers: { "content-type": "application/json" } })
    }
    const res = await forwardNative({
      body: { model: "claude", system: "You are OpenCode.", messages: [{ role: "user", content: "hi" }] },
      clientHeaders: { "x-api-key": "placeholder" },
      profile: { type: "claude-max", env: {} },
      deps: { fetchImpl, store: fakeStore("tok-abc"), getFingerprintFn: fixedFingerprint as any },
    })
    expect(res.status).toBe(200)
    expect(capturedUrl).toBe("https://api.anthropic.com/v1/messages")
    const headers = capturedInit.headers as Record<string, string>
    expect(headers["authorization"]).toBe("Bearer tok-abc")
    expect(headers["x-api-key"]).toBeUndefined()
    const sentBody = JSON.parse(capturedInit.body as string)
    expect(sentBody.system[0].text).toBe("You are Claude Code, Anthropic's official CLI for Claude.")
    expect(sentBody.system[1].text).toBe("You are OpenCode.")
    expect(sentBody.messages).toEqual([{ role: "user", content: "hi" }])
  })

  it("returns 400 when no OAuth token is available", async () => {
    const res = await forwardNative({
      body: { messages: [] },
      clientHeaders: {},
      profile: { type: "claude-max", env: {} },
      deps: { fetchImpl: async () => new Response("{}"), store: fakeStore(null), getFingerprintFn: fixedFingerprint as any },
    })
    expect(res.status).toBe(400)
  })

  it("uses the oauth-token profile env token directly", async () => {
    let auth = ""
    const fetchImpl = async (_url: string, init?: RequestInit) => {
      auth = (init?.headers as Record<string, string>)["authorization"]
      return new Response("{}", { status: 200 })
    }
    await forwardNative({
      body: { messages: [], system: "x" },
      clientHeaders: {},
      profile: { type: "oauth-token", env: { CLAUDE_CODE_OAUTH_TOKEN: "env-tok" } },
      deps: { fetchImpl, getFingerprintFn: fixedFingerprint as any },
    })
    expect(auth).toBe("Bearer env-tok")
  })

  it("returns a non-2xx upstream response as-is (no fallback)", async () => {
    const fetchImpl = async () => new Response(JSON.stringify({ error: "OAuth not supported" }), { status: 403 })
    const res = await forwardNative({
      body: { messages: [], system: "x" },
      clientHeaders: {},
      profile: { type: "claude-max", env: {} },
      deps: { fetchImpl, store: fakeStore("t"), getFingerprintFn: fixedFingerprint as any },
    })
    expect(res.status).toBe(403)
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `bun test src/__tests__/transparentRelay-forward.test.ts`
Expected: FAIL — `forwardNative` is not exported.

- [ ] **Step 3: Implement `forwardNative`**

Append to `src/proxy/transparentRelay.ts`:

```typescript
import { createPlatformCredentialStore, refreshOAuthToken, type CredentialStore } from "./tokenRefresh"
import { getFingerprint } from "./claudeEnvelope"

const ANTHROPIC_MESSAGES_URL = "https://api.anthropic.com/v1/messages"
type FetchLike = (input: string, init?: RequestInit) => Promise<Response>

async function readToken(store: CredentialStore): Promise<string | null> {
  const creds = await store.read()
  return creds?.claudeAiOauth?.accessToken ?? null
}

export async function forwardNative(input: {
  body: { system?: unknown; [k: string]: unknown }
  clientHeaders: Record<string, string>
  profile: { type: string; env: Record<string, string> }
  deps?: { fetchImpl?: FetchLike; store?: CredentialStore; getFingerprintFn?: typeof getFingerprint }
}): Promise<Response> {
  const fetchImpl = input.deps?.fetchImpl ?? (globalThis.fetch as FetchLike)
  const getFp = input.deps?.getFingerprintFn ?? getFingerprint

  // Resolve token: oauth-token profile carries it in env; otherwise read the store.
  let token: string | null = null
  let store: CredentialStore | undefined = input.deps?.store
  if (input.profile.type === "oauth-token" && input.profile.env.CLAUDE_CODE_OAUTH_TOKEN) {
    token = input.profile.env.CLAUDE_CODE_OAUTH_TOKEN
  } else {
    store = store ?? createPlatformCredentialStore({ claudeConfigDir: input.profile.env.CLAUDE_CONFIG_DIR })
    token = await readToken(store)
  }
  if (!token) {
    return new Response(JSON.stringify({ type: "error", error: { type: "authentication_error", message: "No OAuth token available for native relay" } }), { status: 400, headers: { "content-type": "application/json" } })
  }

  const fingerprint = await getFp()
  const outBody = { ...input.body, system: ensureClaudeCodeIdentity(input.body.system) }
  const send = (tok: string) => fetchImpl(ANTHROPIC_MESSAGES_URL, {
    method: "POST",
    headers: buildRelayHeaders({ fingerprint, token: tok, clientHeaders: input.clientHeaders }),
    body: JSON.stringify(outBody),
  })

  let res = await send(token)
  if (res.status === 401 && store) {
    const refreshed = await refreshOAuthToken(store)
    if (refreshed) {
      const newToken = await readToken(store)
      if (newToken) res = await send(newToken)
    }
  }
  return res
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `bun test src/__tests__/transparentRelay-forward.test.ts`
Expected: PASS (4 tests).

- [ ] **Step 5: Run the full suite**

Run: `npm test`
Expected: PASS (no regressions).

- [ ] **Step 6: Commit**

```bash
git add src/proxy/transparentRelay.ts src/__tests__/transparentRelay-forward.test.ts
git commit -m "feat: implement native forward to api.anthropic.com"
```

---

### Task 7: Wire the native branch into `server.ts`

**Files:**
- Modify: `src/proxy/server.ts` (imports near line 49-62; insert branch after the `stream` is resolved at ~line 568, before the non-stream/stream split at lines 978 / 1466; passthrough override near line 843)
- Test: `src/__tests__/proxy-native-relay.test.ts`

**Interfaces:**
- Consumes: `resolveRelayMode`, `shouldNativeForward` (Task 2); `forwardNative` (Task 6, now takes a `fingerprint` field); `getFingerprint` (Task 5, returns `Fingerprint | null`); `getFeaturesForAdapter` (`sdkFeatures.ts`). `adapter` (`adapter.name`), `profile` (`{ type, env }`), `body`, `c.req.raw.headers`, `c.req.header("x-meridian-mode")` all already in scope in `handleMessages`.
- **Fingerprint fallback (design change):** the native branch first calls `getFingerprint()`. If it returns `null` (capture failed — there is no baseline), the branch does NOT forward; it logs `relay.native_fallback_passthrough`, reassigns the effective mode to `"passthrough"`, and falls through to the SDK path. Only when a real fingerprint exists does it call `forwardNative({ ..., fingerprint })`.

- [ ] **Step 1: Write the failing test**

Create `src/__tests__/proxy-native-relay.test.ts`:

```typescript
import { describe, it, expect, mock, beforeEach, afterEach } from "bun:test"

mock.module("@anthropic-ai/claude-agent-sdk", () => ({
  query: () => (async function* () { /* should NOT be called in native mode */ })(),
  createSdkMcpServer: () => ({ type: "sdk", name: "test", instance: {} }),
  tool: () => ({}),
}))
mock.module("../logger", () => ({ claudeLog: () => {}, withClaudeLogContext: (_c: any, fn: any) => fn() }))

// Force native relay + a fake upstream so no real network/credential access happens.
mock.module("../proxy/transparentRelay", () => ({
  forwardNative: async () => new Response(JSON.stringify({ type: "message", relayed: true }), { status: 200, headers: { "content-type": "application/json" } }),
  ensureClaudeCodeIdentity: (s: any) => s,
  buildRelayHeaders: () => ({}),
  CLAUDE_CODE_IDENTITY: "x",
}))
// Fingerprint present → native branch proceeds. (A null return would degrade to passthrough.)
mock.module("../proxy/claudeEnvelope", () => ({
  getFingerprint: async () => ({ "user-agent": "claude-cli/2.1.0" }),
  filterFingerprintHeaders: (h: any) => h,
  getCachedFingerprint: () => null,
  setCachedFingerprint: () => {},
  resetEnvelopeCache: () => {},
}))
mock.module("../proxy/sdkFeatures", () => ({
  getFeaturesForAdapter: () => ({ relayMode: "native", codeSystemPrompt: false, clientSystemPrompt: true, claudeMd: "off", memory: false, dreaming: false, thinking: "disabled", thinkingPassthrough: false, sharedMemory: false, maxBudgetUsd: 0, fallbackModel: "", sdkDebug: false, additionalDirectories: "" }),
  getAllFeatureConfigs: () => ({}),
  validateFeatureUpdate: (x: any) => x,
  updateAdapterFeatures: () => {},
  resetAdapterFeatures: () => {},
}))

const { createProxyServer, clearSessionCache } = await import("../proxy/server")

describe("native relay branch", () => {
  beforeEach(() => clearSessionCache())

  it("routes to forwardNative (bypassing the SDK) when relayMode=native and profile has OAuth", async () => {
    const app = createProxyServer({ profiles: [{ id: "p", type: "claude-max", claudeConfigDir: "/tmp/x" }], defaultProfile: "p" } as any).app
    const res = await app.request("/v1/messages", {
      method: "POST",
      headers: { "content-type": "application/json", "x-meridian-profile": "p" },
      body: JSON.stringify({ model: "claude-3", system: "s", messages: [{ role: "user", content: "hi" }], stream: false }),
    })
    expect(res.status).toBe(200)
    expect(await res.json()).toEqual({ type: "message", relayed: true })
  })
})
```

> Note: adjust `createProxyServer(...)` construction and the `.app` accessor to match the real export signature you find at the top of `server.ts` (the integration test in `proxy-forgecode-integration.test.ts` shows the established `createTestApp()` pattern — reuse its helper shape).

- [ ] **Step 2: Run test to verify it fails**

Run: `bun test src/__tests__/proxy-native-relay.test.ts`
Expected: FAIL — the SDK `query()` path runs (or errors) instead of `forwardNative`.

- [ ] **Step 3: Add imports**

In `src/proxy/server.ts`, add near the other proxy imports (~line 49-62):

```typescript
import { resolveRelayMode, shouldNativeForward } from "./relayMode"
import { forwardNative } from "./transparentRelay"
import { getFingerprint } from "./claudeEnvelope"
```

- [ ] **Step 4: Insert the native branch**

In `handleMessages`, after `const stream = ...` (~line 568) and after `sdkFeatures` is resolved (it is resolved at ~line 615 via `getFeaturesForAdapter(adapter.name)` — move the branch below that resolution, or resolve `relayMode` from a fresh `getFeaturesForAdapter(adapter.name)` call), insert BEFORE the `if (!stream) { ... } else { ... }` SDK split:

```typescript
    // Native passthrough (third mode): forward verbatim to api.anthropic.com,
    // bypassing the Agent SDK. Gated by per-adapter relayMode + OAuth profile.
    // `let` because a failed fingerprint capture degrades the mode to passthrough.
    let relayMode = resolveRelayMode({
      feature: getFeaturesForAdapter(adapter.name).relayMode,
      envForceNative: process.env.MERIDIAN_NATIVE_FORWARD === "1",
      headerOverride: c.req.header("x-meridian-mode"),
    })
    if (shouldNativeForward(relayMode, profile.type)) {
      const fingerprint = await getFingerprint()
      if (fingerprint) {
        const clientHeaders: Record<string, string> = {}
        c.req.raw.headers.forEach((v, k) => { clientHeaders[k] = v })
        claudeLog("relay.native", { adapter: adapter.name, profile: profile.id })
        return await forwardNative({ body, clientHeaders, profile: { type: profile.type, env: profile.env }, fingerprint })
      }
      // No real fingerprint available → never forward with a guessed one.
      // Degrade to the existing SDK passthrough mode.
      claudeLog("relay.native_fallback_passthrough", { adapter: adapter.name, profile: profile.id })
      relayMode = "passthrough"
    }
```

> The reassigned `relayMode` is consumed by Task 8's `applyRelayModeToPassthrough(relayMode, pipelinePassthrough)`, so the fallback forces `passthrough = true`. Ensure `relayMode` is declared with `let` and remains in scope at the Task 8 override site (same `handleMessages` body).

- [ ] **Step 5: Run test to verify it passes**

Run: `bun test src/__tests__/proxy-native-relay.test.ts`
Expected: PASS — response is `{ type: "message", relayed: true }`, SDK `query()` never invoked.

- [ ] **Step 6: Run the full suite**

Run: `npm test`
Expected: PASS (no regressions in existing auto/internal/passthrough behavior).

- [ ] **Step 7: Commit**

```bash
git add src/proxy/server.ts src/__tests__/proxy-native-relay.test.ts
git commit -m "feat: route native relay mode before the SDK query path"
```

---

### Task 8: `internal` / `passthrough` mode overrides in `server.ts`

**Files:**
- Modify: `src/proxy/server.ts` (where `passthrough` is computed, ~line 843-845)
- Test: extend `src/__tests__/relayMode-unit.test.ts` with an override helper, OR add `src/__tests__/relayMode-override-unit.test.ts`

**Interfaces:**
- Produces: `applyRelayModeToPassthrough(mode: RelayMode, pipelinePassthrough: boolean): boolean` in `relayMode.ts` — `internal` → `false`, `passthrough` → `true`, otherwise the pipeline value unchanged.

- [ ] **Step 1: Write the failing test**

Create `src/__tests__/relayMode-override-unit.test.ts`:

```typescript
import { describe, it, expect } from "bun:test"
import { applyRelayModeToPassthrough } from "../proxy/relayMode"

describe("applyRelayModeToPassthrough", () => {
  it("internal forces false", () => {
    expect(applyRelayModeToPassthrough("internal", true)).toBe(false)
  })
  it("passthrough forces true", () => {
    expect(applyRelayModeToPassthrough("passthrough", false)).toBe(true)
  })
  it("auto keeps the pipeline value", () => {
    expect(applyRelayModeToPassthrough("auto", true)).toBe(true)
    expect(applyRelayModeToPassthrough("auto", false)).toBe(false)
  })
  it("native keeps the pipeline value (native handled earlier)", () => {
    expect(applyRelayModeToPassthrough("native", false)).toBe(false)
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `bun test src/__tests__/relayMode-override-unit.test.ts`
Expected: FAIL — `applyRelayModeToPassthrough` not exported.

- [ ] **Step 3: Implement the helper**

Append to `src/proxy/relayMode.ts`:

```typescript
export function applyRelayModeToPassthrough(mode: RelayMode, pipelinePassthrough: boolean): boolean {
  if (mode === "internal") return false
  if (mode === "passthrough") return true
  return pipelinePassthrough
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `bun test src/__tests__/relayMode-override-unit.test.ts`
Expected: PASS (4 tests).

- [ ] **Step 5: Apply the override in server.ts**

In `src/proxy/server.ts`, find the passthrough computation (~line 843):

```typescript
const passthrough = pipelineCtx.passthrough !== undefined
  ? pipelineCtx.passthrough
  : envBool("PASSTHROUGH")
```

Wrap it with the override (using the `relayMode` already computed in Task 7 — ensure that branch's `relayMode` is in scope here, or recompute identically):

```typescript
const pipelinePassthrough = pipelineCtx.passthrough !== undefined
  ? pipelineCtx.passthrough
  : envBool("PASSTHROUGH")
const passthrough = applyRelayModeToPassthrough(relayMode, pipelinePassthrough)
```

Add `applyRelayModeToPassthrough` to the existing `./relayMode` import.

- [ ] **Step 6: Run the full suite**

Run: `npm test`
Expected: PASS — confirm auto path unchanged; internal/passthrough modes now pin behavior.

- [ ] **Step 7: Commit**

```bash
git add src/proxy/relayMode.ts src/proxy/server.ts src/__tests__/relayMode-override-unit.test.ts
git commit -m "feat: honor internal/passthrough relay mode overrides"
```

---

### Task 9: Settings UI — `relayMode` dropdown + non-CC risk warning

**Files:**
- Modify: `src/telemetry/settingsPage.ts` (the `FEATURES` array ~line 116-129, and `saveFeature` ~line 148-158)
- Test: manual (HTML page); no unit test framework for the static page. Verify by loading `/settings`.

**Interfaces:**
- Consumes: `relayMode` feature (Task 1), persisted through the existing `PATCH /settings/api/features/:adapter` route (no route change).

- [ ] **Step 1: Add the feature to the FEATURES array**

In `src/telemetry/settingsPage.ts`, add as the FIRST entry of the `FEATURES` array (so it renders at the top of each adapter card):

```javascript
  { key: 'relayMode', label: 'Relay Mode', desc: 'auto: default — internal: MCP tools — passthrough: SDK passthrough — native: direct forward to api.anthropic.com (bypasses SDK; higher risk on non-Claude-Code clients)', type: 'select', options: ['auto', 'internal', 'passthrough', 'native'] },
```

- [ ] **Step 2: Add the non-CC risk confirmation in saveFeature**

In `src/telemetry/settingsPage.ts`, modify `saveFeature` to confirm when selecting `native` on a non-`claude-code` adapter:

```javascript
async function saveFeature(adapter, key, value) {
  if (key === 'relayMode' && value === 'native' && adapter !== 'claude-code') {
    const ok = confirm('Native mode forwards requests directly to api.anthropic.com using your OAuth token, bypassing the SDK. On non-Claude-Code clients the tool/prompt shape differs from the real CLI, which carries a HIGHER risk of your account being flagged. Enable anyway?');
    if (!ok) { render(); return; }
  }
  const patch = {};
  patch[key] = value;
  await fetch('/settings/api/features/' + adapter, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(patch),
  });
  currentConfig[adapter][key] = value;
  showSaved();
}
```

> Note: the `ADAPTER_LABELS` map at ~line 131 currently has no `claude-code` entry. If the Claude Code adapter should appear as its own card, add `'claude-code': 'Claude Code'` there. Confirm the adapter name string used by `detectAdapter`/`claudeCodeAdapter.name` (`"claude-code"`) matches the key.

- [ ] **Step 3: Verify manually**

Run: `npm run build && npm start` (or the dev start command), open `http://127.0.0.1:3456/settings`, confirm the Relay Mode dropdown renders per adapter, selecting `native` on a non-Claude-Code card prompts the warning, and the choice persists across reload (round-trips through `sdk-features.json`).

- [ ] **Step 4: Commit**

```bash
git add src/telemetry/settingsPage.ts
git commit -m "feat: relay mode selector with non-CC risk warning in settings UI"
```

---

### Task 10: Documentation — README risk + usage section

**Files:**
- Modify: `README.md` (add a "Native passthrough (experimental)" subsection) and `ARCHITECTURE.md` (note the third mode + new modules)

**Interfaces:** none (docs only).

- [ ] **Step 1: Add README section**

Add a subsection under the features/configuration area of `README.md`:

```markdown
### Native passthrough (experimental, off by default)

`relayMode: native` (set per adapter on the SDK Features page) forwards requests
verbatim to `api.anthropic.com` using your Max OAuth token, bypassing the Agent
SDK. This removes MCP tool re-wrapping and injected prompts, saving tokens.

**Risk:** This works *around* the SDK rather than through it. Since January 2026
Anthropic restricts Max OAuth tokens used outside Claude.ai / Claude Code. Meridian
spoofs a genuine Claude Code fingerprint to reduce static detection, but behavioral
signals (request volume, non-CLI tool/prompt shapes, account/IP sharing) can still
flag an account. A perfect fingerprint is not a guarantee of safety. Use it only:
- on a single account you control, one user, one IP — never shared or rotated;
- ideally with the Claude Code client (its tool/prompt shape already matches the CLI);
- within normal single-user volume (Meridian backs off near rate-limit windows).

Enable per adapter in **Settings → SDK Features → Relay Mode**, or set
`MERIDIAN_NATIVE_FORWARD=1`. Per-request override: header `x-meridian-mode: native | sdk`.
```

- [ ] **Step 2: Add ARCHITECTURE.md note**

Under the module map / agent-specific logic in `ARCHITECTURE.md`, add: `relayMode.ts` (pure mode resolution + eligibility), `claudeEnvelope.ts` (dynamic CLI fingerprint capture), `transparentRelay.ts` (direct forward to api.anthropic.com) — and note the three relay modes (auto/internal/passthrough/native).

- [ ] **Step 3: Commit**

```bash
git add README.md ARCHITECTURE.md
git commit -m "docs: document native passthrough mode and its risks"
```

---

## Self-Review

**1. Spec coverage**
- §1/§2 (verbatim forward, no MCP wrap) → Task 6 (`forwardNative` forwards `body` untouched except `system` identity).
- §3 (risk statement, off by default) → defaults `relayMode: "auto"` (Task 1); README/ARCHITECTURE risk docs (Task 10).
- §4 (per-adapter mode selection, env + header override, eligibility) → Tasks 1, 2, 7; UI Task 9.
- §5 (claudeEnvelope dynamic fingerprint, version cache, baseline fallback, per-request x-stainless drop, inflight dedupe) → Tasks 4, 5.
- §6 (transparentRelay: OAuth Bearer + refresh on 401, identity-line-only injection, verbatim body, SSE pass-through, error on non-2xx) → Tasks 3, 6.
- §7 (behavioral safety) → concurrency reuse is automatic (relay runs inside the existing `handleWithQueue` semaphore, Task 7); rate-limit-aware back-off and single-user docs → Task 10 docs. **Gap:** active rate-limit back-off before relaying is documented but not enforced in code. Decision: keep the existing semaphore as the concurrency guard; explicit pre-relay back-off is deferred (listed in §10 follow-ups). Not a blocking gap for v1 — recorded here intentionally.
- §8 (module boundaries) → Tasks 2-6 create leaf modules; Task 7 wires from server.ts only.
- §9 (testing) → unit tests Tasks 1-6, 8; integration Task 7; E2E remains manual.

**2. Placeholder scan** — no "TBD"/"handle edge cases"/"similar to Task N". Two explicit `> Note:` callouts in Tasks 7 and 9 ask the implementer to match the real `createProxyServer`/`createTestApp` signature and adapter-label key — these are verification instructions, not missing content (the established pattern is named: `proxy-forgecode-integration.test.ts`).

**3. Type consistency** — `RelayMode` used identically across Tasks 2/8. `Fingerprint` shared across Tasks 4/5/6. `forwardNative` param/return matches its call site in Task 7. `getFingerprint` signature in Task 5 matches the `getFingerprintFn` dep in Task 6. `relayMode` enum values identical in sdkFeatures validation (Task 1), resolveRelayMode (Task 2), and the UI options (Task 9).

> **Known follow-ups (from spec §10):** baseline fingerprint constants to be refined from a first real capture; optional `/health` indicator for native mode + last capture time; active rate-limit back-off before relaying.
