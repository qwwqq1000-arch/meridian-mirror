# Global Native-Forwarding Toggle + Relay Status in Telemetry Logs

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a global (cross-adapter) native-forwarding toggle to settings.ts + a settings API, wire it into the server's eligibility check, surface relay decisions in the dashboard Logs tab, and update the settings UI to show a global section while removing per-adapter native toggles.

**Architecture:** Two enhancements: (1) `MeridianSettings` gains `nativeForward` + `nativeBodyCheck` booleans persisted via existing `setSetting`/`getSetting`; a new `/settings/api/native` GET+PATCH route in server.ts reads/writes them. The eligibility check `nativeEligible(...)` is widened so the global setting ORs with the per-adapter setting. (2) After every native path decision in server.ts, a `diagnosticLog.session(...)` call emits a structured line so it appears in the dashboard Logs tab.

**Tech Stack:** TypeScript, Bun test runner, Hono (HTTP framework used in server.ts), no new dependencies.

## Global Constraints

- NO `as any`, `@ts-ignore`, or `@ts-expect-error` — TypeScript must be clean
- `npm run typecheck` must pass; `npm test` must be 0 failures
- Stay on branch `feat/native-go-egress` — no git checkout/switch/branch/reset
- Only `git add` + `git commit` (no push); NO AI/Co-Authored-By trailer in commits
- Leaf modules (`settings.ts`, `relayMode.ts`, `errors.ts`, `models.ts`, `tools.ts`, `messages.ts`) must NOT import from `server.ts` or `session/`
- `nativeForward` and `nativeBodyCheck` fields in `AdapterFeatures` (`sdkFeatures.ts`) must be KEPT for back-compat — do not remove them
- All new test files go in `src/__tests__/`
- Commit message format: `type: brief description` (no AI attribution lines)

---

### Task 1: Extend MeridianSettings with global native fields

**Files:**
- Modify: `src/proxy/settings.ts` (add two optional fields to `MeridianSettings`)
- Modify: `src/__tests__/settings-unit.test.ts` (add tests for the new fields)

**Interfaces:**
- Produces: `MeridianSettings.nativeForward?: boolean`, `MeridianSettings.nativeBodyCheck?: boolean` — both optional, undefined means use default (false / true respectively)

- [ ] **Step 1: Add the two fields to `MeridianSettings`**

In `src/proxy/settings.ts`, change the interface from:
```typescript
export interface MeridianSettings {
  /** Last active profile ID — restored on proxy startup */
  activeProfile?: string
}
```
to:
```typescript
export interface MeridianSettings {
  /** Last active profile ID — restored on proxy startup */
  activeProfile?: string
  /**
   * Global native-forwarding toggle. When true, ALL adapters that route to an
   * OAuth-capable profile will forward requests verbatim to api.anthropic.com,
   * bypassing the SDK. Per-adapter sdkFeatures.nativeForward still ORs with
   * this as an additional enable path. Default: false (off).
   */
  nativeForward?: boolean
  /**
   * Global anti-forge gate for native forwarding. When false, the body-shape
   * check is skipped globally. Default: true (gate ON — safe default).
   */
  nativeBodyCheck?: boolean
}
```

- [ ] **Step 2: Add tests for the new fields to `src/__tests__/settings-unit.test.ts`**

Append a new describe block at the end of the file (before the last `}`):
```typescript
describe("MeridianSettings new fields — nativeForward, nativeBodyCheck", () => {
  test("nativeForward defaults to undefined (treated as false)", () => {
    // The interface is typed as optional; absent key means feature is off.
    const s: import("../proxy/settings").MeridianSettings = {}
    expect(s.nativeForward).toBeUndefined()
    // Absent = falsy = disabled
    expect(s.nativeForward === true).toBe(false)
  })

  test("nativeBodyCheck defaults to undefined (treated as true / gate ON)", () => {
    const s: import("../proxy/settings").MeridianSettings = {}
    expect(s.nativeBodyCheck).toBeUndefined()
    // The consumer uses `getSetting("nativeBodyCheck") !== false`, so undefined ≠ false → gate stays ON
    expect(s.nativeBodyCheck !== false).toBe(true)
  })

  test("JSON roundtrip preserves nativeForward=true", () => {
    const data = { activeProfile: "work", nativeForward: true, nativeBodyCheck: false }
    const serialized = JSON.stringify(data, null, 2)
    const parsed = JSON.parse(serialized) as import("../proxy/settings").MeridianSettings
    expect(parsed.nativeForward).toBe(true)
    expect(parsed.nativeBodyCheck).toBe(false)
  })
})
```

