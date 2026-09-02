/**
 * Backup Flow E2E — Playwright spec
 *
 * Full round-trip: configure backup settings → enable toggle on test-app stack
 * → run a backup → verify snapshot in BackupsTab → restore via ConfirmDialog
 * → verify post-restore state.
 *
 * Prerequisites (same as the bash harness):
 *   - App running: frontend on BASE_URL (default http://localhost:3001),
 *     backend on API_URL (default http://localhost:5001).
 *   - restic 0.18.0+ available to the backend process.
 *   - AUTH_DISABLED=true in backend env OR valid credentials set via
 *     CAPSTAN_TEST_USER / CAPSTAN_TEST_PASSWORD env vars.
 *
 * Run:
 *   npx playwright test testing/tests/playwright/backup-flow.spec.ts
 *   # or via playwright.config.ts:
 *   npx playwright test --grep backup-flow
 *
 * Note on selectors: Prefer aria-label, data-testid, and visible text over
 * CSS paths. Selectors are derived from the actual component source:
 *   - BackupStatusCard:  aria-label="Back up now"
 *   - BackupToggle:      data-testid="backup-toggle-<stackId>"
 *                        data-testid="backup-switch-<stackId>"
 *                        data-testid="backup-stop-policy-<stackId>"
 *   - BackupsTab:        aria-label="Restore snapshot <shortId>"
 *   - ConfirmDialog:     role="button", name="Restore"
 *   - BackupSettingsContent:
 *       id="backup-repository", id="backup-password"
 *       text "Initialize repository", text "Initialized"
 */

import { test, expect, Page, APIRequestContext } from 'playwright/test'

// ─── Config ──────────────────────────────────────────────────────────────────

const BASE_URL = process.env.CAPSTAN_BASE_URL ?? 'http://localhost:3001'
const API_URL = process.env.CAPSTAN_API_URL ?? 'http://localhost:5001'
const TEST_USER = process.env.CAPSTAN_TEST_USER ?? 'testadmin@example.com'
const TEST_PASSWORD = process.env.CAPSTAN_TEST_PASSWORD ?? 'TestPass123!'
const AUTH_DISABLED = (process.env.AUTH_DISABLED ?? 'false') === 'true'
const TEST_STACK_NAME = process.env.CAPSTAN_TEST_STACK ?? 'test-app'
// Backup repo path used in settings — must be writable by the backend process.
const BACKUP_REPO_PATH = process.env.CAPSTAN_BACKUP_REPO ?? '/tmp/capstan-e2e-restic-repo-playwright'
const BACKUP_PASSPHRASE = process.env.CAPSTAN_BACKUP_PASSPHRASE ?? 'capstan-e2e-playwright-passphrase'

// Shared state across tests in the describe block
let authToken = ''
let testStackId = ''
let firstSnapshotId = ''
// CSRF double-submit token. The backend (middleware/csrf.go) sets a
// `capstan_csrf` cookie on any GET and requires the same value echoed in the
// `X-CSRF-Token` header on every mutating request. The real UI (axios) does
// this automatically; Playwright's APIRequestContext does not, so we bootstrap
// it per test via ensureCsrf() below. Each test gets a fresh request context
// (fresh cookie jar), so this must run before the first mutation in each test.
let csrfToken = ''

// ─── Helpers ─────────────────────────────────────────────────────────────────

// Every `waitForLoadState('networkidle')` below is a RENDER wait, which is a
// fine thing to want. It is NOT a valid bound for a request-COUNT assertion:
// networkidle resolves 500ms after the last connection, while the app debounces
// its WS-driven react-query invalidations by 750ms (`scheduleInvalidations()` in
// frontend/src/hooks/useStackEvents.ts), so the refetch lands outside the window.
// To count requests use countMatchingRequests() plus waitForInvalidationSettle()
// from ./helpers/network-settle.ts; that tally refuses to be read unsettled.

/**
 * Show the Backup settings pane.
 *
 * SettingsPage is a master-detail layout: a left sidebar nav (one Link per
 * section, deep-linkable as /settings/<id>) plus a content pane that renders
 * only the active section. The backup fields (#backup-repository, the
 * "Initialized" badge, etc.) mount only when "Backup" is the active section, so
 * we click the sidebar "Backup" link. Falls back to the deep link if the nav
 * link isn't present (e.g. the mobile dropdown layout).
 */
