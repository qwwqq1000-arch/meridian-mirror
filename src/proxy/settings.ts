/**
 * Persistent server settings.
 *
 * Stored in ~/.config/meridian/settings.json. Survives proxy restarts.
 * Shared between CLI, UI, and API — browser localStorage is only used
 * for client-only preferences (theme, collapsed sections, etc.).
 *
 * This is a leaf module — no imports from server.ts or session/.
 */

import { existsSync, readFileSync, writeFileSync, mkdirSync } from "node:fs"
import { join, dirname } from "node:path"
import { homedir } from "node:os"

const SETTINGS_FILE = join(homedir(), ".config", "meridian", "settings.json")

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
  /**
   * Inject the full Claude Code MAIN system prompt (~7K-token harness prompt)
   * into native-forwarded requests. When false (default) only the cheap identity
   * blocks (billing header + "You are a Claude agent…") are sent, so customer
   * usage isn't inflated by the ~33K envelope. Turn ON only when the full CC
   * disguise is worth the token cost. Default: false (off).
   */
  injectSystemPrompt?: boolean
  /**
   * Inject the Claude Code base tool set (28 tools, ~26K tokens) into
   * native-forwarded requests. When false (default) the request carries the
   * user's OWN tools verbatim instead (not the CC 28, not empty). Default: false.
   */
  injectTools?: boolean
  /**
   * Egress proxy (normalized URL, e.g. socks5://user:pass@host:port). Applied
   * as ALL_PROXY/HTTPS_PROXY env so the SDK subprocess + native sidecar route
   * outbound traffic through it. Empty/undefined = direct.
   */
  egressProxy?: string
}

/** Read settings from disk. Returns empty object if file doesn't exist or is invalid. */
export function loadSettings(): MeridianSettings {
  try {
    if (!existsSync(SETTINGS_FILE)) return {}
    return JSON.parse(readFileSync(SETTINGS_FILE, "utf-8"))
  } catch {
    return {}
  }
}

/** Write settings to disk. Merges with existing settings (doesn't clobber unknown keys). */
export function saveSettings(updates: Partial<MeridianSettings>): void {
  const current = loadSettings()
  const merged = { ...current, ...updates }
  try {
    mkdirSync(dirname(SETTINGS_FILE), { recursive: true })
    writeFileSync(SETTINGS_FILE, JSON.stringify(merged, null, 2) + "\n", { mode: 0o600 })
  } catch (err) {
    console.warn(`[meridian] Failed to write ${SETTINGS_FILE}: ${err instanceof Error ? err.message : err}`)
  }
}

/** Get a single setting value */
export function getSetting<K extends keyof MeridianSettings>(key: K): MeridianSettings[K] {
  return loadSettings()[key]
}

/** Set a single setting value and persist */
export function setSetting<K extends keyof MeridianSettings>(key: K, value: MeridianSettings[K]): void {
  saveSettings({ [key]: value })
}