- [ ] **Step 3: Run the test to verify it passes**

```bash
cd /Users/leo/meridian轻量版/new-meridian && npx bun test src/__tests__/settings-unit.test.ts
```
Expected: All tests pass (including the 3 new ones)

- [ ] **Step 4: Commit**

```bash
cd /Users/leo/meridian轻量版/new-meridian && git add src/proxy/settings.ts src/__tests__/settings-unit.test.ts && git commit -m "feat: add nativeForward + nativeBodyCheck to MeridianSettings"
```

---

### Task 2: Add GET/PATCH /settings/api/native routes in server.ts

**Files:**
- Modify: `src/proxy/server.ts` — add two routes after the existing `/settings/api/features` block (around line 2574)

**Interfaces:**
- Consumes: `getSetting`, `setSetting` from `./settings` (already imported in server.ts via `require`)
- Produces: `GET /settings/api/native` → `{ nativeForward: boolean, nativeBodyCheck: boolean }`; `PATCH /settings/api/native` → `{ ok: true }` or `{ error: string }` (400)

- [ ] **Step 1: Check how settings is imported in server.ts**

```bash
grep -n "require.*settings\|from.*settings" /Users/leo/meridian轻量版/new-meridian/src/proxy/server.ts | head -20
```

- [ ] **Step 2: Add the two routes after the `app.delete("/settings/api/features/:adapter"...)` block**

Find this block in `src/proxy/server.ts` (around line 2569-2574):
```typescript
  app.delete("/settings/api/features/:adapter", (c) => {
    const { resetAdapterFeatures } = require("./sdkFeatures") as typeof import("./sdkFeatures")
    const adapter = c.req.param("adapter")
    resetAdapterFeatures(adapter)
    return c.json({ ok: true })
  })
```

Insert immediately after it (before the `// Prometheus metrics` comment):
```typescript
  // Global native-forwarding settings
  app.get("/settings/api/native", (c) => {
    const { getSetting } = require("./settings") as typeof import("./settings")
    return c.json({
      nativeForward: getSetting("nativeForward") === true,
      nativeBodyCheck: getSetting("nativeBodyCheck") !== false,
    })
  })
  app.patch("/settings/api/native", async (c) => {
    const { setSetting } = require("./settings") as typeof import("./settings")
    let body: unknown
    try {
      body = await c.req.json()
    } catch {
      return c.json({ error: "invalid JSON body" }, 400)
    }
    if (body === null || typeof body !== "object" || Array.isArray(body)) {
      return c.json({ error: "body must be a JSON object" }, 400)
    }
    const input = body as Record<string, unknown>
    if ("nativeForward" in input) {
      if (typeof input.nativeForward !== "boolean") {
        return c.json({ error: "nativeForward must be a boolean" }, 400)
      }
      setSetting("nativeForward", input.nativeForward)
    }
    if ("nativeBodyCheck" in input) {
      if (typeof input.nativeBodyCheck !== "boolean") {
        return c.json({ error: "nativeBodyCheck must be a boolean" }, 400)
      }
      setSetting("nativeBodyCheck", input.nativeBodyCheck)
    }
    return c.json({ ok: true })
  })
```

- [ ] **Step 3: Run typecheck to confirm types are clean**

```bash
cd /Users/leo/meridian轻量版/new-meridian && npm run typecheck 2>&1 | tail -10
```
Expected: 0 errors

- [ ] **Step 4: Commit**