async function expandBackupSection(page: Page): Promise<void> {
  const navLink = page.getByRole('link', { name: 'Backup', exact: true })
  if (await navLink.count() > 0) {
    await navLink.first().click()
    await page.waitForLoadState('networkidle')
    return
  }
  await page.goto(`${BASE_URL}/settings/backup`)
  await page.waitForLoadState('networkidle')
}

/**
 * Log in via the UI if auth is on, then land on `target`.
 *
 * `target` exists so this is the caller's ONE page load. Every `page.goto` here
 * re-bootstraps the whole app — the auth probe, settings/config,
 * resources/updates, dashboard/stats, backups/status, stacks, plus the events
 * and dashboard-metrics websockets — and CI runs AUTH_DISABLED=true, where
 * there is no login step at all. Landing on /dashboard and letting the caller
 * immediately navigate elsewhere therefore paid for a full boot that nothing
 * asserted on. Callers run their API-only setup (ensureCsrf, stack-id lookup,
 * the mutation under test) BEFORE calling this, so the single load already
 * reflects the state they are about to assert on.
 */
async function loginIfNeeded(page: Page, target = '/dashboard'): Promise<void> {
  if (AUTH_DISABLED) {
    await page.goto(`${BASE_URL}${target}`)
    await page.waitForLoadState('networkidle')
    return
  }
  await page.goto(`${BASE_URL}/login`)
  await page.waitForLoadState('networkidle')

  // A session cookie may have skipped the form entirely.
  if (page.url().includes('login')) {
    await page.getByLabel(/email/i).fill(TEST_USER)
    await page.getByLabel(/password/i).fill(TEST_PASSWORD)
    await page.getByRole('button', { name: /login|sign in/i }).click()
    await page.waitForURL((u) => !u.href.includes('login'), { timeout: 15_000 })
  }

  await page.goto(`${BASE_URL}${target}`)
  await page.waitForLoadState('networkidle')
}

/** GET the backend API with auth header when we have a token. */
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
 * the header we send on mutations always matches the cookie. Idempotent and
 * safe to call multiple times; only updates the token when a cookie is present.
 */
async function ensureCsrf(request: APIRequestContext): Promise<string> {
  await apiGet(request, '/api/v1/stacks')
  const state = await request.storageState()
  const cookie = state.cookies.find((c) => c.name === 'capstan_csrf')
  if (cookie?.value) csrfToken = cookie.value
  return csrfToken
}

/** PUT/POST with JSON body. Includes the CSRF header for the double-submit check. */
async function apiMutate(
  request: APIRequestContext,
  method: 'PUT' | 'POST',
  path: string,
  data: unknown,
) {
  const headers: Record<string, string> = { 'Content-Type': 'application/json' }
  if (authToken) headers['Authorization'] = `Bearer ${authToken}`
  if (csrfToken) headers['X-CSRF-Token'] = csrfToken
  if (method === 'PUT') {
    return request.put(`${API_URL}${path}`, { headers, data })
  }
  return request.post(`${API_URL}${path}`, { headers, data })
}

/**
 * Attach to a backup/restore operation's WebSocket and await its outcome.
 *
 * POST /backups/run and /backups/restore start the operation immediately, on
 * a detached goroutine, and return 202 + a wsUrl. A BackupRun DB record is
 * created with status="running" before that response is sent, so the run is
 * durable even if no WS client ever connects (backend/internal/handlers/
 * backup.go:675-679, backend/internal/services/backup_runner.go:350 —
 * `LaunchBackup` ends in `go reg.execBackup(...)`). Connecting the WS here
 * only lets us observe the run to completion, the same way the real UI does;
 * it does not start anything. We open it here and await the streamed
 * `{type:"done"}` event. Node 22 (the Playwright runner) exposes a global
 * WebSocket, so no extra dependency is needed.
 *
 * wsUrl is the backend-relative path (e.g. "/ws/backups/run/<id>"); it lives
 * under /api/v1 on the BACKEND. vite DOES proxy it (vite.config.ts, '/api' →
 * API_URL, ws: true) — but that proxy only exists for requests the browser
 * page makes through its own origin. This helper runs in the Node test
 * process, which has no vite origin to proxy through, so we always dial
 * API_URL directly, never BASE_URL.
 *
 * The terminal frame is `{type:"done", outcome:"success"|"partial"|"failed",
 * reason:"..."}` (backend/internal/handlers/backup.go:846,976-980) — there has
 * never been a boolean `success` field on this frame; `outcome` is returned
 * as-is and callers decide what counts as passing (see the two call sites
 * below — for these single-target runs, "partial" is treated as a failure,
 * not a lesser pass: backend/internal/services/backup.go:499-527,634-661
 * shows "partial" only fires here when the capstan-database self-backup
 * failed alongside an otherwise-successful stack backup, a real degradation).
 */
