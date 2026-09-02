/**
 * Waiting for THIS app to finish fetching — which is not `networkidle`.
 *
 * WHY THIS MODULE EXISTS (2026-09-01/02): `waitForLoadState('networkidle')`
 * resolves 500ms after the last in-flight connection ends. This app debounces
 * its WebSocket-driven react-query invalidations by 750ms, in
 * `scheduleInvalidations()` in frontend/src/hooks/useStackEvents.ts. 750 > 500,
 * so a probe that stops at networkidle stops listening ~250ms BEFORE a
 * WS-triggered refetch can even be scheduled, let alone land.
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
 * Not a concern, settled 2026-09-02: networkidle DOES resolve on a page holding
 * open WebSockets — terminal-flow.spec.ts awaits it on a page with two live
 * sockets and passes in CI. Sockets are not what makes it the wrong bound.
 *
 * HOW THIS IS ENFORCED. Reading a tally's `count` THROWS unless a
 * `waitForInvalidationSettle()` on the same page has completed since the tally
 * started — see `countMatchingRequests()`. That is the mechanism, and it cannot
 * be evaded by naming, formatting or file layout. A static backstop
 * (scripts/check-networkidle-probes.sh) catches hand-rolled listeners, and
 * `.route()` handlers, that never reach this module at all.
 *
 * WHAT IS DELIBERATELY NOT COVERED, and why the trade was taken: counting by
 * repeated `await page.waitForResponse(...)` calls. Neither layer sees it --
 * layer 1 is never reached because no tally is constructed, and the static
 * backstop does not look at waits at all. This is a decision, not an oversight.
 * The gate that preceded this one DID try to catch it, by flagging a bare
 * networkidle wait sitting near anything that looked like a counter, and
 * OBSERVED 2026-09-02 that rule turned one ordinary `await
 * page.waitForResponse(...)` into 6 violations on a REQUIRED CI check, five of
 * them on pre-existing untouched lines -- while a spec that misused this very
 * helper still exited 0. An ordinary wait is not a measurement, and no line
 * scanner can tell "this wait bounds that count" from "this wait is a wait", so
 * a rule that tries reddens correct code far more often than it catches a bad
 * probe. `waitForResponse` therefore stays unflagged, and a probe that counts
 * with it is caught in review rather than by a gate. `page.routeFromHAR()` and
 * `page.routeWebSocket()` are excluded from the backstop for the same kind of
 * reason, recorded there rather than here.
 */

import type { Page, Request } from 'playwright/test'

/**
 * The app's WS-invalidation debounce, restated rather than imported.
 *
 * WHY a duplicate constant: this file is transpiled by Playwright outside the
 * frontend's Vite/tsconfig graph, so importing from frontend/src would drag
 * that project's path aliases in for one number. The trade-off is a constant
 * that can drift, so the drift is checked instead of trusted:
 * scripts/check-networkidle-probes.sh re-reads the timeout out of
 * `scheduleInvalidations()` on every CI run and fails when the two disagree.
 *
 * Source of truth: `scheduleInvalidations()` in
 * frontend/src/hooks/useStackEvents.ts.
 */
export const WS_INVALIDATION_DEBOUNCE_MS = 750

/** Slack on top of the debounce, covering the refetch's own round trip. */
const SETTLE_GRACE_MS = 300

/** Cap on a settle, so a permanently chatty page fails loudly instead of hanging. */
const SETTLE_TIMEOUT_MS = 30_000

/** How often the quiet window is re-checked. */
const SETTLE_POLL_MS = 50

/**
 * Which requests count as "the app is still working". Everything, by default.
 *
 * MEASURED 2026-09-02 against the dev fixture, and the reason this is NOT
 * narrowed to API traffic: a fresh /dashboard navigation streams ~200 Vite
 * module requests (/src/**, /node_modules/.vite/deps/**) over roughly 12s, with
 * gaps up to 1653ms, and the app's own first /api/v1/* call lands late inside
 * that window. Filtering to `/api/` made a settle immediately after `goto`
 * return before the app had fetched anything at all — the tally read 0 where it
 * should have read 1 (OBSERVED: the L1-B control failed at
 * `expect(tally.count).toBeGreaterThanOrEqual(1)`). Under-waiting is precisely
 * the defect this module exists to remove, so the default over-waits instead.
 *
 * That cost is paid only right after a navigation, and only under a dev server.
 * The intended shape — tally, click on an already-loaded page, settle — loads no
 * modules and settles in about one quiet window. A caller who knows the page is
 * loaded can narrow it with `activityFilter: (url) => url.includes('/api/')`.
 */
const DEFAULT_ACTIVITY_FILTER = () => true

/**
 * Per-page count of completed settles.
 *
 * A WeakMap rather than a property on the page so nothing leaks when the page
 * closes, and so the stamp cannot be read or forged from spec code.
 */
const settleStamps = new WeakMap<Page, number>()

export interface InvalidationSettleOptions {
  /** Override the debounce when testing a component that uses a different one. */
  debounceMs?: number
  /** Extra quiet demanded on top of the debounce, for the refetch's round trip. */
  graceMs?: number
  /** Give up after this long and throw. */
  timeoutMs?: number
  /** Which request URLs restart the quiet window. Defaults to all of them. */
  activityFilter?: (url: string) => boolean
}