```bash
cd /Users/leo/meridian轻量版/new-meridian && git add src/proxy/server.ts && git commit -m "feat: add GET/PATCH /settings/api/native routes"
```

---

### Task 3: Wire global setting into server.ts native eligibility and body-check gate

**Files:**
- Modify: `src/proxy/server.ts` — change the `nativeEligible` call and the body-check condition (lines 848-858)

**Interfaces:**
- Consumes: `getSetting` from `./settings` (already available via require pattern)
- The `nativeEligible` call's `featureNativeForward` parameter changes to: `getSetting("nativeForward") === true || sdkFeatures.nativeForward`
- The body-check condition changes to: `getSetting("nativeBodyCheck") !== false && sdkFeatures.nativeBodyCheck`

**NOTE on semantics:**
- `featureNativeForward`: `true` if EITHER global OR per-adapter is enabled (OR logic)
- `nativeBodyCheck` gate in body: `false` (skip check) only if BOTH global is explicitly false AND per-adapter is false. Actually per the spec: global is authoritative → use `getSetting("nativeBodyCheck") !== false` (default ON) and ignore per-adapter in the condition.

Wait — re-read spec: "the body-check gate should use the global setting: `bodyCheck = getSetting("nativeBodyCheck") !== false` (default ON) — i.e. global authoritative, default true." So the gate becomes purely the global setting. Per-adapter nativeBodyCheck kept in sdkFeatures for back-compat but NOT used in the gate anymore.

- [ ] **Step 1: Find the exact lines to change**

```bash
grep -n "nativeEligible\|nativeBodyCheck" /Users/leo/meridian轻量版/new-meridian/src/proxy/server.ts
```

- [ ] **Step 2: Update the `nativeEligible` call**

Find (around line 848):
```typescript
      if (nativeEligible({
        featureNativeForward: sdkFeatures.nativeForward,
        envForceNative: process.env.MERIDIAN_NATIVE_FORWARD === "1",
        clientForcedSdk: c.req.header("x-meridian-mode") === "sdk",
        profileType: profile.type,
      })) {
```

Change to:
```typescript
      const { getSetting: getNativeSetting } = require("./settings") as typeof import("./settings")
      if (nativeEligible({
        featureNativeForward: getNativeSetting("nativeForward") === true || sdkFeatures.nativeForward,
        envForceNative: process.env.MERIDIAN_NATIVE_FORWARD === "1",
        clientForcedSdk: c.req.header("x-meridian-mode") === "sdk",
        profileType: profile.type,
      })) {
```

- [ ] **Step 3: Update the body-check gate**

Find (around line 858):
```typescript
        if (!sdkFeatures.nativeBodyCheck || isClaudeCodeShaped(body)) {
```

Change to (global authoritative, default ON):
```typescript
        const globalBodyCheck = getNativeSetting("nativeBodyCheck") !== false
        if (!globalBodyCheck || isClaudeCodeShaped(body)) {
```

- [ ] **Step 4: Run typecheck**

```bash
cd /Users/leo/meridian轻量版/new-meridian && npm run typecheck 2>&1 | tail -10
```
Expected: 0 errors

- [ ] **Step 5: Run the native relay tests to confirm existing tests still pass**

```bash
cd /Users/leo/meridian轻量版/new-meridian && npx bun test src/__tests__/proxy-native-relay.test.ts --timeout 30000
```
Expected: All 4 tests pass (they use `MERIDIAN_NATIVE_FORWARD=1` env which still feeds `envForceNative`)

- [ ] **Step 6: Commit**

```bash
cd /Users/leo/meridian轻量版/new-meridian && git add src/proxy/server.ts && git commit -m "feat: global nativeForward + nativeBodyCheck settings drive eligibility"
```

---

### Task 4: Add diagnosticLog.session relay status lines in server.ts native branch

**Files:**
- Modify: `src/proxy/server.ts` — three insertion points in the native branch

**Interfaces:**
- Consumes: `diagnosticLog.session(msg: string, requestId: string)` — already called at lines 704, 714, 2305 in server.ts. Pattern: `diagnosticLog.session(\`${requestMeta.requestId} ...\`, requestMeta.requestId)`
- The variable in scope in the native branch is `requestMeta.requestId` (confirmed by existing code at line 875 where `requestMeta.requestId` is used)

