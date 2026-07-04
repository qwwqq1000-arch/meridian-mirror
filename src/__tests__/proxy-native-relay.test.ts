import { describe, it, expect, mock, beforeAll, afterAll, beforeEach, afterEach } from "bun:test"
import { createServer, type Server } from "node:http"

// Track whether the SDK was invoked (proves native bypassed it, or that a
// degraded/rejected request fell through to the SDK). Declared before mock blocks
// so the factory closures capture the initialized binding.
let sdkInvoked = false

mock.module("@anthropic-ai/claude-agent-sdk", () => ({
  query: () => { sdkInvoked = true; return (async function* () {})() },
  createSdkMcpServer: () => ({ type: "sdk", name: "t", instance: {} }),
  tool: () => ({}),
}))
mock.module("../logger", () => ({ claudeLog: () => {}, withClaudeLogContext: (_c: unknown, fn: () => unknown) => fn() }))

// ---------------------------------------------------------------------------
// Fake sidecar — a real HTTP server so no mock.module leaks to other test files
// ---------------------------------------------------------------------------

/** Toggle: when true the fake sidecar responds with X-Degrade:1 */
let degradeNext = false

let fakeSidecar: Server
let fakeSidecarPort: number

function startFakeSidecar(): Promise<void> {
  return new Promise((resolve) => {
    fakeSidecar = createServer((_req, res) => {
      if (degradeNext) {
        res.writeHead(200, {
          "Content-Type": "application/json",
          "X-Degrade": "1",
          "X-Degrade-Reason": "upstream_429",
        })
        res.end(JSON.stringify({}))
      } else {
        res.writeHead(200, { "Content-Type": "application/json" })
        res.end(JSON.stringify({ relayed: true }))
      }
    })
    fakeSidecar.listen(0, "127.0.0.1", () => {
      const addr = fakeSidecar.address()
      fakeSidecarPort = typeof addr === "object" && addr !== null ? addr.port : 0
      resolve()
    })
  })
}

function stopFakeSidecar(): Promise<void> {
  return new Promise((resolve, reject) => {
    fakeSidecar.close((err) => (err ? reject(err) : resolve()))
  })
}

beforeAll(async () => {
  await startFakeSidecar()
})

afterAll(async () => {
  await stopFakeSidecar()
})

// ---------------------------------------------------------------------------
// Import the server AFTER mocks are registered
// ---------------------------------------------------------------------------

const { createProxyServer, clearSessionCache } = await import("../proxy/server")
const { setSetting } = await import("../proxy/settings")

// ---------------------------------------------------------------------------
// Test fixtures
// ---------------------------------------------------------------------------

const CC_IDENTITY = "You are Claude Code, Anthropic's official CLI for Claude."
const ccShapedBody = {
  model: "claude-3",
  system: [{ type: "text", text: `${CC_IDENTITY}\n\nYou are an interactive CLI tool.` }],
  tools: [{ name: "Bash" }, { name: "Read" }, { name: "Edit" }, { name: "Write" }],
  messages: [{ role: "user", content: "hi" }],
  stream: false,
}

function makeApp() {
  return createProxyServer({
    profiles: [{ id: "p", type: "claude-max", claudeConfigDir: "/tmp/meridian-native-test-nocreds" }],
    defaultProfile: "p",
  } as Parameters<typeof createProxyServer>[0]).app
}

// ---------------------------------------------------------------------------
// Env bookkeeping
// ---------------------------------------------------------------------------

let savedForwardEnv: string | undefined
let savedUrlEnv: string | undefined

describe("native relay branch (Go sidecar delegation)", () => {
  beforeEach(() => {
    clearSessionCache()
    sdkInvoked = false
    degradeNext = false

    savedForwardEnv = process.env.MERIDIAN_NATIVE_FORWARD
    savedUrlEnv = process.env.MERIDIAN_NATIVE_EGRESS_URL

    process.env.MERIDIAN_NATIVE_FORWARD = "1"
    process.env.MERIDIAN_NATIVE_EGRESS_URL = `http://127.0.0.1:${fakeSidecarPort}`
  })

  afterEach(() => {
    if (savedForwardEnv === undefined) delete process.env.MERIDIAN_NATIVE_FORWARD
    else process.env.MERIDIAN_NATIVE_FORWARD = savedForwardEnv

    if (savedUrlEnv === undefined) delete process.env.MERIDIAN_NATIVE_EGRESS_URL
    else process.env.MERIDIAN_NATIVE_EGRESS_URL = savedUrlEnv
  })

  it("(a) not-degraded: returns native response, SDK not invoked", async () => {
    degradeNext = false
    const res = await makeApp().fetch(new Request("http://localhost/v1/messages", {
      method: "POST",
      headers: { "content-type": "application/json", "x-meridian-profile": "p", "user-agent": "claude-cli/2.1.0" },
      body: JSON.stringify(ccShapedBody),
    }))
    expect(res.status).toBe(200)
    const j = await res.json() as { relayed?: boolean }
    expect(j.relayed).toBe(true)
    expect(sdkInvoked).toBe(false)
  })

  it("(b) degraded (X-Degrade:1): SDK invoked (falls through to SDK path)", async () => {
    degradeNext = true
    const res = await makeApp().fetch(new Request("http://localhost/v1/messages", {
      method: "POST",
      headers: { "content-type": "application/json", "x-meridian-profile": "p", "user-agent": "claude-cli/2.1.0" },
      body: JSON.stringify(ccShapedBody),
    }))
    expect(sdkInvoked).toBe(true)
    const j = await res.json().catch(() => ({})) as { relayed?: boolean }
    expect(j.relayed).toBeUndefined()
  })

  it("(c) sidecar unavailable (bad port): SDK invoked, not native response", async () => {
    // Point at a port that is guaranteed to refuse connections
    process.env.MERIDIAN_NATIVE_EGRESS_URL = "http://127.0.0.1:1"
    const res = await makeApp().fetch(new Request("http://localhost/v1/messages", {
      method: "POST",
      headers: { "content-type": "application/json", "x-meridian-profile": "p", "user-agent": "claude-cli/2.1.0" },
      body: JSON.stringify(ccShapedBody),
    }))
    expect(sdkInvoked).toBe(true)
    const j = await res.json().catch(() => ({})) as { relayed?: boolean }
    expect(j.relayed).toBeUndefined()
  })

  it("(d) non-CC body: anti-forgery rejects native, SDK invoked", async () => {
    // Body does NOT look like Claude Code (OpenCode-shaped: wrong system text, lowercase tool names)
    const nonCcBody = {
      model: "claude-3",
      system: [{ type: "text", text: "You are OpenCode." }],
      tools: [{ name: "read" }, { name: "write" }, { name: "bash" }, { name: "edit" }],
      messages: [{ role: "user", content: "hi" }],
      stream: false,
    }
    const res = await makeApp().fetch(new Request("http://localhost/v1/messages", {
      method: "POST",
      headers: { "content-type": "application/json", "x-meridian-profile": "p", "user-agent": "claude-cli/2.1.0" },
      body: JSON.stringify(nonCcBody),
    }))
    expect(sdkInvoked).toBe(true)
    const j = await res.json().catch(() => ({})) as { relayed?: boolean }
    expect(j.relayed).toBeUndefined()
  })

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
})
