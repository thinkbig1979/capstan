/**
 * Waiting for THIS app to finish fetching — which is not `networkidle`.
 *
 * WHY THIS MODULE EXISTS (2026-09-01/02): `waitForLoadState('networkidle')`
 * resolves 500ms after the last in-flight connection ends. This app debounces
 * its WebSocket-driven react-query invalidations by 750ms
 * (`scheduleInvalidations()` in frontend/src/hooks/useStackEvents.ts:130).
 * 750 > 500, so a probe that stops at networkidle stops listening ~250ms
 * BEFORE a WS-triggered refetch can even be scheduled, let alone land.
 *
 * OBSERVED, same session: a baseline probe bounded by networkidle reported
 * "/api/v1/stacks fires 1x". A second run that held the page for 12s measured
 * the same 1x AND separately saw a real second fetch land 752-928ms after a
 * `container_event` WS frame. The networkidle-bounded number was therefore
 * consistent with both "no bug" and "bug present but invisible to this
 * instrument" — it was one-sided evidence, not a measurement.
 *
 * The same blindness from the other direction, OBSERVED 2026-09-01: a
 * `page.route` stall cannot test an expect budget in this suite, because
 * `loginIfNeeded` ends in `waitForLoadState('networkidle')` and a stalled
 * request keeps the page non-idle — the wait simply runs longer under the 30s
 * navigationTimeout. A 12s stall on /backups/status or /backups/snapshots
 * PASSED at both EXPECT_MS=10000 and EXPECT_MS=15000. Only landing the delay
 * AFTER networkidle discriminated.
 *
 * RULE OF THUMB: waiting for RENDER, networkidle is fine (that is what every
 * current call site in this suite does). Asserting on a REQUEST COUNT, it is
 * not — use `waitForInvalidationSettle` so the window covers the debounce.
 * `scripts/check-networkidle-probes.sh` enforces this mechanically.
 */

import type { Page, Request } from 'playwright/test'

/**
 * The app's WS-invalidation debounce, restated rather than imported.
 *
 * WHY a duplicate constant: this file is transpiled by Playwright outside the
 * frontend's Vite/tsconfig graph, so importing from frontend/src would drag
 * that project's path aliases in for one number. The trade-off accepted is a
 * constant that can drift; the guard against drift is that the source of truth
 * is named right here and asserted below.
 *
 * Source of truth: `scheduleInvalidations()` in
 * frontend/src/hooks/useStackEvents.ts — `setTimeout(..., 750)`.
 */
export const WS_INVALIDATION_DEBOUNCE_MS = 750

/** Slack on top of the debounce, covering the refetch's own round trip. */
const SETTLE_GRACE_MS = 300

export interface InvalidationSettleOptions {
  /** Override the debounce when testing a component that uses a different one. */
  debounceMs?: number
  /** Extra time for the refetches the debounce releases to actually land. */
  graceMs?: number
}

/**
 * Settle past the app's WS-invalidation debounce, not merely to network quiet.
 *
 * Quiet first (so the debounce timer is measured from a real lull), then hold
 * for debounce + grace, then quiet again to absorb whatever the debounce
 * released. Call this — not `waitForLoadState('networkidle')` — before reading
 * a request tally.
 */
export async function waitForInvalidationSettle(
  page: Page,
  options: InvalidationSettleOptions = {}
): Promise<void> {
  const debounceMs = options.debounceMs ?? WS_INVALIDATION_DEBOUNCE_MS
  const graceMs = options.graceMs ?? SETTLE_GRACE_MS

  // networkidle-ok: this is the START of the settle window, not the end of it;
  // the debounce hold below is what makes the window cover a WS refetch.
  await page.waitForLoadState('networkidle')
  await page.waitForTimeout(debounceMs + graceMs)
  // networkidle-ok: closing quiet, after the debounce has been allowed to fire.
  await page.waitForLoadState('networkidle')
}

export interface RequestTally {
  /** Requests matched so far. */
  readonly count: number
  /** The matched URLs, in order, for failure messages. */
  readonly urls: readonly string[]
  /** Detach the listener. Safe to call more than once. */
  stop(): void
}

/**
 * Tally the requests whose URL matches `match`, from now until `stop()`.
 *
 * Paired with `waitForInvalidationSettle` on purpose: a tally is only as
 * trustworthy as the window it was taken over, and the whole point of this
 * module is that networkidle is the wrong window.
 */
export function countMatchingRequests(
  page: Page,
  match: RegExp | ((url: string) => boolean)
): RequestTally {
  const urls: string[] = []
  const matches = typeof match === 'function' ? match : (url: string) => match.test(url)

  const onRequest = (request: Request) => {
    const url = request.url()
    if (matches(url)) urls.push(url)
  }
  page.on('request', onRequest)

  return {
    get count() {
      return urls.length
    },
    urls,
    stop() {
      page.off('request', onRequest)
    },
  }
}