**Three insertion points:**

1. **Native success** (after line 903 `claudeLog("relay.native", ...)`):
   ```typescript
   diagnosticLog.session(`${requestMeta.requestId} relay=native`, requestMeta.requestId)
   ```

2. **Degrade (sidecar unavailable, line 862)** and **degrade (r.degraded, line 873)**: two separate insertion points, each immediately after the existing `claudeLog("relay.native_degrade", ...)` call.
   - After sidecar_unavailable claudeLog:
     ```typescript
     diagnosticLog.session(`${requestMeta.requestId} relay=degrade:sidecar_unavailable`, requestMeta.requestId)
     ```
   - After r.degraded claudeLog:
     ```typescript
     diagnosticLog.session(`${requestMeta.requestId} relay=degrade:${r.reason ?? "unknown"}`, requestMeta.requestId)
     ```

3. **Anti-forge reject** (after line 934 `claudeLog("relay.native_reject_noncc_shape", ...)`):
   ```typescript
   diagnosticLog.session(`${requestMeta.requestId} relay=reject:noncc`, requestMeta.requestId)
   ```

- [ ] **Step 1: Confirm diagnosticLog is in scope in the native branch**

```bash
grep -n "diagnosticLog\b" /Users/leo/meridian轻量版/new-meridian/src/proxy/server.ts | head -10
```
Should show `diagnosticLog` is defined/imported near the top of server.ts

- [ ] **Step 2: Insert relay=native log after native success claudeLog**

In `src/proxy/server.ts`, find:
```typescript
              nativeCb.recordSuccess()
              claudeLog("relay.native", { adapter: adapter.name, profile: profile.id })
              telemetryStore.record({
```

Change to:
```typescript
              nativeCb.recordSuccess()
              claudeLog("relay.native", { adapter: adapter.name, profile: profile.id })
              diagnosticLog.session(`${requestMeta.requestId} relay=native`, requestMeta.requestId)
              telemetryStore.record({
```

- [ ] **Step 3: Insert relay=degrade:sidecar_unavailable after first claudeLog in the sidecar-unavailable branch**

Find:
```typescript
          if (nativeBaseUrl === null || nativeCb.isOpen(Date.now())) {
            // Sidecar unavailable or circuit open — degrade to SDK path
            claudeLog("relay.native_degrade", { reason: "sidecar_unavailable", adapter: adapter.name, profile: profile.id })
          } else {
```

Change to:
```typescript
          if (nativeBaseUrl === null || nativeCb.isOpen(Date.now())) {
            // Sidecar unavailable or circuit open — degrade to SDK path
            claudeLog("relay.native_degrade", { reason: "sidecar_unavailable", adapter: adapter.name, profile: profile.id })
            diagnosticLog.session(`${requestMeta.requestId} relay=degrade:sidecar_unavailable`, requestMeta.requestId)
          } else {
```

- [ ] **Step 4: Insert relay=degrade log after the r.degraded claudeLog**

Find:
```typescript
            if (r.degraded) {
              nativeCb.recordFailure(Date.now())
              claudeLog("relay.native_degrade", { reason: r.reason ?? "unknown", adapter: adapter.name, profile: profile.id })
              telemetryStore.record({
```

Change to:
```typescript
            if (r.degraded) {
              nativeCb.recordFailure(Date.now())
              claudeLog("relay.native_degrade", { reason: r.reason ?? "unknown", adapter: adapter.name, profile: profile.id })
              diagnosticLog.session(`${requestMeta.requestId} relay=degrade:${r.reason ?? "unknown"}`, requestMeta.requestId)
              telemetryStore.record({
```

- [ ] **Step 5: Insert relay=reject:noncc after anti-forge claudeLog**

Find:
```typescript
          claudeLog("relay.native_reject_noncc_shape", { adapter: adapter.name, profile: profile.id })
```

