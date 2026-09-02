import { test, expect, Page } from 'playwright/test'

import {
  countMatchingRequests,
  waitForInvalidationSettle,
} from './helpers/network-settle'

/**
 * Regression controls for the settle guard in helpers/network-settle.ts.
 *
 * WHY THESE EXIST AND WHY THEY ARE HERE. The guard is the only enforcement left
 * in this area: three static scanners were built to back it up and all three
 * were deleted for failing in both directions on a required check. So the guard
 * carries the whole load, and an earlier revision of it was ORNAMENTAL without
 * anyone noticing -- it checked that some settle had happened since the tally
 * object was constructed, which is satisfied by settling BEFORE the action and
 * never waiting for the traffic at all. OBSERVED 2026-09-02 against that
 * revision: `ATTACK A count read OK -> 2 (guard did NOT fire)`. GUARD-002 below
 * is that attack, frozen.
 *
 * These drive a STUB page rather than a browser: the guard's semantics are
 * about ordering, not about rendering, and a stub makes the ordering explicit
 * instead of hoping real traffic arrives in the right sequence. No fixture, no
 * chromium, no network.
 *
 * The debounce is overridden to a few milliseconds throughout. What is under
 * test is which orderings are allowed, not how long the real quiet window is --
 * the real value is checked separately, and continuously, by
 * scripts/check-networkidle-probes.sh.
 */

const FAST = { debounceMs: 40, graceMs: 10 }

interface StubPage {
  on(event: string, handler: (arg: unknown) => void): void
  off(event: string, handler: (arg: unknown) => void): void
  waitForTimeout(ms: number): Promise<void>
  /** Deliver one request to every attached 'request' handler. */
  fire(url?: string): void
  /** How many handlers are currently attached for an event. */
  attached(event: string): number
  /** Every event name ever subscribed to, so leaks and strays are visible. */
  subscribed(): string[]
}

function stubPage(): StubPage {
  const handlers: Record<string, Array<(arg: unknown) => void>> = {}
  const seen = new Set<string>()
  return {
    on(event, handler) {
      seen.add(event)
      ;(handlers[event] = handlers[event] ?? []).push(handler)
    },
    off(event, handler) {
      handlers[event] = (handlers[event] ?? []).filter((h) => h !== handler)
    },
    async waitForTimeout(ms) {
      await new Promise((resolve) => setTimeout(resolve, ms))
    },
    fire(url = 'http://localhost:5001/api/v1/stacks') {
      ;[...(handlers['request'] ?? [])].forEach((h) => h({ url: () => url }))
    },
    attached(event) {
      return (handlers[event] ?? []).length
    },
    subscribed() {
      return [...seen]
    },
  }
}

/** The stub is deliberately not a Page; only the four members used are real. */
const asPage = (stub: StubPage) => stub as unknown as Page

const MATCH = /\/api\/v1\/stacks/

test.describe('settle guard', () => {
  test('GUARD-001: act, settle, read -- the sanctioned order returns the tally', async () => {
    const stub = stubPage()
    const tally = countMatchingRequests(asPage(stub), MATCH)
    try {
      stub.fire()
      stub.fire()
      await waitForInvalidationSettle(asPage(stub), FAST)
      expect(tally.count).toBe(2)
      expect(tally.urls).toHaveLength(2)
    } finally {
      tally.stop()
    }
  })

  test('GUARD-002: settle BEFORE the action throws (the attack that defeated the old guard)', async () => {
    const stub = stubPage()
    const tally = countMatchingRequests(asPage(stub), MATCH)
    try {
      await waitForInvalidationSettle(asPage(stub), FAST)
      stub.fire()
      stub.fire()
      expect(() => tally.count).toThrow(/not been settled since its last matched request/)
      expect(() => tally.urls).toThrow(/not been settled since its last matched request/)
    } finally {
      tally.stop()
    }
  })

  test('GUARD-003: a settle does not unlock the tally permanently', async () => {
    const stub = stubPage()
    const tally = countMatchingRequests(asPage(stub), MATCH)
    try {
      stub.fire()
      await waitForInvalidationSettle(asPage(stub), FAST)
      expect(tally.count).toBe(1)

      // New traffic after that settle re-locks it: the read above measured a
      // window this traffic is not inside.
      stub.fire()
      expect(() => tally.count).toThrow(/not been settled since its last matched request/)

      // ...and settling again makes it readable, with the new total.
      await waitForInvalidationSettle(asPage(stub), FAST)
      expect(tally.count).toBe(2)
    } finally {
      tally.stop()
    }
  })

  test('GUARD-004: a tally that was never settled throws, even at zero', async () => {
    const stub = stubPage()
    const tally = countMatchingRequests(asPage(stub), MATCH)
    try {
      expect(() => tally.count).toThrow(/not been settled/)
    } finally {
      tally.stop()
    }
  })

  test('GUARD-005: a tally that matched nothing still reads 0 once settled', async () => {
    const stub = stubPage()
    const tally = countMatchingRequests(asPage(stub), MATCH)
    try {
      stub.fire('http://localhost:5001/api/v1/networks')
      await waitForInvalidationSettle(asPage(stub), FAST)
      expect(tally.count).toBe(0)
    } finally {
      tally.stop()
    }
  })

  test('GUARD-006: settling a different page does not satisfy this one', async () => {
    const mine = stubPage()
    const other = stubPage()
    const tally = countMatchingRequests(asPage(mine), MATCH)
    try {
      mine.fire()
      await waitForInvalidationSettle(asPage(other), FAST)
      expect(() => tally.count).toThrow(/not been settled/)
    } finally {
      tally.stop()
    }
  })

  test('GUARD-007: the settle leaks no listeners, and never subscribes to websockets', async () => {
    const stub = stubPage()
    await waitForInvalidationSettle(asPage(stub), FAST)
    await waitForInvalidationSettle(asPage(stub), FAST)
    expect(stub.attached('request')).toBe(0)

    // WS frames were deliberately taken off the settle clock: they were already
    // invisible for sockets opened before the listener attached, and a socket
    // opening DURING a settle pushes 1000ms metrics against a 1050ms quiet
    // window, which would spin to the 30s cap and throw.
    expect(stub.subscribed()).not.toContain('websocket')
  })

  test('GUARD-008: stop() detaches, is idempotent, and never throws', async () => {
    const stub = stubPage()
    const tally = countMatchingRequests(asPage(stub), MATCH)
    expect(stub.attached('request')).toBe(1)
    tally.stop()
    tally.stop()
    expect(stub.attached('request')).toBe(0)

    // A request after stop() is not counted, and cannot re-lock a settled read.
    stub.fire()
    await waitForInvalidationSettle(asPage(stub), FAST)
    expect(tally.count).toBe(0)
  })
})
