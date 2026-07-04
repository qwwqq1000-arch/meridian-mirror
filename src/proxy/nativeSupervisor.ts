/**
 * nativeSupervisor.ts — circuit breaker + Go child-process manager.
 *
 * Leaf module: no imports from server.ts or session/.
 */

import { spawn, type ChildProcess } from "node:child_process"
import { claudeLog } from "../logger"

// ---------------------------------------------------------------------------
// CircuitBreaker — pure (takes `now` as param, no Date.now() inside)
// ---------------------------------------------------------------------------

export interface CircuitBreakerConfig {
  maxFailures: number
  cooldownMs: number
}

export class CircuitBreaker {
  private failures = 0
  private openedAt = -Infinity

  constructor(private cfg: CircuitBreakerConfig) {}

  recordFailure(now: number): void {
    this.failures++
    if (this.failures >= this.cfg.maxFailures) this.openedAt = now
  }

  recordSuccess(): void {
    this.failures = 0
    this.openedAt = -Infinity
  }

  isOpen(now: number): boolean {
    if (this.openedAt === -Infinity) return false
    if (now - this.openedAt >= this.cfg.cooldownMs) {
      this.failures = 0
      this.openedAt = -Infinity
      return false
    }
    return true
  }
}

// ---------------------------------------------------------------------------
// NativeSupervisor — spawns the Go binary, health-polls, exposes baseUrl()
// ---------------------------------------------------------------------------

const DEFAULT_BINARY_PATH = "/app/native-egress"
const HEALTH_POLL_INTERVAL_MS = 5000
const HEALTH_POLL_TIMEOUT_MS = 3000

export interface NativeSupervisorConfig {
  /** Override the binary path (defaults to MERIDIAN_NATIVE_EGRESS_PATH or /app/native-egress). */
  binaryPath?: string
  /** Port the Go binary listens on (default 9876). */
  port?: number
  maxFailures?: number
  cooldownMs?: number
}

export class NativeSupervisor {
  private child: ChildProcess | null = null
  private url: string | null = null
  private cb: CircuitBreaker
  private pollTimer: ReturnType<typeof setInterval> | null = null
  private restartChain: Promise<void> = Promise.resolve()
  private readonly resolvedBinaryPath: string
  private readonly port: number

  constructor(cfg: NativeSupervisorConfig = {}) {
    this.resolvedBinaryPath =
      cfg.binaryPath ??
      process.env["MERIDIAN_NATIVE_EGRESS_PATH"] ??
      DEFAULT_BINARY_PATH
    this.port = cfg.port ?? 9876
    this.cb = new CircuitBreaker({
      maxFailures: cfg.maxFailures ?? 3,
      cooldownMs: cfg.cooldownMs ?? 60000,
    })
  }

  /** Returns the base URL of the running Go binary, or null if unavailable. */
  baseUrl(): string | null {
    if (this.cb.isOpen(Date.now())) return null
    return this.url
  }

  /** Start the supervisor: spawn the binary and begin health polling. */
  async start(): Promise<void> {
    const fs = await import("node:fs")
    if (!fs.existsSync(this.resolvedBinaryPath)) {
      claudeLog(`[nativeSupervisor] binary not found at ${this.resolvedBinaryPath} — native egress disabled`)
      return
    }

    this.spawnBinary()
    this.startHealthPolling()
  }

  /** Stop the supervisor and kill the child process. */
  stop(): void {
    if (this.pollTimer !== null) {
      clearInterval(this.pollTimer)
      this.pollTimer = null
    }
    if (this.child !== null) {
      this.child.kill()
      this.child = null
    }
    this.url = null
  }

  /**
   * Restart cleanly: kill the child, WAIT for it to actually exit (so the OS
   * releases the listen port), add a short grace, reset the circuit breaker
   * (clears the failures the restart churn would otherwise leave behind), then
   * start fresh. Spawning before the old process frees port 9876 causes a bind
   * race → the new child exits → sidecar stays unavailable. Used when the egress
   * proxy changes at runtime (the Go child must re-inherit the new env).
   *
   * Restarts are serialized: two quick proxy saves chain instead of racing.
   */
  restart(): Promise<void> {
    this.restartChain = this.restartChain.then(
      () => this.restartOnce(),
      () => this.restartOnce(),
    )
    return this.restartChain
  }

