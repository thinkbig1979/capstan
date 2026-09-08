/**
 * Dashboard Backups tab + stacks-table Auto backup toggle — Playwright spec
 *
 * Covers the two lak4 features that unit tests cannot reach, because both are
 * multi-page UI flows against a live backend:
 *   1. the dashboard's Backups tab actually mounts and loads real history;
 *   2. the stacks-table Auto backup toggle round-trips to the server, and
 *      clicking it does not navigate the row it lives in.
 *
 * Prerequisites (same as backup-flow.spec.ts):
 *   - App running: frontend on BASE_URL (default http://localhost:3001),
 *     backend on API_URL (default http://localhost:5001).
 *   - restic 0.18.0+ available to the backend process.
 *   - AUTH_DISABLED=true in backend env OR valid credentials set via
 *     CAPSTAN_TEST_USER / CAPSTAN_TEST_PASSWORD env vars.
 *   - A stack named CAPSTAN_TEST_STACK (default test-app) already registered.
 *
 * Run:
 *   npx playwright test testing/tests/playwright/dashboard-backups.spec.ts --retries=0
 *
 * WHY --retries=0 IS PART OF THE DOCUMENTED COMMAND: playwright.config.ts sets
 * `retries: process.env.CI ? 1 : 0`, and Playwright reports a fail-then-pass as
 * FLAKY rather than as passed. Without the flag, "3 passed" is not a claim the
 * CI run can actually make.
 *
 * NO SETTLE-STATE WAITS. Every wait here is positive: a locator assertion, a
 * `waitForResponse` on the request under test, or `waitForURL`. There is no
 * fixed-duration sleep and no load-state gate anywhere in this file. A
 * load-state gate resolves 500ms after the last connection while the app
 * debounces its WS-driven react-query invalidations by 750ms
 * (`scheduleInvalidations()` in frontend/src/hooks/useStackEvents.ts), so it is
 * a wait that expires before the thing it appears to be waiting for.
 *
 * SELECTORS — all derived from component source, so a later reader can tell a
 * stale selector from a real failure:
 *   - Route: the dashboard is `/`, NOT `/dashboard`. frontend/src/App.tsx:140
 *     mounts DashboardPage at `/` and :145 is a catch-all redirect, so a typo'd
 *     path silently lands on the same page and hides the mistake.
 *   - frontend/src/pages/DashboardPage.tsx:56-59 — the active tab IS the `?tab=`
 *     search param; `setActiveTab` writes `?tab=backups` (replace) and clears
 *     the param entirely for the default 'stacks' tab. So selecting the Backups
 *     tab CHANGES the URL, which is why the no-navigation test below compares
 *     against the URL captured immediately before its click rather than a
 *     hardcoded path.
 *   - frontend/src/components/ui/responsive-tabs-list.tsx:49-55 — the desktop
 *     TabsList (role=tab triggers) is `hidden md:inline-flex`; the mobile
 *     branch (:35-48) is a Select, `display:none` at md and above.
 *     playwright.config.ts:58-62 declares one project, chromium, at the Desktop
 *     Chrome viewport (1280x720), so only the role=tab branch is reachable here
 *     and the Select branch is deliberately out of scope.
 *   - frontend/src/components/dashboard/BackupHistoryTab.tsx:368 (<Table>) and
 *     :291 ("No backup history") — the two states this spec accepts.
 *   - frontend/src/components/dashboard/StacksTab.tsx:184-195 — rows are
 *     role="link" with an onClick that navigates; :139-145 wraps the whole
 *     Auto backup cell in <div onClick={stopRowNavigation} onKeyDown={...}>.
 *     That wrapper is what test 003 exercises at the browser layer.
 *   - frontend/src/components/dashboard/BackupToggle.tsx:
 *       :123 data-testid="backup-toggle-<stackId>"      (engine available)
 *       :129 data-testid="backup-switch-<stackId>"      (engine available ONLY)
 *       :94  data-testid="backup-toggle-disabled-<id>"  (engine unavailable)
 *       :144 data-testid="backup-stop-policy-<stackId>" (enabled ONLY)
 *       :166/:174 data-testid="backup-stop-policy-stop" / "-hot"
 *     The switch testid exists ONLY in the engine-available branch. If restic
 *     is missing or the repo is uninitialised, BackupToggle renders the locked
 *     branch at :88-119, which has NO switch testid — a bare switch locator
 *     would then simply time out and read as "the toggle is broken" when the
 *     truth is "the backup engine is not ready". ensureBackupEngine() below
 *     asserts that precondition up front and names the actual cause.
 */

