/**
 * Relay-mode helpers. Pure leaf module — no I/O, no imports from server.ts or
 * session/.
 *
 * Two concerns:
 *   1. Native eligibility — whether to forward verbatim to api.anthropic.com,
 *      bypassing the SDK. This is a SERVER-SIDE decision (per-adapter
 *      `nativeForward` toggle or the MERIDIAN_NATIVE_FORWARD env). A client can
 *      never ENABLE native; it may only DOWNGRADE to the SDK path via
 *      `x-meridian-mode: sdk`. (Without this rule, any client could spend the
 *      operator's OAuth token by spoofing a header.)
 *   2. relayMode override — pin the SDK path to internal/passthrough.
 */

export type RelayMode = "auto" | "internal" | "passthrough" | "native"

export function nativeEligible(input: {
  /** The adapter's `nativeForward` feature toggle (server-side setting). */
  featureNativeForward: boolean
  /** MERIDIAN_NATIVE_FORWARD=1 (server-side operator escape hatch). */
  envForceNative: boolean
  /** Client sent `x-meridian-mode: sdk` to opt OUT of native. */
  clientForcedSdk: boolean
  profileType: "claude-max" | "api" | "oauth-token"
}): boolean {
  if (input.clientForcedSdk) return false
  if (!(input.featureNativeForward || input.envForceNative)) return false
  // Native needs a usable OAuth token; `api` profiles are a bare API key.
  return input.profileType === "claude-max" || input.profileType === "oauth-token"
}

/** Apply an internal/passthrough relayMode override to the SDK-path passthrough flag. */
export function applyRelayModeToPassthrough(mode: RelayMode, pipelinePassthrough: boolean): boolean {
  if (mode === "internal") return false
  if (mode === "passthrough") return true
  return pipelinePassthrough
}