Change to:
```typescript
          claudeLog("relay.native_reject_noncc_shape", { adapter: adapter.name, profile: profile.id })
          diagnosticLog.session(`${requestMeta.requestId} relay=reject:noncc`, requestMeta.requestId)
```

- [ ] **Step 6: Run typecheck**

```bash
cd /Users/leo/meridian轻量版/new-meridian && npm run typecheck 2>&1 | tail -10
```
Expected: 0 errors

- [ ] **Step 7: Run native relay tests**

```bash
cd /Users/leo/meridian轻量版/new-meridian && npx bun test src/__tests__/proxy-native-relay.test.ts --timeout 30000
```
Expected: All 4 pass

- [ ] **Step 8: Commit**

```bash
cd /Users/leo/meridian轻量版/new-meridian && git add src/proxy/server.ts && git commit -m "feat: emit diagnosticLog.session relay status in native branch"
```

---

### Task 5: Update settingsPage.ts UI — global section + remove per-adapter native entries

**Files:**
- Modify: `src/telemetry/settingsPage.ts`

**Changes:**
1. Remove `nativeForward` and `nativeBodyCheck` from the `FEATURES` array (lines 117-118)
2. Add a global "Native Forwarding" section at the TOP of `<div id="adapters">` — rendered by a new JS function `renderGlobalNativeSection()` that is called at the start of `render()`. It fetches from `GET /settings/api/native` and PATCHes to `PATCH /settings/api/native`.

**UI layout for the global section:**
```html
<div class="adapter-card" id="global-native-card" style="border-color: var(--accent); margin-bottom: 24px;">
  <div class="adapter-header">
    <span class="adapter-name">Native Forwarding <span style="font-size:11px;padding:2px 8px;border-radius:10px;background:rgba(210,153,34,0.15);color:var(--yellow);vertical-align:middle;margin-left:8px">Global</span></span>
  </div>
  <p style="font-size:12px;color:var(--muted);margin-bottom:12px">...</p>
  <div class="feature-grid">
    ... two toggle rows ...
  </div>
</div>
```

- [ ] **Step 1: Remove nativeForward and nativeBodyCheck from the FEATURES array**

In `src/telemetry/settingsPage.ts`, find and remove these two lines from the `FEATURES` array:
```javascript
  { key: 'nativeForward', label: 'Native Forwarding', desc: 'Forward requests routed to this adapter VERBATIM to api.anthropic.com using your OAuth token, bypassing the SDK (no tool re-wrapping, no injected prompts). Intended for the Claude Code adapter — higher risk on non-CC adapters.', type: 'toggle' },
  { key: 'nativeBodyCheck', label: 'Native: Anti-Forge Body Check', desc: 'Only forward natively when the request body genuinely looks like Claude Code (CC identity + CC tools). Blocks a client that spoofed CC detection headers from spending your OAuth token / risking the account. Keep ON.', type: 'toggle' },
```

- [ ] **Step 2: Add global native state variable and loadGlobalNative function**

After the `let currentConfig = {};` line, add:
```javascript
let globalNative = { nativeForward: false, nativeBodyCheck: true };

async function loadGlobalNative() {
  const res = await fetch('/settings/api/native');
  globalNative = await res.json();
  renderGlobalNativeSection();
}
```

- [ ] **Step 3: Add saveGlobalNative function**

After `loadGlobalNative`, add:
```javascript
async function saveGlobalNative(key, value) {
  if (key === 'nativeForward' && value === true) {
    const ok = confirm('Native forwarding sends requests DIRECTLY to api.anthropic.com using your OAuth token, bypassing the SDK. This carries account risk — enabling it means all adapters with OAuth-capable profiles will bypass the SDK. Enable anyway?');
    if (!ok) { renderGlobalNativeSection(); return; }
  }
  const patch = {};
  patch[key] = value;
  await fetch('/settings/api/native', {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(patch),
  });
  globalNative[key] = value;
  showSaved();
}
```

- [ ] **Step 4: Add renderGlobalNativeSection function**