import { test, expect, Page, APIRequestContext } from 'playwright/test'

// ─── Config ──────────────────────────────────────────────────────────────────

const BASE_URL = process.env.CAPSTAN_BASE_URL ?? 'http://localhost:3001'
const API_URL = process.env.CAPSTAN_API_URL ?? 'http://localhost:5001'
const TEST_USER = process.env.CAPSTAN_TEST_USER ?? 'testadmin@example.com'
const TEST_PASSWORD = process.env.CAPSTAN_TEST_PASSWORD ?? 'TestPass123!'
const AUTH_DISABLED = (process.env.AUTH_DISABLED ?? 'false') === 'true'
const TEST_STACK_NAME = process.env.CAPSTAN_TEST_STACK ?? 'test-app'
// Same defaults as backup-flow.spec.ts, deliberately: when both specs run in
// one suite, the settings write below is a no-op rather than a reconfiguration.
const BACKUP_REPO_PATH = process.env.CAPSTAN_BACKUP_REPO ?? '/tmp/capstan-e2e-restic-repo-playwright'
const BACKUP_PASSPHRASE = process.env.CAPSTAN_BACKUP_PASSPHRASE ?? 'capstan-e2e-playwright-passphrase'

let authToken = ''
let testStackId = ''
// CSRF double-submit token. The backend (middleware/csrf.go) sets a
// `capstan_csrf` cookie on any GET and requires the same value echoed in the
// `X-CSRF-Token` header on every mutating request. Each test gets a fresh
// request context (fresh cookie jar), so this is re-bootstrapped per test.
//
// NOT imported from backup-flow.spec.ts: `ensureCsrf` is private there and in
// terminal-flow.spec.ts, and the two copies do not even agree on a return type
// (backup-flow.spec.ts:139 returns Promise<string>, terminal-flow.spec.ts:94
// returns Promise<void>). This file carries its own copy of the Promise<string>
// shape.
let csrfToken = ''

// ─── API helpers ─────────────────────────────────────────────────────────────

/** GET the backend API directly, with auth header when we have a token. */
async function apiGet(request: APIRequestContext, path: string) {
  const headers: Record<string, string> = { 'Content-Type': 'application/json' }
  if (authToken) headers['Authorization'] = `Bearer ${authToken}`
  return request.get(`${API_URL}${path}`, { headers })
}

/**
 * Bootstrap the CSRF double-submit token for this request context.
 *
 * A GET makes the backend set the `capstan_csrf` cookie (its value IS the
 * token). We read it back from the context's cookie jar via storageState() so
 * the header we send on mutations always matches the cookie.
 */
async function ensureCsrf(request: APIRequestContext): Promise<string> {
  await apiGet(request, '/api/v1/stacks')
  const state = await request.storageState()
  const cookie = state.cookies.find((c) => c.name === 'capstan_csrf')
  if (cookie?.value) csrfToken = cookie.value
  return csrfToken
}

/** PUT/POST with JSON body, carrying the CSRF header for the double-submit check. */
async function apiMutate(
  request: APIRequestContext,
  method: 'PUT' | 'POST',
  path: string,
  data?: unknown,
) {
  const headers: Record<string, string> = { 'Content-Type': 'application/json' }
  if (authToken) headers['Authorization'] = `Bearer ${authToken}`
  if (csrfToken) headers['X-CSRF-Token'] = csrfToken
  const options = data === undefined ? { headers } : { headers, data }
  if (method === 'PUT') return request.put(`${API_URL}${path}`, options)
  return request.post(`${API_URL}${path}`, options)
}

/**
 * The server's view of whether this stack has backups enabled.
 *
 * This is the ONLY thing the toggle tests assert on. Reading the Switch's
 * checked state instead would assert BackupToggle's optimistic local state
 * (BackupToggle.tsx:31, set before the mutation resolves), which is true the
 * instant you click regardless of what the server did — an assertion that
 * cannot fail is not a round-trip test.
 *
 * A stack with no policy row at all is disabled, hence the `?? false`: the
 * endpoint returns only rows that exist (backend/internal/handlers/backup.go:581).
 */
async function readPolicyEnabled(
  request: APIRequestContext,
  stackId: string,
): Promise<boolean> {
  const resp = await apiGet(request, '/api/v1/backups/policies')
  expect(resp.status(), 'GET /backups/policies').toBe(200)
  const body = await resp.json()
  const policies: Array<{ targetId: string; enabled: boolean }> = body.policies ?? []
  return policies.find((p) => p.targetId === stackId)?.enabled ?? false
}

