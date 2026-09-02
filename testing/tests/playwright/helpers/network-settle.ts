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
 * HOW THIS IS ENFORCED. Reading a tally's `count` (or `urls`) THROWS unless a
 * `waitForInvalidationSettle()` on the same page completed AFTER the most
 * recent request that tally matched — see `countMatchingRequests()`.
 *
 * Ordering is the entire point, and getting it slightly wrong makes the guard
 * ornamental. An earlier revision only checked that SOME settle had happened
 * since the tally object was constructed. That sounds equivalent and is not:
 * settle first, act second, read third, and it never fired. OBSERVED
 * 2026-09-02 against that revision: `ATTACK A count read OK -> 2 (guard did
 * NOT fire)`, and one settle unlocked a tally permanently, for every later read
 * over every later window — `ATTACK B first=1 then count read OK -> 3`. Both
 * throw now, because the comparison is against the last matched REQUEST rather
 * than against the tally's birth.
 *
 * THERE IS NO STATIC BACKSTOP, and that is a decision (2026-09-02). Three were
 * built and all three deleted: a line-proximity heuristic, then a "raw listener
 * in a spec" scanner, then that plus `.route(`. Each failed in BOTH directions
 * on a REQUIRED check. The last one reddened a multi-line template literal of
 * prose (the string mask is per-line, only the block-comment state carries
 * across), reddened a correctly annotated stub whose JSDoc pushed the marker
 * past a three-line lookback, and reddened `router.route('/stacks').get(fn)` —
 * while waving through `page.on(\n  'request',\n  fn\n)`, which is exactly
 * what prettier emits once the handler grows. TypeScript is not line-oriented
 * and the formatter decides where the lines fall, so "is there a raw listener
 * in this file" is not a question a line scanner can answer. The runtime guard
 * needs no help from one: it lives inside the code that is actually counting,
 * so it has no false positives by construction.
 *
 * WHAT IS DELIBERATELY NOT COVERED. Three things, each a decision rather than
 * an oversight:
 *
 *   1. Counting by repeated `await page.waitForResponse(...)`. No tally is
 *      constructed, so the guard is never reached. The gate that tried to catch
 *      this statically turned one ordinary wait into 6 violations on a REQUIRED
 *      check, five of them on untouched pre-existing lines (OBSERVED
 *      2026-09-02), while still passing a spec that misused this very helper.
 *      An ordinary wait is not a measurement and no line scanner can tell the
 *      two apart, so it stays unflagged and review catches it instead.
 *   2. Counting inside a callback this module hands back to the caller —
 *      either the `match` predicate here or `activityFilter` on the settle.
 *      Both are caller code that sees every URL, so incrementing in one and
 *      never reading `.count` sidesteps the guard completely. MEASURED
 *      2026-09-02 against the fixed guard: `counted via activityFilter, no
 *      tally, no guard -> 2`.
 *   3. `page['on']`, `page.on.bind(page)`, or a computed event name, attaching
 *      a listener without ever touching this module.
 *
 * 2 and 3 are deliberate circumvention, not the accidental misuse this module
 * exists to prevent. A test helper cannot stop a caller who is trying to defeat
 * it, and building machinery that pretends otherwise is how the three deleted
 * scanners happened.
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
 * One monotonic clock for "what happened before what".
 *
 * Both a matched request and a completed settle take the next value, so the two
 * are directly comparable. A counter rather than `Date.now()` because the
 * question is purely one of ordering: two events inside the same millisecond
 * still have to be ordered, and a settle legitimately completes very soon after
 * the quiet window that preceded it.
 */
let eventSeq = 0

/**
 * The sequence number of the most recently completed settle, per page.
 *
 * A WeakMap rather than a property on the page so nothing leaks when the page
 * closes, and so the value cannot be read or forged from spec code.
 */
const lastSettleSeq = new WeakMap<Page, number>()

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
 * just with a narrower window. So the clock here restarts on every request, and
 * the wait returns only once the page has been quiet for
 * `debounceMs + graceMs`.
 *
 * WHY WEBSOCKET FRAMES ARE NOT ON THAT CLOCK (decided 2026-09-02, reversing an
 * earlier revision that put them there). Two reasons pointing the same way.
 *
 * It never worked for the traffic that matters: `page.on('websocket')` reports
 * only sockets opened AFTER it attaches, and this app opens its event sockets
 * during page load, so the frames that actually drive the invalidations were
 * invisible here from the start.
 *
 * And watching frames is actively hazardous. The backend pushes dashboard
 * metrics every 1000ms (`broadcastMetrics` in
 * backend/internal/services/monitor.go) while the quiet window is
 * debounce + grace = 1050ms. A socket opened DURING a settle — which
 * frontend/src/hooks/useWebSocket.ts does on reconnect — would restart the
 * clock every 1000ms against a 1050ms requirement, a 50ms margin, and the
 * settle would spin to its 30s cap and throw. Paying a hang risk for coverage
 * that was already absent is a bad trade, so HTTP alone drives the clock: an
 * invalidation worth waiting for ends in a refetch, and the refetch restarts
 * it. This also removes the per-socket handlers, which were attached on every
 * settle and never detached.
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
  page.on('request', onRequest)
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
  }

  lastSettleSeq.set(page, ++eventSeq)
}

export interface RequestTally {
  /**
   * Requests matched so far. THROWS unless a settle has completed since the
   * most recent matched request.
   */
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
 * completed on this page SINCE THE LAST REQUEST THIS TALLY MATCHED. That is
 * deliberate and is the whole mechanism: a tally is only as trustworthy as the
 * window it was taken over, and every cheap-looking bound — `networkidle`, a
 * fixed sleep, an `expect` on some other element — stops before this app's
 * debounce can fire.
 *
 * "Since the last matched request" rather than "since the tally was created",
 * because the second is satisfied by settling BEFORE the action and then never
 * waiting for the traffic at all. Read the guard as: nothing you are counting
 * may have arrived after the last time you waited.
 *
 * So: start the tally, do the thing, settle, then read — and stop it when done,
 * or the listener outlives the test.
 *
 *   const tally = countMatchingRequests(page, /\/api\/v1\/stacks$/)
 *   try {
 *     await page.getByRole('button', { name: 'Restart' }).click()
 *     await waitForInvalidationSettle(page)
 *     expect(tally.count).toBe(1)
 *   } finally {
 *     tally.stop()
 *   }
 */
export function countMatchingRequests(
  page: Page,
  match: RegExp | ((url: string) => boolean)
): RequestTally {
  const urls: string[] = []
  const matches = typeof match === 'function' ? match : (url: string) => match.test(url)

  // Seeded at construction, so a tally that has matched nothing at all still
  // demands a settle before it may be read as 0.
  let lastMatchSeq = ++eventSeq

  const onRequest = (request: Request) => {
    const url = request.url()
    if (!matches(url)) return
    urls.push(url)
    lastMatchSeq = ++eventSeq
  }
  page.on('request', onRequest)

  const assertSettled = () => {
    if ((lastSettleSeq.get(page) ?? 0) > lastMatchSeq) return
    throw new Error(
      'Refusing to read a request tally that has not been settled since its ' +
        'last matched request. The order has to be: create the tally, do the ' +
        'thing, await waitForInvalidationSettle(page) from ' +
        'testing/tests/playwright/helpers/network-settle.ts, THEN read .count. ' +
        'Settling BEFORE the action does not count -- the requests you are ' +
        'measuring arrive after that settle finished, so nothing ever waited ' +
        'for them. Bounding the window with networkidle (or any fixed wait) is ' +
        'not enough either: it resolves at 500ms, while this app debounces its ' +
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
