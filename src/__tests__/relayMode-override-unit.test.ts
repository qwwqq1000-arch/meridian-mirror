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
