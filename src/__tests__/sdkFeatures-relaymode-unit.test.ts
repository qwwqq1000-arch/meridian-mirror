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
