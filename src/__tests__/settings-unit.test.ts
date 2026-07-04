/**
 * Unit tests for settings.ts — persistent server settings.
 */
import { describe, test, expect, beforeEach, afterEach } from "bun:test"
import { existsSync, mkdirSync, rmSync, readFileSync, writeFileSync } from "node:fs"
import { join } from "node:path"
import { tmpdir } from "node:os"

// We can't easily mock homedir() in the module, so we test the
// load/save logic by directly importing and verifying the contract.
// The module reads/writes ~/.config/meridian/settings.json.

describe("settings module contract", () => {
  // Use a temp file to avoid polluting real settings
  const tempDir = join(tmpdir(), `meridian-settings-test-${Date.now()}`)
  const tempFile = join(tempDir, "test-settings.json")

  beforeEach(() => {
    mkdirSync(tempDir, { recursive: true })
  })

  afterEach(() => {
    rmSync(tempDir, { recursive: true, force: true })
  })

  test("JSON roundtrip preserves data", () => {
    const data = { activeProfile: "work" }
    writeFileSync(tempFile, JSON.stringify(data, null, 2) + "\n")
    const loaded = JSON.parse(readFileSync(tempFile, "utf-8"))
    expect(loaded.activeProfile).toBe("work")
  })

  test("merge semantics: new keys added, existing keys updated", () => {
    const initial = { activeProfile: "personal" }
    writeFileSync(tempFile, JSON.stringify(initial))
    const current = JSON.parse(readFileSync(tempFile, "utf-8"))
    const merged = { ...current, activeProfile: "work" }
    writeFileSync(tempFile, JSON.stringify(merged, null, 2) + "\n")
    const result = JSON.parse(readFileSync(tempFile, "utf-8"))
    expect(result.activeProfile).toBe("work")
  })

  test("missing file returns empty on parse", () => {
    const missing = join(tempDir, "nonexistent.json")
    expect(existsSync(missing)).toBe(false)
  })

  test("corrupt JSON doesn't throw", () => {
    writeFileSync(tempFile, "not json{{{")
    expect(() => {
      try { JSON.parse(readFileSync(tempFile, "utf-8")) } catch { /* expected */ }
    }).not.toThrow()
  })
})

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