After `saveGlobalNative`, add:
```javascript
function renderGlobalNativeSection() {
  const existing = document.getElementById('global-native-card');
  if (existing) existing.remove();
  const container = document.getElementById('adapters');
  const card = document.createElement('div');
  card.id = 'global-native-card';
  card.className = 'adapter-card';
  card.style.cssText = 'border-color: var(--accent); margin-bottom: 24px;';
  card.innerHTML =
    '<div class="adapter-header">' +
      '<span class="adapter-name">Native Forwarding ' +
        '<span style="font-size:11px;padding:2px 8px;border-radius:10px;background:rgba(210,153,34,0.15);color:var(--yellow);vertical-align:middle;margin-left:8px">Global</span>' +
      '</span>' +
    '</div>' +
    '<p style="font-size:12px;color:var(--muted);margin-bottom:12px">' +
      'Global override: applies to ALL adapters. When enabled, requests routed to OAuth-capable profiles bypass the Agent SDK and are forwarded verbatim to api.anthropic.com. ' +
      'Per-adapter nativeForward in the cards below still works as an additional enable.' +
    '</p>' +
    '<div class="feature-grid">' +
      '<div class="feature-row">' +
        '<div class="feature-info">' +
          '<span class="feature-label">Native Forwarding</span>' +
          '<span class="feature-desc">Bypass SDK globally — forward all OAuth-capable requests verbatim to api.anthropic.com</span>' +
        '</div>' +
        '<label class="toggle"><input type="checkbox" id="global-nativeForward" ' + (globalNative.nativeForward ? 'checked' : '') +
        ' onchange="saveGlobalNative(\'nativeForward\', this.checked)"><span class="toggle-track"></span></label>' +
      '</div>' +
      '<div class="feature-row">' +
        '<div class="feature-info">' +
          '<span class="feature-label">Anti-Forge Body Check</span>' +
          '<span class="feature-desc">Only forward natively when the body looks like genuine Claude Code (CC identity + tools). Keep ON.</span>' +
        '</div>' +
        '<label class="toggle"><input type="checkbox" id="global-nativeBodyCheck" ' + (globalNative.nativeBodyCheck ? 'checked' : '') +
        ' onchange="saveGlobalNative(\'nativeBodyCheck\', this.checked)"><span class="toggle-track"></span></label>' +
      '</div>' +
    '</div>';
  container.insertBefore(card, container.firstChild);
}
```

- [ ] **Step 5: Call renderGlobalNativeSection at start of render() and load global native in loadConfig()**

Find the `render()` function:
```javascript
function render() {
  const container = document.getElementById('adapters');
  container.innerHTML = '';
```

Change to:
```javascript
function render() {
  const container = document.getElementById('adapters');
  container.innerHTML = '';
  renderGlobalNativeSection();
```

Find `loadConfig()`:
```javascript
async function loadConfig() {
  const res = await fetch('/settings/api/features');
  currentConfig = await res.json();
  render();
}
```

Change to:
```javascript
async function loadConfig() {
  const res = await fetch('/settings/api/features');
  currentConfig = await res.json();
  render();
}
```

And change the call at the bottom from `loadConfig();` to:
```javascript
loadConfig();
loadGlobalNative();
```

- [ ] **Step 6: Run typecheck**

```bash
cd /Users/leo/meridian轻量版/new-meridian && npm run typecheck 2>&1 | tail -10
```
Expected: 0 errors

- [ ] **Step 7: Run the full test suite**

```bash
cd /Users/leo/meridian轻量版/new-meridian && npm test 2>&1 | tail -20
```
Expected: 0 failures

- [ ] **Step 8: Commit**

```bash
cd /Users/leo/meridian轻量版/new-meridian && git add src/telemetry/settingsPage.ts && git commit -m "feat: global native section in settings UI, remove per-adapter native toggles"
```

---

### Task 6: Add tests for global native toggle enabling native + settings API route

**Files:**
- Modify: `src/__tests__/proxy-native-relay.test.ts` — add a test that global `getSetting("nativeForward") === true` enables native (without `MERIDIAN_NATIVE_FORWARD` env)
- Create: `src/__tests__/proxy-settings-native-api.test.ts` — integration test for the new `/settings/api/native` GET + PATCH routes

