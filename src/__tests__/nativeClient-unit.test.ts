import { describe, it, expect } from "bun:test"
import { forwardToNative } from "../proxy/nativeClient"

describe("forwardToNative", () => {
  it("returns degraded when Go responds X-Degrade:1", async () => {
    const fetchImpl = async () => new Response("", { status: 200, headers: { "X-Degrade": "1", "X-Degrade-Reason": "no_fingerprint" } })
    const r = await forwardToNative({ baseUrl: "http://127.0.0.1:9", rawBody: "{}", profile: { configDir: "/c", account: "a" }, stream: false, fetchImpl })
    expect(r.degraded).toBe(true)
    expect(r.reason).toBe("no_fingerprint")
    expect(r.connectionFailed).toBeFalsy() // sidecar responded → must NOT trip the breaker
  })
  it("returns the response when not degraded", async () => {
    const fetchImpl = async () => new Response(JSON.stringify({ ok: true }), { status: 200 })
    const r = await forwardToNative({ baseUrl: "http://127.0.0.1:9", rawBody: "{}", profile: { configDir: "/c", account: "a" }, stream: false, fetchImpl })
    expect(r.degraded).toBe(false)
    expect(r.response?.status).toBe(200)
  })
  it("degrades on connection error", async () => {
    const fetchImpl = async () => { throw new Error("ECONNREFUSED") }
    const r = await forwardToNative({ baseUrl: "http://127.0.0.1:9", rawBody: "{}", profile: { configDir: "/c", account: "a" }, stream: false, fetchImpl })
    expect(r.degraded).toBe(true)
    expect(r.connectionFailed).toBe(true) // sidecar unreachable → trips the breaker
  })
  it("sends the rawBody verbatim as the POST body with metadata in headers", async () => {
    let sentBody: unknown, sentHeaders: Record<string, string> = {}
    const raw = '{"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"a<b>","signature":"S=="}]}]}'
    const fetchImpl = async (_u: string, init?: RequestInit) => {
      sentBody = init?.body
      sentHeaders = (init?.headers ?? {}) as Record<string, string>
      return new Response("", { status: 200 })
    }
    await forwardToNative({ baseUrl: "http://127.0.0.1:9", rawBody: raw, profile: { configDir: "/c", account: "acct" }, stream: true, anthropicBeta: "structured-outputs-2025-12-15", fetchImpl })
    expect(sentBody).toBe(raw) // verbatim — not re-serialized
    expect(sentHeaders["x-native-account"]).toBe("acct")
    expect(sentHeaders["x-native-config-dir"]).toBe("/c")
    expect(sentHeaders["x-native-stream"]).toBe("1")
    expect(sentHeaders["x-native-anthropic-beta"]).toBe("structured-outputs-2025-12-15")
  })
})