/**
 * Settle past the app's WS-invalidation debounce, not merely to network quiet.
 *
 * This waits for an OBSERVED quiet window rather than sleeping a fixed amount.
 * `scheduleInvalidations()` calls `clearTimeout` and re-arms on every event, so
 * the invalidation fires 750ms after the LAST event, not the first: under a
 * burst — a stack restart streams container events — a fixed
 * `waitForTimeout(750 + 300)` can expire while the deadline is still being
 * pushed out, which is the same failure as the networkidle bug it replaces,
 * just with a narrower window. So the clock here restarts on every request and
 * every WebSocket frame, and the wait returns only once the page has been quiet
 * for `debounceMs + graceMs`.
 *
 * KNOWN LIMIT: `page.on('websocket')` only reports sockets opened AFTER it is
 * attached, and this app opens its event sockets during page load. For a burst
 * arriving on one of those pre-existing sockets the frames are invisible here,
 * and only the resulting HTTP refetches restart the clock. That still cannot
 * return mid-refetch, but a burst running longer than the quiet window with no
 * HTTP traffic at all could be cut short. Not observed in this suite; stated
 * because the fix (attaching a tracker before the first navigation) costs API
 * surface nobody needs yet.
 */
export async function waitForInvalidationSettle(
  page: Page,
  options: InvalidationSettleOptions = {}
): Promise<void> {
  const debounceMs = options.debounceMs ?? WS_INVALIDATION_DEBOUNCE_MS
  const graceMs = options.graceMs ?? SETTLE_GRACE_MS
  const timeoutMs = options.timeoutMs ?? SETTLE_TIMEOUT_MS
  const isActivity = options.activityFilter ?? DEFAULT_ACTIVITY_FILTER
  const quietMs = debounceMs + graceMs

  // Seeded to "now", so a page that is already silent still waits one full
  // quiet window rather than returning immediately.
  let lastActivityAt = Date.now()
  const touch = () => {
    lastActivityAt = Date.now()
  }
  const onRequest = (request: { url(): string }) => {
    if (isActivity(request.url())) touch()
  }
  const onSocket = (socket: { on(event: string, handler: () => void): void }) => {
    socket.on('framereceived', touch)
    socket.on('framesent', touch)
  }

  page.on('request', onRequest)
  page.on('websocket', onSocket)
  try {
    const deadline = Date.now() + timeoutMs
    for (;;) {
      const quietFor = Date.now() - lastActivityAt
      if (quietFor >= quietMs) break
      if (Date.now() >= deadline) {
        throw new Error(
          `waitForInvalidationSettle: page never went quiet for ${quietMs}ms within ${timeoutMs}ms. ` +
            'Something is still requesting; raise timeoutMs or narrow what the page is doing.'
        )
      }
      await page.waitForTimeout(Math.min(SETTLE_POLL_MS, quietMs - quietFor))
    }
  } finally {
    page.off('request', onRequest)
    page.off('websocket', onSocket)
  }

  settleStamps.set(page, (settleStamps.get(page) ?? 0) + 1)
}

export interface RequestTally {
  /** Requests matched so far. THROWS until the page has been settled. */
  readonly count: number
  /** The matched URLs, in order, for failure messages. Same guard as `count`. */
  readonly urls: readonly string[]
  /** Detach the listener. Safe to call more than once, and never throws. */
  stop(): void
}

/**
 * Tally the requests whose URL matches `match`, from now until `stop()`.
 *
 * Reading `count` (or `urls`) throws unless `waitForInvalidationSettle()` has
 * completed on this page since the tally was created. That is deliberate and is
 * the whole mechanism: a tally is only as trustworthy as the window it was
 * taken over, and every cheap-looking bound — `networkidle`, a fixed sleep, an
 * `expect` on some other element — stops before this app's debounce can fire.
 * Ordering matters, so start the tally, do the thing, settle, then read:
 *
 *   const tally = countMatchingRequests(page, /\/api\/v1\/stacks$/)
 *   await page.getByRole('button', { name: 'Restart' }).click()
 *   await waitForInvalidationSettle(page)
 *   expect(tally.count).toBe(1)
 */
export function countMatchingRequests(
  page: Page,
  match: RegExp | ((url: string) => boolean)
): RequestTally {
  const urls: string[] = []
  const matches = typeof match === 'function' ? match : (url: string) => match.test(url)
  const startStamp = settleStamps.get(page) ?? 0

  const onRequest = (request: Request) => {
    const url = request.url()
    if (matches(url)) urls.push(url)
  }
  page.on('request', onRequest)

  const assertSettled = () => {
    if ((settleStamps.get(page) ?? 0) > startStamp) return
    throw new Error(
      'Refusing to read a request tally that was never settled. Await ' +
        'waitForInvalidationSettle(page) from ' +
        'testing/tests/playwright/helpers/network-settle.ts after the action and ' +
        'before reading .count. Bounding the window with networkidle (or any fixed ' +
        'wait) is not enough: it resolves at 500ms, while this app debounces its ' +
        `WS-driven invalidations by ${WS_INVALIDATION_DEBOUNCE_MS}ms, so the refetch ` +
        'you are trying to count lands after you stopped listening.'
    )
  }

  return {
    get count() {
      assertSettled()
      return urls.length
    },
    get urls() {
      assertSettled()
      return urls.slice()
    },
    stop() {
      page.off('request', onRequest)
    },
  }
}