**Note on test isolation:** `setSetting("nativeForward", ...)` writes to `~/.config/meridian/settings.json`. To avoid polluting real settings, tests should save/restore the setting, or mock the settings module. Since `settings.ts` is a leaf module that reads from disk on each call (no in-process cache to invalidate), the cleanest approach is to set the env `MERIDIAN_NATIVE_FORWARD=1` for enabling (which already works) and rely on the existing tests. For the new global path test, use `setSetting` + restore in afterEach.

- [ ] **Step 1: Add a test to proxy-native-relay.test.ts for the global setting path**

In `src/__tests__/proxy-native-relay.test.ts`, add the following imports at top of file (after existing imports):
```typescript
import { setSetting, getSetting } from "../proxy/settings"
```

Add the following test inside the existing `describe("native relay branch (Go sidecar delegation)", ...)` block, after test `(d)`:
```typescript
  it("(e) global nativeForward=true enables native (no env var)", async () => {
    // Remove the env var that existing tests rely on, use global setting instead
    delete process.env.MERIDIAN_NATIVE_FORWARD
    setSetting("nativeForward", true)
    try {
      const res = await makeApp().fetch(new Request("http://localhost/v1/messages", {
        method: "POST",
        headers: { "content-type": "application/json", "x-meridian-profile": "p", "user-agent": "claude-cli/2.1.0" },
        body: JSON.stringify(ccShapedBody),
      }))
      expect(res.status).toBe(200)
      const j = await res.json() as { relayed?: boolean }
      expect(j.relayed).toBe(true)
      expect(sdkInvoked).toBe(false)
    } finally {
      setSetting("nativeForward", false)
      process.env.MERIDIAN_NATIVE_FORWARD = "1"  // restore for other tests in the suite
    }
  })
```

- [ ] **Step 2: Create proxy-settings-native-api.test.ts**

Create `src/__tests__/proxy-settings-native-api.test.ts`:
```typescript
/**
 * Integration tests for GET/PATCH /settings/api/native.
 */
import { describe, it, expect, beforeEach, afterEach } from "bun:test"
import { setSetting } from "../proxy/settings"

mock.module("@anthropic-ai/claude-agent-sdk", () => ({
  query: () => (async function* () {})(),
  createSdkMcpServer: () => ({ type: "sdk", name: "t", instance: {} }),
  tool: () => ({}),
}))
mock.module("../logger", () => ({ claudeLog: () => {}, withClaudeLogContext: (_c: unknown, fn: () => unknown) => fn() }))

import { mock } from "bun:test"
const { createProxyServer } = await import("../proxy/server")

function makeApp() {
  return createProxyServer({
    profiles: [{ id: "p", type: "claude-max", claudeConfigDir: "/tmp/meridian-native-api-test" }],
    defaultProfile: "p",
  } as Parameters<typeof createProxyServer>[0]).app
}

describe("GET/PATCH /settings/api/native", () => {
  beforeEach(() => {
    // Reset to safe defaults before each test
    setSetting("nativeForward", false)
    setSetting("nativeBodyCheck", true)
  })

  afterEach(() => {
    setSetting("nativeForward", false)
    setSetting("nativeBodyCheck", true)
  })

  it("GET returns defaults when settings are at default", async () => {
    const app = makeApp()
    const res = await app.fetch(new Request("http://localhost/settings/api/native"))
    expect(res.status).toBe(200)
    const body = await res.json() as { nativeForward: boolean; nativeBodyCheck: boolean }
    expect(body.nativeForward).toBe(false)
    expect(body.nativeBodyCheck).toBe(true)
  })

  it("PATCH nativeForward=true persists and GET reflects it", async () => {
    const app = makeApp()
    const patch = await app.fetch(new Request("http://localhost/settings/api/native", {
      method: "PATCH",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ nativeForward: true }),
    }))
    expect(patch.status).toBe(200)
    const patchBody = await patch.json() as { ok: boolean }
    expect(patchBody.ok).toBe(true)

    const get = await app.fetch(new Request("http://localhost/settings/api/native"))
    const getBody = await get.json() as { nativeForward: boolean; nativeBodyCheck: boolean }
    expect(getBody.nativeForward).toBe(true)
  })

  it("PATCH nativeBodyCheck=false persists", async () => {
    const app = makeApp()
    await app.fetch(new Request("http://localhost/settings/api/native", {
      method: "PATCH",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ nativeBodyCheck: false }),
    }))
    const get = await app.fetch(new Request("http://localhost/settings/api/native"))
    const body = await get.json() as { nativeForward: boolean; nativeBodyCheck: boolean }
    expect(body.nativeBodyCheck).toBe(false)
  })

  it("PATCH with non-boolean returns 400", async () => {
    const app = makeApp()
    const res = await app.fetch(new Request("http://localhost/settings/api/native", {
      method: "PATCH",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ nativeForward: "yes" }),
    }))
    expect(res.status).toBe(400)
    const body = await res.json() as { error: string }
    expect(body.error).toContain("boolean")
  })

  it("PATCH with non-object body returns 400", async () => {
    const app = makeApp()
    const res = await app.fetch(new Request("http://localhost/settings/api/native", {
      method: "PATCH",
      headers: { "content-type": "application/json" },
      body: JSON.stringify([1, 2, 3]),
    }))
    expect(res.status).toBe(400)
  })
})
```

