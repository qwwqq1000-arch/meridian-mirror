/**
 * Claude Code request-shape detector.
 *
 * Decides whether an incoming `/v1/messages` body genuinely looks like a Claude
 * Code request. Used by the native-forward path as an anti-forgery gate: since
 * adapter detection is header-based (and therefore spoofable), a caller could
 * set `user-agent: claude-cli/…` to be classified as Claude Code and reach the
 * native path. This check ensures that what actually gets forwarded under the
 * operator's OAuth token genuinely resembles a Claude Code request — blocking a
 * non-CC body (which would both be off-purpose and, carrying a CC fingerprint,
 * risk flagging the account).
 *
 * Signals (both required):
 *   1. The first system text block begins with the Claude Code identity line.
 *   2. The tool set includes a quorum of Claude Code's PascalCase core tools.
 *      Non-CC clients use lowercase (`read`/`write`) or `mcp__…`-prefixed names,
 *      so they miss the quorum.
 *
 * Pure leaf module — no I/O, no imports from server.ts or session/.
 */

/**
 * Version-stable identity PREFIX. Genuine Claude Code's system block begins with
 * this; the sentence then continues differently across CLI versions — older
 * builds end "…for Claude." while current builds continue "…for Claude, running
 * within the Claude Agent SDK." Matching the prefix (not the full sentence)
 * accepts both and survives future wording tweaks after this point.
 */
export const CC_IDENTITY = "You are Claude Code, Anthropic's official CLI for Claude"

/**
 * Strong, version-stable discriminators. Genuine Claude Code always ships these
 * PascalCase names; non-CC harnesses rename or MCP-prefix them. A quorum (not an
 * exact match) avoids false negatives when a CC version adds/removes a tool or
 * the user restricts the allowlist.
 */
const CC_QUORUM_TOOLS = new Set([
  "Bash", "Read", "Edit", "Write", "MultiEdit", "Glob", "Grep", "LS",
  "NotebookEdit", "WebFetch", "WebSearch", "TodoWrite", "Task", "ExitPlanMode",
])

const DEFAULT_MIN_TOOLS = 4

/** All system text-block strings, in order. (string system → single element) */
function systemTexts(system: unknown): string[] {
  if (typeof system === "string") return [system]
  if (Array.isArray(system)) {
    const out: string[] = []
    for (const b of system) {
      if (b && typeof b === "object" && (b as { type?: unknown }).type === "text") {
        const text = (b as { text?: unknown }).text
        if (typeof text === "string") out.push(text)
      }
    }
    return out
  }
  return []
}

export interface CcShapeReport {
  ok: boolean
  /** Some system text block starts with the CC identity line. */
  identityOk: boolean
  /** How many of the request's tools are CC PascalCase quorum tools. */
  toolHits: number
  /** Total tools in the request. */
  toolCount: number
  /** Minimum CC tools required. */
  minTools: number
  /** First ~60 chars of the first system text block (for diagnosing identity mismatch). */
  systemPrefix: string
  /** Up to 8 tool names from the request (for diagnosing naming mismatch). */
  sampleTools: string[]
}

/**
 * Full breakdown of why a body is / isn't Claude-Code-shaped. Used by the native
 * gate (via `isClaudeCodeShaped`) and for actionable reject logging.
 */
export function inspectClaudeCodeShape(body: unknown, opts?: { minTools?: number }): CcShapeReport {
  const minTools = opts?.minTools ?? DEFAULT_MIN_TOOLS
  const empty: CcShapeReport = { ok: false, identityOk: false, toolHits: 0, toolCount: 0, minTools, systemPrefix: "", sampleTools: [] }
  if (!body || typeof body !== "object") return empty
  const b = body as { system?: unknown; tools?: unknown }

  // Genuine Claude Code does not always put the identity first — recent CLI
  // versions emit a `x-anthropic-billing-header: cc_version=…` system block
  // ahead of it. Scan ALL system text blocks, not just the first.
  const texts = systemTexts(b.system)
  const identityOk = texts.some((t) => t.trimStart().startsWith(CC_IDENTITY))

  const tools = Array.isArray(b.tools) ? b.tools : []
  const names: string[] = []
  let toolHits = 0
  for (const t of tools) {
    const name = t && typeof t === "object" ? (t as { name?: unknown }).name : undefined
    if (typeof name === "string") {
      names.push(name)
      if (CC_QUORUM_TOOLS.has(name)) toolHits++
    }
  }

  return {
    ok: identityOk && toolHits >= minTools,
    identityOk,
    toolHits,
    toolCount: tools.length,
    minTools,
    systemPrefix: (texts[0] ?? "").slice(0, 60),
    sampleTools: names.slice(0, 8),
  }
}

export function isClaudeCodeShaped(body: unknown, opts?: { minTools?: number }): boolean {
  return inspectClaudeCodeShape(body, opts).ok
}

/**
 * True if any assistant message carries a `thinking` / `redacted_thinking` block.
 *
 * Such blocks carry a cryptographic signature that must reach Anthropic exactly
 * as originally returned. When the request has passed through an intermediary
 * that re-serializes JSON (e.g. a new-api gateway), the signature no longer
 * matches and Anthropic rejects the native forward with a 400 ("thinking blocks
 * … cannot be modified"). We can't repair an already-mangled block, so the
 * native path degrades these to the SDK (which reconstructs the request) instead
 * of making a doomed upstream call that would also trip the circuit breaker.
 */
export function hasThinkingBlocks(body: unknown): boolean {
  if (!body || typeof body !== "object") return false
  const msgs = (body as { messages?: unknown }).messages
  if (!Array.isArray(msgs)) return false
  for (const m of msgs) {
    const content = m && typeof m === "object" ? (m as { content?: unknown }).content : undefined
    if (!Array.isArray(content)) continue
    for (const blk of content) {
      const t = blk && typeof blk === "object" ? (blk as { type?: unknown }).type : undefined
      if (t === "thinking" || t === "redacted_thinking") return true
    }
  }
  return false
}