  private async restartOnce(): Promise<void> {
    const old = this.child
    this.stop()
    if (old && old.exitCode === null) {
      await new Promise<void>((resolve) => {
        let settled = false
        const done = () => {
          if (settled) return
          settled = true
          resolve()
        }
        old.once("exit", done)
        setTimeout(done, 3000) // safety cap if SIGTERM is slow
      })
    }
    await new Promise((r) => setTimeout(r, 500)) // grace for socket release
    this.cb.recordSuccess() // clear stale failures so baseUrl() isn't gated
    await this.start()
  }

  private spawnBinary(): void {
    const child = spawn(this.resolvedBinaryPath, ["--port", String(this.port)], {
      stdio: ["ignore", "pipe", "pipe"],
    })

    child.stdout?.on("data", (chunk: Buffer) => {
      claudeLog(`[native-egress] ${chunk.toString().trimEnd()}`)
    })

    child.stderr?.on("data", (chunk: Buffer) => {
      const msg = chunk.toString().trimEnd()
      if (msg) console.error(msg)
    })

    child.on("exit", (code, signal) => {
      claudeLog(`[nativeSupervisor] binary exited (code=${String(code)} signal=${String(signal)})`)
      this.child = null
      this.url = null
      this.cb.recordFailure(Date.now())
    })

    this.child = child
    this.url = `http://127.0.0.1:${this.port}`
    claudeLog(`[nativeSupervisor] spawned pid=${String(child.pid)} url=${this.url}`)
  }

  private startHealthPolling(): void {
    this.pollTimer = setInterval(() => {
      void this.pollHealth()
    }, HEALTH_POLL_INTERVAL_MS)
  }

  private async pollHealth(): Promise<void> {
    if (this.url === null) return

    const controller = new AbortController()
    const timer = setTimeout(() => controller.abort(), HEALTH_POLL_TIMEOUT_MS)
    try {
      const res = await fetch(`${this.url}/health`, { signal: controller.signal })
      clearTimeout(timer)
      if (res.ok) {
        this.cb.recordSuccess()
      } else {
        this.cb.recordFailure(Date.now())
        claudeLog(`[nativeSupervisor] health check returned ${String(res.status)}`)
      }
    } catch (err) {
      clearTimeout(timer)
      const msg = err instanceof Error ? err.message : String(err)
      this.cb.recordFailure(Date.now())
      claudeLog(`[nativeSupervisor] health check failed: ${msg}`)
    }
  }
}

// ---------------------------------------------------------------------------
// Module-level singleton — shared across the server lifetime.
// `getNativeBaseUrl()` is the stable seam used by server.ts (and mocked in tests).
// ---------------------------------------------------------------------------

const _supervisor = new NativeSupervisor()
void _supervisor.start()

/**
 * Restart the native-egress sidecar so it re-inherits the current process env
 * (notably all_proxy/ALL_PROXY). Called after the egress proxy changes at
 * runtime — the already-spawned Go child holds a frozen env snapshot, so a
 * restart is required for a newly-saved proxy to take effect.
 */
export function restartNativeSupervisor(): void {
  // Fire-and-forget: the endpoint returns immediately while the sidecar restarts
  // (~3.5s) in the background. restart() waits for the old child to exit before
  // re-spawning, avoiding the port-bind race that left the sidecar unavailable.
  void _supervisor.restart().catch(() => {})
}

/**
 * Returns the base URL of the running Go sidecar, or null if unavailable.
 *
 * When `MERIDIAN_NATIVE_EGRESS_URL` is set to a non-empty string the singleton
 * is bypassed entirely and that URL is returned directly. This lets tests (and
 * operators pointing at an external sidecar) inject a known address without
 * spawning a binary or mocking this module. The env var is also useful in
 * production when the sidecar is managed outside of this process (e.g. a
 * separate Docker side-container).
 */
export function getNativeBaseUrl(): string | null {
  const override = process.env["MERIDIAN_NATIVE_EGRESS_URL"]
  if (override && override.length > 0) return override
  return _supervisor.baseUrl()
}