- [ ] **Step 3: Run both new test files**

```bash
cd /Users/leo/meridian轻量版/new-meridian && npx bun test src/__tests__/proxy-settings-native-api.test.ts src/__tests__/proxy-native-relay.test.ts --timeout 30000
```
Expected: All tests pass (including new test `(e)`)

- [ ] **Step 4: Run the full suite + typecheck**

```bash
cd /Users/leo/meridian轻量版/new-meridian && npm run typecheck 2>&1 | tail -5 && npm test 2>&1 | tail -10
```
Expected: 0 type errors, 0 test failures

- [ ] **Step 5: Commit**

```bash
cd /Users/leo/meridian轻量版/new-meridian && git add src/__tests__/proxy-settings-native-api.test.ts src/__tests__/proxy-native-relay.test.ts && git commit -m "test: global native toggle + /settings/api/native route coverage"
```

---

### Task 7: Squash into single commit + write report

**Files:**
- Create: `/Users/leo/meridian轻量版/new-meridian/.superpowers/sdd/global-toggle-report.md`

- [ ] **Step 1: Final typecheck + full test run**

```bash
cd /Users/leo/meridian轻量版/new-meridian && npm run typecheck 2>&1 | tail -5
cd /Users/leo/meridian轻量版/new-meridian && npm test 2>&1 | tail -15
```
Capture the totals.

- [ ] **Step 2: Squash all task commits into the required single commit**

```bash
cd /Users/leo/meridian轻量版/new-meridian && git log --oneline -10
```
Count how many commits since the branch base. Then:
```bash
git rebase -i HEAD~<N>
```
In the interactive editor, keep the first as `pick`, mark all others as `squash` (`s`). Edit the combined message to:
```
feat: global native-forwarding toggle + relay status in telemetry logs
```

- [ ] **Step 3: Write report**

Create `/Users/leo/meridian轻量版/new-meridian/.superpowers/sdd/global-toggle-report.md` with:
- Settings fields added: `nativeForward?: boolean`, `nativeBodyCheck?: boolean` in `MeridianSettings`
- API routes added: `GET /settings/api/native`, `PATCH /settings/api/native`
- Server eligibility change: `featureNativeForward` now ORs global + per-adapter; body check uses global setting
- UI changes: global "Native Forwarding" section added at top; `nativeForward` and `nativeBodyCheck` removed from per-adapter `FEATURES` array
- `diagnosticLog.session` lines added: native success, two degrade paths, anti-forge reject
- Typecheck total: `Found 0 errors.`
- Test total: `X pass, 0 fail`