/**
 * Make the backup engine ready, then assert it IS ready.
 *
 * Both halves matter. The assert half is the point: BackupToggle's locked
 * branch has no switch testid (see the selector header), so without an
 * explicit precondition every toggle failure looks identical to a broken
 * toggle. The fix half is what lets this spec pass when run on its own — CI
 * initialises the repo inside backup-flow.spec.ts (BACKUP-PW-002), so a solo
 * run of THIS file would otherwise start against an uninitialised repository
 * and fail on a precondition it is perfectly able to satisfy itself.
 *
 * Both operations are idempotent and skipped entirely when the repo is already
 * initialised, so running inside the full suite (where this spec sorts after
 * backup-flow.spec.ts) changes nothing.
 *
 * restic being absent is NOT fixable from here and fails loudly instead.
 */
async function ensureBackupEngine(request: APIRequestContext): Promise<void> {
  const before = await apiGet(request, '/api/v1/backups/status')
  expect(before.status(), 'GET /backups/status').toBe(200)
  const status = await before.json()

  expect(
    status.resticAvailable,
    'restic is not available to the backend process. BackupToggle renders its ' +
      'locked branch (no backup-switch testid) in this state. Install restic ' +
      'on the machine running the backend; this spec cannot fix it.',
  ).toBe(true)

  if (status.repositoryInitialized) return

  const settingsResp = await apiMutate(request, 'PUT', '/api/v1/settings/backup', {
    repository: BACKUP_REPO_PATH,
    password: BACKUP_PASSPHRASE,
  })
  expect(settingsResp.status(), 'PUT /settings/backup').toBe(200)

  const initResp = await apiMutate(request, 'POST', '/api/v1/backups/repo/init')
  expect(initResp.status(), 'POST /backups/repo/init').toBe(200)

  const after = await apiGet(request, '/api/v1/backups/status')
  expect(after.status(), 'GET /backups/status (after init)').toBe(200)
  const afterStatus = await after.json()
  expect(
    afterStatus.repositoryInitialized,
    `Backup repository at ${BACKUP_REPO_PATH} is still not initialised after ` +
      'PUT /settings/backup + POST /backups/repo/init. BackupToggle will render ' +
      'its locked branch, which has no backup-switch testid.',
  ).toBe(true)
}

// ─── Page helpers ────────────────────────────────────────────────────────────

/** Log in via the UI if auth is on, then land on `target` (default: the dashboard). */
async function loginIfNeeded(page: Page, target = '/'): Promise<void> {
  if (!AUTH_DISABLED) {
    await page.goto(`${BASE_URL}/login`)
    // A session cookie may have skipped the form entirely.
    if (page.url().includes('login')) {
      await page.getByLabel(/email/i).fill(TEST_USER)
      await page.getByLabel(/password/i).fill(TEST_PASSWORD)
      await page.getByRole('button', { name: /login|sign in/i }).click()
      await page.waitForURL((u) => !u.href.includes('login'), { timeout: 15_000 })
    }
  }
  await page.goto(`${BASE_URL}${target}`)
}

/**
 * Click something that must trigger the policy upsert, and wait for the
 * response — not for a duration.
 *
 * The wait is armed BEFORE the click on purpose: the PUT can land before the
 * click promise resolves, and a listener attached afterwards would miss it and
 * then time out.
 *
 * This is also what makes the no-navigation assertion in test 003 a real one.
 * If stopRowNavigation were removed, react-router would navigate synchronously
 * inside the row's onClick while BackupToggle's own onCheckedChange still fires
 * the mutation — so the PUT lands either way, and by the time it does, a broken
 * build is already on /stacks/<id>. Asserting the URL straight after the click
 * with no wait would race that navigation and could pass on broken code.
 */
async function clickAwaitingPolicyPut(page: Page, click: () => Promise<void>) {
  const responsePromise = page.waitForResponse(
    (r) => r.request().method() === 'PUT' && r.url().includes('/backups/policies/stack/'),
  )
  await click()
  const resp = await responsePromise
  expect(resp.status(), 'PUT /backups/policies/stack/<id>').toBe(200)
}

// ─── Suite ───────────────────────────────────────────────────────────────────

