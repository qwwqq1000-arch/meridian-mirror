/**
 * Integration tests for GET/PATCH /settings/api/native.
 */
import { describe, it, expect, beforeEach, afterEach, mock } from "bun:test"
import { setSetting } from "../proxy/settings"

mock.module("@anthropic-ai/claude-agent-sdk", () => ({
  query: () => (async function* () {})(),
  createSdkMcpServer: () => ({ type: "sdk", name: "t", instance: {} }),
  tool: () => ({}),
}))
mock.module("../logger", () => ({ claudeLog: () => {}, withClaudeLogContext: (_c: unknown, fn: () => unknown) => fn() }))

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
    setSetting("nativeForward", true)
    setSetting("nativeBodyCheck", false)
  })

  afterEach(() => {
    setSetting("nativeForward", true)
    setSetting("nativeBodyCheck", false)
  })

  it("GET returns defaults when settings are at default", async () => {
    const app = makeApp()
    const res = await app.fetch(new Request("http://localhost/settings/api/native"))
    expect(res.status).toBe(200)
    const body = await res.json() as { nativeForward: boolean; nativeBodyCheck: boolean }
    expect(body.nativeForward).toBe(true)
    expect(body.nativeBodyCheck).toBe(false)
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