async function runViaWs(
  wsUrl: string,
  timeoutMs = 90_000,
): Promise<{ outcome: string; reason: string; events: string[] }> {
  const wsBase = API_URL.replace(/^http/, 'ws') + '/api/v1'
  return new Promise((resolve) => {
    const events: string[] = []
    const ws = new WebSocket(wsBase + wsUrl)
    let settled = false
    const finish = (outcome: string, reason = '') => {
      if (settled) return
      settled = true
      try { ws.close() } catch { /* already closing */ }
      resolve({ outcome, reason, events })
    }
    ws.onmessage = (m: MessageEvent) => {
      const raw = String(m.data)
      events.push(raw.slice(0, 200))
      try {
        const j = JSON.parse(raw)
        if (j.type === 'done') finish(j.outcome ?? 'failed', j.reason ?? '')
      } catch { /* non-JSON stream line */ }
    }
    // A clean close before a done event means the op failed/expired.
    ws.onclose = () => finish('failed', 'WebSocket closed before a done frame arrived')
    ws.onerror = () => { /* close handler resolves */ }
    setTimeout(() => finish('failed', `timed out after ${timeoutMs}ms`), timeoutMs)
  })
}

/** Poll a condition until it returns a truthy value or timeout expires. */
async function pollUntil<T>(
  fn: () => Promise<T | null>,
  timeoutMs = 60_000,
  intervalMs = 2_000,
): Promise<T | null> {
  const deadline = Date.now() + timeoutMs
  while (Date.now() < deadline) {
    const result = await fn()
    if (result) return result
    await new Promise((r) => setTimeout(r, intervalMs))
  }
  return null
}

// ─── Suite ───────────────────────────────────────────────────────────────────