test.describe('Dashboard backups tab and stack toggles E2E', () => {
  test.beforeEach(async ({ request }) => {
    if (!AUTH_DISABLED) {
      const loginResp = await request.post(`${API_URL}/api/v1/auth/login`, {
        data: { email: TEST_USER, password: TEST_PASSWORD },
      })
      if (loginResp.ok()) {
        const body = await loginResp.json()
        authToken = body.token ?? ''
      }
    }
    await ensureCsrf(request)

    const stacksResp = await apiGet(request, '/api/v1/stacks')
    expect(stacksResp.status(), 'GET /stacks').toBe(200)
    const stacksBody = await stacksResp.json()
    const stacks: Array<{ id: string; name?: string; projectName?: string }> =
      Array.isArray(stacksBody) ? stacksBody : stacksBody.stacks ?? []
    const testStack = stacks.find((s) =>
      (s.projectName ?? s.name ?? s.id ?? '').includes(TEST_STACK_NAME),
    )
    expect(testStack, `Stack '${TEST_STACK_NAME}' not found`).toBeTruthy()
    testStackId = testStack!.id

    await ensureBackupEngine(request)
  })

  // ── 001: the Backups tab mounts and loads ──────────────────────────────────

  test('DASH-BACKUPS-PW-001: Backups tab renders history or its empty state, with no failed requests', async ({
    page,
  }) => {
    // Armed before the first navigation so it sees the whole dashboard boot:
    // the auth probe, settings/config, resources/updates, dashboard/stats,
    // backups/status, stacks, then the history fetch the tab itself makes.
    //
    // ONE exemption, and it is not a convenience. `GET /auth/me` sits behind
    // AuthMiddleware and is the boot-time SESSION PROBE, so an anonymous boot
    // — which is every boot under AUTH_DISABLED=true — genuinely answers 401
    // and the app treats that as "not logged in", not as an error
    // (frontend/src/lib/api.ts:106-117, and authStore.ts:156). OBSERVED: with
    // no exemption at all this assertion failed on exactly one entry,
    // `401 GET http://localhost:3001/api/v1/auth/me`, and on nothing else. So
    // the tally is not a check that could only come out clean: it has been
    // seen firing, and this exemption is pinned to that one method+path+status
    // rather than to a prefix, so any OTHER 401 — including a 401 on a
    // different endpoint, or a 403/404/500 on this one — still fails.
    const isExpectedBootProbe401 = (method: string, url: string, status: number) =>
      status === 401 && method === 'GET' && new URL(url).pathname === '/api/v1/auth/me'

    const failed: string[] = []
    page.on('response', (r) => {
      const method = r.request().method()
      if (r.status() >= 400 && !isExpectedBootProbe401(method, r.url(), r.status())) {
        failed.push(`${r.status()} ${method} ${r.url()}`)
      }
    })

    await loginIfNeeded(page, '/')

    const backupsTab = page.getByRole('tab', { name: 'Backups' })
    await expect(backupsTab).toBeVisible()
    await backupsTab.click()

    // DashboardPage.tsx:56-59 puts the active tab in the URL, so this both
    // confirms the click registered and bounds the tab swap positively.
    await page.waitForURL(/[?&]tab=backups/)

    // The run table is identified by a column header only IT has, not by being
    // "a table inside the active tabpanel".
    //
    // That looser version was WRITTEN FIRST AND PASSED AGAINST A MUTANT that
    // deleted <BackupHistoryTab /> from the panel entirely (OBSERVED: 1 passed
    // in 3.0s). Two things defeated it at once: Radix leaves every visited
    // TabsContent in the DOM (OBSERVED: 10 elements match role=tabpanel on
    // this page), so scoping to "the tabpanel" scopes to all of them; and
    // `waitForURL` resolves the instant the search param changes, while the
    // stacks table is still the visible one — so the assertion read the STACKS
    // table, which has its own <table> and is present either way.
    // BackupHistoryTab.tsx:374 has a 'Trigger' column; StacksTab.tsx:31-38's
    // columns are Name/Status/Containers/Auto update/Auto backup/Actions, so
    // this filter cannot match the wrong table no matter what is on screen.
    const runTable = page
      .getByRole('table')
      .filter({ has: page.getByRole('columnheader', { name: 'Trigger', exact: true }) })
    const emptyState = page.getByText('No backup history')

    // Either branch is a correct render: which one appears depends on whether
    // any backup has run against this install yet, and this spec deliberately
    // does not run one (that is backup-flow.spec.ts's job). Both are asserted
    // through one retrying locator, so a tab that renders NEITHER — the real
    // failure mode, an errored or never-mounted tab — still fails.
    await expect(runTable.or(emptyState).first()).toBeVisible()

    expect(
      failed,
      `Dashboard + Backups tab produced failing HTTP responses:\n${failed.join('\n')}`,
    ).toEqual([])
  })

  // ── 002: the toggle round-trips to the server, both ways ───────────────────

  test('DASH-BACKUPS-PW-002: Auto backup toggle round-trips to the server and back', async ({
    page,
    request,
  }) => {
    // Read the server's starting value FIRST. Nothing below assumes a
    // direction: run this file alone and the policy starts absent (disabled);
    // run it in the full suite and backup-flow.spec.ts:265 has already left it
    // ENABLED for this stack with no teardown hook, so a test that hard-coded
    // "true after the first click" would pass alone and fail in CI.
    const initial = await readPolicyEnabled(request, testStackId)

    await loginIfNeeded(page, '/')

    const backupSwitch = page.getByTestId(`backup-switch-${testStackId}`)
    // Precondition, not the assertion under test: if the engine were somehow
    // unavailable this locator would time out, so ensureBackupEngine() has
    // already named that cause before we get here.
    await expect(backupSwitch).toBeVisible()

    await clickAwaitingPolicyPut(page, () => backupSwitch.click())
    expect(
      await readPolicyEnabled(request, testStackId),
      'after one click the server-side policy must be the inverse of its starting value',
    ).toBe(!initial)

    await clickAwaitingPolicyPut(page, () => backupSwitch.click())
    expect(
      await readPolicyEnabled(request, testStackId),
      'after a second click the server-side policy must be back to its starting value',
    ).toBe(initial)
  })

  // ── 003: the toggle does not navigate the row it lives in ──────────────────

  test('DASH-BACKUPS-PW-003: clicking the toggle does not navigate away from the dashboard', async ({
    page,
    request,
  }) => {
    const initial = await readPolicyEnabled(request, testStackId)

    await loginIfNeeded(page, '/')

    const backupSwitch = page.getByTestId(`backup-switch-${testStackId}`)
    await expect(backupSwitch).toBeVisible()

    // Captured, not hardcoded: the dashboard's URL carries the active tab as a
    // search param, so the literal string depends on which tab is showing.
    const urlBeforeSwitch = page.url()
    await clickAwaitingPolicyPut(page, () => backupSwitch.click())
    expect(
      page.url(),
      'clicking the Auto backup switch must not follow the row=link navigation ' +
        '(StacksTab.tsx:139-145 wraps the cell in stopRowNavigation)',
    ).toBe(urlBeforeSwitch)

    // Second click: restores the policy AND checks the other transition
    // direction, since the two clicks travel opposite edges of the switch.
    await clickAwaitingPolicyPut(page, () => backupSwitch.click())
    expect(page.url(), 'the second click must not navigate either').toBe(urlBeforeSwitch)
    expect(
      await readPolicyEnabled(request, testStackId),
      'the policy must be left as it was found',
    ).toBe(initial)

    // ── The stop-policy Select, folded in here rather than as a fourth test ──
    // BackupToggle.tsx:132 renders it only while backup is ENABLED, and no unit
    // test anywhere exercises it: StacksTab's tests stub BackupToggle out, so
    // the Select inside the stopRowNavigation wrapper is invisible to them.
    const enabledForSelect = initial
      ? Promise.resolve()
      : clickAwaitingPolicyPut(page, () => backupSwitch.click())
    await enabledForSelect

    const stopPolicyTrigger = page.getByTestId(`backup-stop-policy-${testStackId}`)
    await expect(stopPolicyTrigger).toBeVisible()
    const urlBeforeSelect = page.url()
    await stopPolicyTrigger.click()
    // Positive bound: the listbox is open. Without this the URL check below
    // would run before any navigation a broken build would have started.
    await expect(page.getByTestId('backup-stop-policy-stop')).toBeVisible()
    expect(
      page.url(),
      'opening the stop-policy Select must not follow the row=link navigation',
    ).toBe(urlBeforeSelect)
    // Close without choosing: selecting an item would fire another policy PUT.
    await page.keyboard.press('Escape')

    if (!initial) {
      await clickAwaitingPolicyPut(page, () => backupSwitch.click())
    }
    expect(
      await readPolicyEnabled(request, testStackId),
      'the policy must be left as it was found',
    ).toBe(initial)
  })
})
