/**
 * Unit tests for nativeSupervisor.ts — pure CircuitBreaker only.
 */

import { describe, it, expect } from "bun:test"
import { CircuitBreaker } from "../proxy/nativeSupervisor"

describe("CircuitBreaker", () => {
  it("opens after maxFailures and closes after cooldown", () => {
    const cb = new CircuitBreaker({ maxFailures: 3, cooldownMs: 60000 })
    expect(cb.isOpen(0)).toBe(false)
    cb.recordFailure(0); cb.recordFailure(0); cb.recordFailure(0)
    expect(cb.isOpen(0)).toBe(true)
    expect(cb.isOpen(59999)).toBe(true)
    expect(cb.isOpen(60001)).toBe(false) // cooldown elapsed → half-open
  })
  it("success resets the failure count", () => {
    const cb = new CircuitBreaker({ maxFailures: 3, cooldownMs: 60000 })
    cb.recordFailure(0); cb.recordFailure(0)
    cb.recordSuccess()
    cb.recordFailure(0)
    expect(cb.isOpen(0)).toBe(false)
  })
})