// .serial, not plain describe. playwright.config.ts already pins workers: 1 and
// fullyParallel: false, so ordering holds either way; what .serial changes is
// RETRY behaviour. With retries: 1 in CI a failing test currently retries ALONE
// against module-level state (authToken, testStackId, firstSnapshotId) that
// earlier tests in this block set, so the retry runs against a half-built
// world. .serial replays the whole block instead.
test.describe.serial('Backup flow E2E', () => {
  // ── 001: Configure backup settings ─────────────────────────────────────────

  test('BACKUP-PW-001: configure backup repository and password', async ({
    page,
    request,
  }) => {
    // ── Obtain auth token for subsequent API calls ─────────────────────────
    if (!AUTH_DISABLED) {
      const loginResp = await request.post(`${API_URL}/api/v1/auth/login`, {
        data: { email: TEST_USER, password: TEST_PASSWORD },
      })
      if (loginResp.ok()) {
        const body = await loginResp.json()
        authToken = body.token ?? ''
      }
    }

    // ── Configure via API ──────────────────────────────────────────────────
    await ensureCsrf(request)
    const settingsResp = await apiMutate(request, 'PUT', '/api/v1/settings/backup', {
      repository: BACKUP_REPO_PATH,
      password: BACKUP_PASSPHRASE,
    })
    expect(settingsResp.status()).toBe(200)
    const settings = await settingsResp.json()
    expect(settings).toHaveProperty('repository')

    // ── Verify in UI ───────────────────────────────────────────────────────
    // Settings are already saved, so this first and only page load shows them.
    await loginIfNeeded(page, '/settings')

    // Backup settings live in a collapsible accordion section (not a tab). The
    // section is collapsed by default, so expand it via its chevron button
    // (aria-label "Expand Backup") before the fields render.
    await expandBackupSection(page)

    // Repository input should be present
    const repoInput = page.locator('#backup-repository')
    await expect(repoInput).toBeVisible()

    // The saved path should appear in the input value
    const repoValue = await repoInput.inputValue()
    expect(repoValue).toBe(BACKUP_REPO_PATH)
  })

  // ── 002: Initialize repository ─────────────────────────────────────────────

  test('BACKUP-PW-002: initialize restic repository', async ({ page, request }) => {
    await ensureCsrf(request)

    // POST /api/v1/backups/repo/init
    const initResp = await apiMutate(request, 'POST', '/api/v1/backups/repo/init', {})
    expect(initResp.status()).toBe(200)
    const initBody = await initResp.json()
    expect(initBody.initialized).toBe(true)

    // ── UI: settings page should show "Initialized" badge ─────────────────
    // Loaded after the init call, so the badge is present on first render.
    await loginIfNeeded(page, '/settings')

    await expandBackupSection(page)

    // Match the positive state exactly — "Not initialized" also contains the
    // substring "initialized", so an exact match avoids a false positive.
    const initializedBadge = page.getByText('Initialized', { exact: true })
    await expect(initializedBadge.first()).toBeVisible()
  })

  // ── 003: Enable backup toggle on test-app stack ────────────────────────────

  test('BACKUP-PW-003: enable backup toggle on test-app stack', async ({
    page,
    request,
  }) => {
    await ensureCsrf(request)

    // ── Resolve stack ID ──────────────────────────────────────────────────
    const stacksResp = await apiGet(request, '/api/v1/stacks')
    expect(stacksResp.ok()).toBe(true)
    const stacksBody = await stacksResp.json()
    const stacks: Array<{ id: string; name: string }> = Array.isArray(stacksBody)
      ? stacksBody
      : stacksBody.stacks ?? []

    const testStack = stacks.find((s) => (s.name ?? s.id ?? '').includes(TEST_STACK_NAME))
    expect(testStack, `Stack '${TEST_STACK_NAME}' not found`).toBeTruthy()
    testStackId = testStack!.id ?? testStack!.name

    // ── Enable via API ────────────────────────────────────────────────────
    const policyResp = await apiMutate(
      request,
      'PUT',
      `/api/v1/backups/policies/stack/${testStackId}`,
      { enabled: true, stopPolicy: 'stop' },
    )
    expect(policyResp.status()).toBe(200)
    const policy = await policyResp.json()
    expect(policy.enabled).toBe(true)

    // ── UI: the stack detail page shows the toggle ON for this stack ───────
    // BackupToggle mounts in StackDetail (frontend/src/components/stack/
    // StackDetail.tsx) and in the non-default Updates tab — never on the
    // dashboard. `/dashboard` is not even a route: App.tsx's catch-all
    // redirects it to `/`. This load used to go there, so the toggle was
    // absent every time and the assertions below were skipped in favour of an
    // API check that could not fail. The policy is already enabled, so the
    // first load renders the ON state.
    //
    // The stack id goes in the path raw. `~` is unreserved and `:` is legal in
    // a path segment, and the app's own link does the same
    // (frontend/src/components/layout/sidebar/StackRow.tsx).
    await loginIfNeeded(page, `/stacks/${testStackId}`)

    // BackupToggle renders data-testid="backup-toggle-<stackId>" when enabled.
    // Unconditional and retrying: a missing toggle must fail this test loudly.
    const toggleEl = page.locator(`[data-testid="backup-toggle-${testStackId}"]`)
    await expect(toggleEl).toBeVisible()

    // The stop-policy selector should appear when enabled
    const stopPolicyTrigger = page.locator(
      `[data-testid="backup-stop-policy-${testStackId}"]`,
    )
    await expect(stopPolicyTrigger).toBeVisible()
  })

  // ── 004: Run backup and wait for completion ────────────────────────────────

  // The backup is driven entirely through the UI now. The test still needs
  // `request` — not for setup, but to read back the durable history record
  // and correlate it to the runId this click kicks off (see below); no CSRF
  // bootstrap is needed for that, since it's a GET.
  test('BACKUP-PW-004: run backup and verify completion', async ({ page, request }) => {
    // ── Click "Back up now" in the BackupStatusCard ───────────────────────
    // BackupStatusCard mounts only in BackupSettingsContent (frontend/src/
    // components/settings/BackupSettingsContent.tsx), i.e. /settings/backup.
    // This load used to go to `/dashboard`, which is not a route at all, so the
    // button was never present and the whole UI path was silently replaced by
    // an API fallback that asserted nothing about the UI.
    await loginIfNeeded(page, '/settings/backup')

    // Unconditional: a missing or disabled button must fail this test loudly.
    // It is enabled once the restic repo is initialised, which BACKUP-PW-002
    // did earlier in this serial run.
    const backupNowBtn = page.getByRole('button', { name: /back up now/i })
    await expect(backupNowBtn).toBeVisible()
    await expect(backupNowBtn).toBeEnabled()

    // Armed (not awaited) before the click so we can't miss the response, and
    // read only after toBeDisabled below rather than in between — see there
    // for why. Matched on pathname, not on API_URL: the page dials BASE_URL
    // (:3001) and vite proxies /api through to API_URL (:5001), so a
    // predicate built from API_URL would match nothing the page itself sends.
    const kickoff = page.waitForResponse(
      (r) => r.request().method() === 'POST' && new URL(r.url()).pathname === '/api/v1/backups/run',
    )

    await backupNowBtn.click()

    // The click registered: the card is busy while the mutation is in flight
    // and then while its WebSocket streams the run.
    //
    // Nothing goes between click() and this assertion. toBeDisabled retries
    // UNTIL disabled rather than waiting a fixed amount, so awaiting the
    // kickoff response here first would burn the whole 15s expect budget if
    // the busy window had already closed by the time we got to it.
    await expect(backupNowBtn).toBeDisabled()

    // Pin this run's ID now, before anything else races ahead. This is the
    // correlation key the history check at the end of this test uses to pin
    // the observed outcome to THIS run rather than the most recent one.
    // Reading the response body here relies on nothing having navigated the
    // page between the click above and this await — a page.goto in between
    // would tear down the in-flight response listener.
    const kickoffResp = await kickoff
    expect(kickoffResp.status()).toBe(202)
    const { runId } = await kickoffResp.json()
    expect(runId, 'POST /backups/run did not return a runId').toBeTruthy()

    // "Live output" mounts only once the stream has delivered a line, so it
    // belongs to THIS click rather than to any earlier run.
    await expect(page.getByText('Live output')).toBeVisible({ timeout: 30_000 })

    // Busy clears only when the stream reports done — BackupStatusCard's
    // isBusy is (mutation pending || streaming === 'running') — and the done
    // handler then invalidates the backup-status query. Waiting for it is what
    // makes the Last-run assertions below describe this run.
    //
    // This wait is load-bearing, not decoration. OBSERVED while writing this
    // test: asserting the Success badge straight after the click passed ~150ms
    // later against a badge left over from a previous suite run, while restic
    // still had ~13s of work to go, and BACKUP-PW-005 then found no snapshots.
    await expect(backupNowBtn).toBeEnabled({ timeout: 90_000 })

    // The stream reached a terminal state: Clear renders only once there are
    // lines and the stream is no longer running.
    // exact: the RepositorySection further down the page also has a
    // "Clear saved password" button, which a substring match picks up too.
    await expect(page.getByRole('button', { name: 'Clear', exact: true })).toBeVisible()

    // Read the outcome off THIS run's stream, never off the Last-run badge.
    //
    // The badge is DB-backed, and useBackup's done handler only ISSUES the
    // status refetch — refetchHistory() awaits nothing — in the same React
    // commit that takes streaming.status out of 'running' and so clears
    // isBusy. The badge therefore still describes the PREVIOUS run for a whole
    // /api/v1/backups/status round trip, and that endpoint shells out to
    // restic twice (CheckRepository, RepoSizeBytes), so the window is a restic
    // round trip wide.
    //
    // OBSERVED, with a previous successful run in DATA_DIR and this stack's
    // directory chmod 000 to force a real failure: the stream said
    // "Backup run finished: status=failed ok=0 failed=1" while the badge still
    // read "Success", and asserting on the badge passed this test green on a
    // failed backup.
    //
    // The stream cannot be stale: connect() calls setLines([]) before it
    // streams, so every line under "Live output" belongs to this click. It is
    // also outcome-specific — useBackup appends "Backup completed
    // successfully." only on success, "Backup partially completed." on
    // partial, and "Error: ..." on failed and on interrupted. Asserting the
    // success line positively therefore fails on all three bad outcomes; the
    // Error check just ahead of it only buys a clearer message.
    await expect(page.getByText(/^Error:/)).toHaveCount(0)
    // This positive assertion depends on execBackup leaving dr.reason empty
    // on success: useBackup.ts:359's `msg.reason || 'Backup completed
    // successfully.'` falls through to the literal string only then, unlike
    // its restore/sync/dr-restore/prune siblings, which all set dr.reason on
    // their own success path (backend/internal/services/backup_runner.go).
    // The runId-correlated history check below is what keeps this test
    // honest if that asymmetry is ever "tidied up" — it checks the
    // DB-persisted status, not this reason-dependent UI string.
    await expect(page.getByText('Backup completed successfully.')).toBeVisible()

    // ── Correlate the outcome to THIS run, not the most recent one ─────────
    // The Last-run badge (BackupStatusCard.tsx's LastRunBadge) and
    // BackupToggle's status pill both read GetBackupRuns(1) — most recent —
    // so nothing above actually pins the observed outcome to the run this
    // test kicked off; a concurrent scheduler run would be indistinguishable.
    // /backups/history is a plain GetBackupRuns SELECT, unlike /backups/status
    // (which shells out to restic twice and would compete for the repo lock
    // BACKUP-PW-005's snapshot list needs next).
    //
    // This only pins WHICH run and that its job status is success — "success"
    // is a job status, not proof of coverage. A run that backed up zero
    // stacks would also report success. BACKUP-PW-005's snapshot count is
    // what proves test-app itself was actually backed up.
    const historyResp = await apiGet(request, '/api/v1/backups/history')
    expect(historyResp.ok()).toBe(true)
    const { runs } = (await historyResp.json()) as { runs: Array<{ id: string; status: string }> }
    // Never `if (run) expect(...)` — that's silently vacuous on a missed find,
    // which is exactly a failed/absent backup's signature. `?.status` fails
    // loudly on undefined instead.
    const thisRun = runs.find((r) => r.id === runId)
    expect(thisRun, `No history record found for runId ${runId}`).toBeTruthy()
    expect(thisRun?.status).toBe('success')
  })

  // ── 005: Verify snapshot appears ──────────────────────────────────────────

  test('BACKUP-PW-005: verify snapshot in API and BackupsTab', async ({
    page,
    request,
  }) => {
    // Resolve stack ID first — needed to scope the snapshot lookup below. The
    // repo also holds an automatic capstan-database self-backup snapshot
    // (backend/internal/services/backup.go:1240), so an unscoped snapshot
    // list can contain more than just this stack's own backups, and is not
    // safe to index by position (agent-os-5y9 Phase 1 finding).
    if (!testStackId) {
      // If previous test did not run in sequence, resolve stack ID
      const stacksResp = await apiGet(request, '/api/v1/stacks')
      const stacksBody = await stacksResp.json()
      const stacks = Array.isArray(stacksBody) ? stacksBody : stacksBody.stacks ?? []
      const s = stacks.find((st: { id: string; name: string }) =>
        (st.name ?? st.id ?? '').includes(TEST_STACK_NAME),
      )
      if (s) testStackId = s.id ?? s.name
    }
    expect(testStackId, 'Stack ID required to scope the snapshot lookup').toBeTruthy()

    // ── Step 1: API snapshot count ────────────────────────────────────────
    // Scoped via ?stackId — the backend filters server-side by restic tag
    // (backend/internal/handlers/backup.go:481), so this only ever returns
    // test-app's own snapshots, never the capstan-database self-backup one.
    const snapshotsResp = await apiGet(
      request,
      `/api/v1/backups/snapshots?stackId=${encodeURIComponent(testStackId)}`,
    )
    expect(snapshotsResp.ok()).toBe(true)
    const snapshots = await snapshotsResp.json()
    const snapshotList: Array<{ id: string; shortId?: string; tags?: string[] }> =
      Array.isArray(snapshots) ? snapshots : []

    expect(snapshotList.length, 'No snapshots found after backup').toBeGreaterThan(0)

    firstSnapshotId = snapshotList[0].id
    const shortId = snapshotList[0].shortId ?? snapshotList[0].id.slice(0, 8)
    // eslint-disable-next-line no-console
    console.log(`First snapshot: id=${firstSnapshotId}, shortId=${shortId}`)

    // ── Step 2: UI BackupsTab ─────────────────────────────────────────────
    // Backups is a section of the Activity tab since the phase-2 redesign;
    // deep-link straight to it (old /backups links redirect here too). This
    // is the test's only page load.
    await loginIfNeeded(page, `/stacks/${testStackId}/activity?view=backups`)

    // The inner Backups section tab (already active via the deep link).
    // Unconditional and retrying: `locator.count()` is a point-in-time check
    // with no auto-wait, so a not-yet-painted tablist would silently skip the
    // click; `locator.click()` auto-waits up to actionTimeout (30s). Clicking
    // an already-active trigger is harmless — it calls
    // setSearchParams({view:'backups'}, {replace:true}) with the same value,
    // no remount.
    const backupsTab = page.getByRole('tab', { name: /^backups$/i })
    await backupsTab.click()
    await page.waitForLoadState('networkidle')

    // "No snapshots yet" empty state should NOT appear
    const emptyState = page.getByText(/no snapshots yet/i)
    await expect(emptyState).not.toBeVisible({ timeout: 5_000 }).catch(() => {
      // Not visible = good; if assertion fails that means it IS visible
      throw new Error('BackupsTab shows "No snapshots yet" despite API having snapshots')
    })

    // Snapshot table should have at least one row — the Restore button's
    // aria-label includes the short ID
    // No timeout override here: playwright.config.ts's expect timeout of 15s
    // applies. This assertion's budget has to cover the page's whole boot,
    // not just the last hop — `waitForLoadState('networkidle')` above can and
    // does settle in the quiet gap before React fires its first query wave,
    // in which case every request the button depends on lands inside this
    // budget. OBSERVED idle, with a warm repo: 4.2s of it, gated by
    // /backups/snapshots. Two of those calls shell out to restic and
    // serialise on the repo lock, so the tail is much longer than the mean —
    // which is what made the old 10s override flaky.
    const restoreBtn = page.getByRole('button', { name: /restore snapshot/i })
    await expect(restoreBtn.first()).toBeVisible()
  })

  // ── 006: Restore snapshot via ConfirmDialog ────────────────────────────────

  test('BACKUP-PW-006: restore snapshot via ConfirmDialog', async ({
    page,
    request,
  }) => {
    await ensureCsrf(request)

    // Resolve stack ID first — the snapshot lookup below is scoped by it.
    if (!testStackId) {
      const stacksResp = await apiGet(request, '/api/v1/stacks')
      const body = await stacksResp.json()
      const stacks = Array.isArray(body) ? body : body.stacks ?? []
      const s = stacks.find((st: { id: string; name: string }) =>
        (st.name ?? st.id ?? '').includes(TEST_STACK_NAME),
      )
      if (s) testStackId = s.id ?? s.name
    }
    expect(testStackId, 'No stack ID for restore').toBeTruthy()

    // Resolve snapshot ID if not set from previous test. Scoped via ?stackId
    // for the same reason as BACKUP-PW-005 — the repo also holds the
    // capstan-database self-backup snapshot, and an unscoped list is not safe
    // to index by position (agent-os-5y9 Phase 1 finding).
    if (!firstSnapshotId) {
      const snapshotsResp = await apiGet(
        request,
        `/api/v1/backups/snapshots?stackId=${encodeURIComponent(testStackId)}`,
      )
      const snaps = await snapshotsResp.json()
      const list = Array.isArray(snaps) ? snaps : []
      expect(list.length, 'No snapshots available for restore').toBeGreaterThan(0)
      firstSnapshotId = list[0].id
    }

    expect(firstSnapshotId, 'No snapshot ID for restore').toBeTruthy()

    let restoredViaUI = false

    // ── UI path ───────────────────────────────────────────────────────────
    if (testStackId) {
      // Backups is a section of the Activity tab since the phase-2 redesign;
      // deep-link straight to it (old /backups links redirect here too). This
      // is the test's only page load.
      await loginIfNeeded(page, `/stacks/${testStackId}/activity?view=backups`)

      const backupsTab = page.getByRole('tab', { name: /^backups$/i })
      if (await backupsTab.count() > 0) {
        await backupsTab.click()
        await page.waitForLoadState('networkidle')
      }

      // Click any Restore button in the snapshots table
      const restoreBtn = page.getByRole('button', { name: /restore snapshot/i })
      if (await restoreBtn.count() > 0) {
        await restoreBtn.first().click()

        // ConfirmDialog appears — click the destructive "Restore" confirm button.
        // The ConfirmDialog renders confirmText="Restore" as a red/destructive button.
        const confirmBtn = page.getByRole('button', { name: /^restore$/i })
        await expect(confirmBtn).toBeVisible({ timeout: 5_000 })
        await confirmBtn.click()

        // RestoreProgress panel appears with "Restoring…"
        const restoringText = page.getByText(/restoring[…\.]/i)
        await expect(restoringText)
          .toBeVisible({ timeout: 5_000 })
          .catch(() => {/* may transition immediately */})

        // Wait for "Restore completed"
        const doneText = page.getByText(/restore completed/i)
        const restored = await doneText
          .waitFor({ timeout: 90_000 })
          .then(() => true)
          .catch(() => false)

        if (restored) {
          await expect(doneText).toBeVisible()
          restoredViaUI = true
        }
      }
    }

    if (!restoredViaUI) {
      // ── API fallback (confirm=true required by server) ─────────────────
      const restoreResp = await apiMutate(request, 'POST', '/api/v1/backups/restore', {
        stackId: testStackId,
        snapshotId: firstSnapshotId,
        confirm: true,
      })
      expect(restoreResp.status()).toBe(202)
      const restoreBody = await restoreResp.json()
      const restoreRunId: string = restoreBody.runId
      const restoreWsUrl: string = restoreBody.wsUrl
      expect(restoreRunId).toBeTruthy()
      expect(restoreWsUrl, 'restore response must include a wsUrl').toBeTruthy()

      // Connect the WS to actually run the restore and await completion.
      const wsResult = await runViaWs(restoreWsUrl)
      // "partial" fails here too — see the runViaWs doc comment for why.
      expect(
        wsResult.outcome,
        `Restore WS did not report success (outcome=${wsResult.outcome}, reason="${wsResult.reason}"). ` +
          `Last events: ${wsResult.events.slice(-3).join(' | ')}`,
      ).toBe('success')

      // Poll for completion
      const done = await pollUntil(async () => {
        const histResp = await apiGet(request, '/api/v1/backups/history?limit=10')
        if (!histResp.ok()) return null
        const hist = await histResp.json()
        const run = (hist.runs ?? []).find(
          (r: { id: string; status: string }) => r.id === restoreRunId,
        )
        if (run && ['success', 'failed'].includes(run.status)) return run
        const latest = (hist.runs ?? [])[0]
        if (latest && ['success', 'failed'].includes(latest.status)) return latest
        return null
      }, 90_000)

      expect(done, 'Restore did not complete within timeout').toBeTruthy()
      expect(done!.status, 'Restore failed').toBe('success')
    }
  })

  // ── 007: Post-restore verification ────────────────────────────────────────

  test('BACKUP-PW-007: post-restore state verification', async ({ page, request }) => {
    // ── Dashboard accessible ───────────────────────────────────────────────
    await loginIfNeeded(page, '/dashboard')
    // Look only for crash/error-boundary phrases — a bare word like "Error"
    // legitimately appears in the dashboard's status-filter chips, so the old
    // /error|crash|broken/i was too broad and matched normal UI chrome.
    await expect(page.locator('body')).not.toContainText(
      /something went wrong|application error|unhandled (error|exception|rejection)|cannot read propert|is not a function|chunkloaderror/i,
    )

    // ── Snapshots still present in API ────────────────────────────────────
    const snapshotsResp = await apiGet(request, '/api/v1/backups/snapshots')
    expect(snapshotsResp.ok()).toBe(true)
    const snaps = await snapshotsResp.json()
    const remaining = Array.isArray(snaps) ? snaps : []
    expect(remaining.length, 'Snapshots disappeared after restore').toBeGreaterThan(0)

    // ── BackupsTab still renders correctly ────────────────────────────────
    if (testStackId) {
      // Backups is a section of the Activity tab since the phase-2 redesign;
      // deep-link straight to it (old /backups links redirect here too).
      await page.goto(`${BASE_URL}/stacks/${testStackId}/activity?view=backups`)
      await page.waitForLoadState('networkidle')

      const backupsTab = page.getByRole('tab', { name: /^backups$/i })
      if (await backupsTab.count() > 0) {
        await backupsTab.click()
        await page.waitForLoadState('networkidle')
      }

      // No error message in the tab
      await expect(page.getByText(/failed to load snapshots/i)).not.toBeVisible()
    }
  })
})
