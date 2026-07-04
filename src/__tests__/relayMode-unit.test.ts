import { describe, it, expect } from "bun:test"
import { nativeEligible } from "../proxy/relayMode"

const base = { featureNativeForward: false, envForceNative: false, clientForcedSdk: false, profileType: "claude-max" as const }

describe("nativeEligible", () => {
  it("true when the adapter toggle is on and the profile has OAuth", () => {
    expect(nativeEligible({ ...base, featureNativeForward: true })).toBe(true)
  })

  it("true when the env escape hatch is set", () => {
    expect(nativeEligible({ ...base, envForceNative: true })).toBe(true)
  })

  it("true for an oauth-token profile", () => {
    expect(nativeEligible({ ...base, featureNativeForward: true, profileType: "oauth-token" })).toBe(true)
  })

  it("false when neither toggle nor env is set (default off)", () => {
    expect(nativeEligible(base)).toBe(false)
  })

  it("false for an api profile even when enabled (no usable OAuth token)", () => {
    expect(nativeEligible({ ...base, featureNativeForward: true, profileType: "api" })).toBe(false)
  })

  it("client x-meridian-mode:sdk forces it OFF even when enabled server-side", () => {
    expect(nativeEligible({ ...base, featureNativeForward: true, clientForcedSdk: true })).toBe(false)
    expect(nativeEligible({ ...base, envForceNative: true, clientForcedSdk: true })).toBe(false)
  })
})
