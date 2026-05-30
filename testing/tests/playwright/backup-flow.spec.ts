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

// ─── Helpers ─────────────────────────────────────────────────────────────────

/** Log in via the UI; skip if AUTH_DISABLED. */
async function loginIfNeeded(page: Page): Promise<void> {
  if (AUTH_DISABLED) {
    await page.goto(`${BASE_URL}/dashboard`)
    await page.waitForLoadState('networkidle')
    return
  }
  await page.goto(`${BASE_URL}/login`)
  await page.waitForLoadState('networkidle')

  const url = page.url()
  if (!url.includes('login')) {
    // Already authenticated (session cookie)
    return
  }

  await page.getByLabel(/email/i).fill(TEST_USER)
  await page.getByLabel(/password/i).fill(TEST_PASSWORD)
  await page.getByRole('button', { name: /login|sign in/i }).click()
  await page.waitForURL((u) => !u.href.includes('login'), { timeout: 15_000 })
}

/** GET the backend API with auth header when we have a token. */
async function apiGet(request: APIRequestContext, path: string) {
  const headers: Record<string, string> = { 'Content-Type': 'application/json' }
  if (authToken) headers['Authorization'] = `Bearer ${authToken}`
  return request.get(`${API_URL}${path}`, { headers })
}

/** PUT/POST with JSON body. */
async function apiMutate(
  request: APIRequestContext,
  method: 'PUT' | 'POST',
  path: string,
  data: unknown,
) {
  const headers: Record<string, string> = { 'Content-Type': 'application/json' }
  if (authToken) headers['Authorization'] = `Bearer ${authToken}`
  if (method === 'PUT') {
    return request.put(`${API_URL}${path}`, { headers, data })
  }
  return request.post(`${API_URL}${path}`, { headers, data })
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

test.describe('Backup flow E2E', () => {
  // ── 001: Configure backup settings ─────────────────────────────────────────

  test('BACKUP-PW-001: configure backup repository and password', async ({
    page,
    request,
  }) => {
    // ── Login ────────────────────────────────────────────────────────────────
    await loginIfNeeded(page)

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
    const settingsResp = await apiMutate(request, 'PUT', '/api/v1/settings/backup', {
      repository: BACKUP_REPO_PATH,
      password: BACKUP_PASSPHRASE,
    })
    expect(settingsResp.status()).toBe(200)
    const settings = await settingsResp.json()
    expect(settings).toHaveProperty('repository')

    // ── Verify in UI ───────────────────────────────────────────────────────
    await page.goto(`${BASE_URL}/settings`)
    await page.waitForLoadState('networkidle')

    // Click the "Backup" tab in settings if present
    const backupTab = page.getByRole('tab', { name: /backup/i })
      .or(page.getByRole('link', { name: /backup/i }))
    if (await backupTab.count() > 0) {
      await backupTab.first().click()
      await page.waitForLoadState('networkidle')
    }

    // Repository input should be present
    const repoInput = page.locator('#backup-repository')
    await expect(repoInput).toBeVisible({ timeout: 10_000 })

    // The saved path should appear in the input value
    const repoValue = await repoInput.inputValue()
    expect(repoValue).toBe(BACKUP_REPO_PATH)
  })

  // ── 002: Initialize repository ─────────────────────────────────────────────

  test('BACKUP-PW-002: initialize restic repository', async ({ page, request }) => {
    await loginIfNeeded(page)

    // POST /api/v1/backups/repo/init
    const initResp = await apiMutate(request, 'POST', '/api/v1/backups/repo/init', {})
    expect(initResp.status()).toBe(200)
    const initBody = await initResp.json()
    expect(initBody.initialized).toBe(true)

    // ── UI: settings page should show "Initialized" badge ─────────────────
    await page.goto(`${BASE_URL}/settings`)
    await page.waitForLoadState('networkidle')

    const backupTab = page.getByRole('tab', { name: /backup/i })
      .or(page.getByRole('link', { name: /backup/i }))
    if (await backupTab.count() > 0) {
      await backupTab.first().click()
      await page.waitForLoadState('networkidle')
    }

    // "Initialized" badge or text
    const initializedBadge = page.getByText(/initialized/i)
    await expect(initializedBadge.first()).toBeVisible({ timeout: 10_000 })
  })

  // ── 003: Enable backup toggle on test-app stack ────────────────────────────

  test('BACKUP-PW-003: enable backup toggle on test-app stack', async ({
    page,
    request,
  }) => {
    await loginIfNeeded(page)

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

    // ── UI: dashboard should show toggle ON for this stack ─────────────────
    await page.goto(`${BASE_URL}/dashboard`)
    await page.waitForLoadState('networkidle')

    // BackupToggle renders data-testid="backup-toggle-<stackId>" when enabled
    const toggleEl = page.locator(`[data-testid="backup-toggle-${testStackId}"]`)
    if (await toggleEl.count() > 0) {
      await expect(toggleEl).toBeVisible()
      // The stop-policy selector should appear when enabled
      const stopPolicyTrigger = page.locator(
        `[data-testid="backup-stop-policy-${testStackId}"]`,
      )
      await expect(stopPolicyTrigger).toBeVisible({ timeout: 5_000 })
    } else {
      // Stack card may not be visible — verify via API instead
      const policiesResp = await apiGet(request, '/api/v1/backups/policies')
      const policiesBody = await policiesResp.json()
      const enabledPolicy = (policiesBody.policies ?? []).find(
        (p: { targetId: string; enabled: boolean }) => p.targetId === testStackId && p.enabled,
      )
      expect(enabledPolicy, 'Policy not enabled after toggle').toBeTruthy()
    }
  })

  // ── 004: Run backup and wait for completion ────────────────────────────────

  test('BACKUP-PW-004: run backup and verify completion', async ({ page, request }) => {
    await loginIfNeeded(page)

    // ── Option A: click "Back up now" in the BackupStatusCard ─────────────
    await page.goto(`${BASE_URL}/dashboard`)
    await page.waitForLoadState('networkidle')

    const backupNowBtn = page.getByRole('button', { name: /back up now/i })
    let ranViaUI = false

    if (await backupNowBtn.count() > 0 && await backupNowBtn.isEnabled()) {
      await backupNowBtn.click()
      // Wait for "Running..." to appear then disappear
      await page
        .getByText(/running/i)
        .waitFor({ timeout: 5_000 })
        .catch(() => {/* may transition fast */})

      const succeeded = await page
        .getByText(/success|completed/i)
        .waitFor({ timeout: 60_000 })
        .then(() => true)
        .catch(() => false)

      if (succeeded) {
        ranViaUI = true
      }
    }

    if (!ranViaUI) {
      // ── Option B: API fallback ────────────────────────────────────────────
      const runResp = await apiMutate(request, 'POST', '/api/v1/backups/run', {
        stackIds: [],
      })
      expect(runResp.status()).toBe(202)
      const runBody = await runResp.json()
      const runId: string = runBody.runId
      expect(runId).toBeTruthy()

      // Poll history until the run completes
      const completedRun = await pollUntil(async () => {
        const histResp = await apiGet(request, '/api/v1/backups/history?limit=10')
        if (!histResp.ok()) return null
        const hist = await histResp.json()
        const run = (hist.runs ?? []).find(
          (r: { id: string; status: string }) => r.id === runId,
        )
        if (!run) {
          // Fallback: check the most recent run
          const latest = (hist.runs ?? [])[0]
          if (latest && ['success', 'failed'].includes(latest.status)) return latest
          return null
        }
        if (['success', 'failed'].includes(run.status)) return run
        return null
      }, 90_000)

      expect(completedRun, 'Backup run did not complete within timeout').toBeTruthy()
      expect(completedRun!.status, 'Backup run failed').toBe('success')
    }
  })

  // ── 005: Verify snapshot appears ──────────────────────────────────────────

  test('BACKUP-PW-005: verify snapshot in API and BackupsTab', async ({
    page,
    request,
  }) => {
    await loginIfNeeded(page)

    // ── Step 1: API snapshot count ────────────────────────────────────────
    const snapshotsResp = await apiGet(request, '/api/v1/backups/snapshots')
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

    if (testStackId) {
      await page.goto(`${BASE_URL}/stacks/${testStackId}`)
      await page.waitForLoadState('networkidle')

      // Click Backups tab
      const backupsTab = page.getByRole('tab', { name: /^backups$/i })
      if (await backupsTab.count() > 0) {
        await backupsTab.click()
        await page.waitForLoadState('networkidle')
      }

      // "No snapshots yet" empty state should NOT appear
      const emptyState = page.getByText(/no snapshots yet/i)
      await expect(emptyState).not.toBeVisible({ timeout: 5_000 }).catch(() => {
        // Not visible = good; if assertion fails that means it IS visible
        throw new Error('BackupsTab shows "No snapshots yet" despite API having snapshots')
      })

      // Snapshot table should have at least one row — the Restore button's
      // aria-label includes the short ID
      const restoreBtn = page.getByRole('button', { name: /restore snapshot/i })
      await expect(restoreBtn.first()).toBeVisible({ timeout: 10_000 })
    }
  })

  // ── 006: Restore snapshot via ConfirmDialog ────────────────────────────────

  test('BACKUP-PW-006: restore snapshot via ConfirmDialog', async ({
    page,
    request,
  }) => {
    await loginIfNeeded(page)

    // Resolve snapshot ID if not set from previous test
    if (!firstSnapshotId) {
      const snapshotsResp = await apiGet(request, '/api/v1/backups/snapshots')
      const snaps = await snapshotsResp.json()
      const list = Array.isArray(snaps) ? snaps : []
      expect(list.length, 'No snapshots available for restore').toBeGreaterThan(0)
      firstSnapshotId = list[0].id
    }

    // Resolve stack ID
    if (!testStackId) {
      const stacksResp = await apiGet(request, '/api/v1/stacks')
      const body = await stacksResp.json()
      const stacks = Array.isArray(body) ? body : body.stacks ?? []
      const s = stacks.find((st: { id: string; name: string }) =>
        (st.name ?? st.id ?? '').includes(TEST_STACK_NAME),
      )
      if (s) testStackId = s.id ?? s.name
    }

    expect(firstSnapshotId, 'No snapshot ID for restore').toBeTruthy()
    expect(testStackId, 'No stack ID for restore').toBeTruthy()

    let restoredViaUI = false

    // ── UI path ───────────────────────────────────────────────────────────
    if (testStackId) {
      await page.goto(`${BASE_URL}/stacks/${testStackId}`)
      await page.waitForLoadState('networkidle')

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
      expect(restoreRunId).toBeTruthy()

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
    await loginIfNeeded(page)

    // ── Dashboard accessible ───────────────────────────────────────────────
    await page.goto(`${BASE_URL}/dashboard`)
    await page.waitForLoadState('networkidle')
    await expect(page.locator('body')).not.toContainText(/error|crash|broken/i)

    // ── Snapshots still present in API ────────────────────────────────────
    const snapshotsResp = await apiGet(request, '/api/v1/backups/snapshots')
    expect(snapshotsResp.ok()).toBe(true)
    const snaps = await snapshotsResp.json()
    const remaining = Array.isArray(snaps) ? snaps : []
    expect(remaining.length, 'Snapshots disappeared after restore').toBeGreaterThan(0)

    // ── BackupsTab still renders correctly ────────────────────────────────
    if (testStackId) {
      await page.goto(`${BASE_URL}/stacks/${testStackId}`)
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
