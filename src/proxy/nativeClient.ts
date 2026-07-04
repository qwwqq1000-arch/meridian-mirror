type FetchLike = (input: string, init?: RequestInit) => Promise<Response>

export async function forwardToNative(input: {
  baseUrl: string
  /**
   * The VERBATIM client request body (the exact text Meridian received). It is
   * sent as the raw POST body — never re-serialized — so assistant `thinking`
   * block signatures survive intact. Re-parsing/re-stringifying would corrupt
   * them and Anthropic rejects the forward with a 400.
   */
  rawBody: string
  profile: { configDir: string; account: string }
  stream: boolean
  /**
   * The client's request-specific `anthropic-beta` header. Forwarded so the Go
   * relay can union it with the fingerprint's baseline — request features like
   * structured outputs require their beta flag, which the static capture lacks.
   */
  anthropicBeta?: string
  /**
   * A stable per-conversation key (the client's session id, else the lineage
   * session id, else the conversation fingerprint). The Go relay hashes it into
   * metadata.session_id + x-claude-code-session-id so those ROTATE per
   * conversation like real Claude Code — without it every request from an
   * account shares one eternal session id, an obvious proxy tell.
   */
  sessionKey?: string
  fetchImpl?: FetchLike
}): Promise<{ degraded: boolean; reason?: string; response?: Response; connectionFailed?: boolean }> {
  const fetchImpl = input.fetchImpl ?? (globalThis.fetch as FetchLike)
  try {
    const res = await fetchImpl(`${input.baseUrl}/relay`, {
      method: "POST",
      headers: {
        "content-type": "application/json",
        "x-native-config-dir": input.profile.configDir,
        "x-native-account": input.profile.account,
        "x-native-stream": input.stream ? "1" : "0",
        ...(input.anthropicBeta ? { "x-native-anthropic-beta": input.anthropicBeta } : {}),
        ...(input.sessionKey ? { "x-native-session-key": input.sessionKey } : {}),
      },
      body: input.rawBody,
    })
    if (res.headers.get("X-Degrade") === "1") {
      return { degraded: true, reason: res.headers.get("X-Degrade-Reason") ?? "unknown" }
    }
    return { degraded: false, response: res }
  } catch (err) {
    // Couldn't reach the sidecar at all — this (and only this) is a genuine
    // "sidecar down" signal that should count toward the circuit breaker. A
    // relay degrade (X-Degrade above) means the sidecar IS up and responded.
    return { degraded: true, reason: err instanceof Error ? err.message : "connection_error", connectionFailed: true }
  }
}
