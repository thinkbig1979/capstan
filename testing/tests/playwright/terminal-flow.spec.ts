/**
 * Terminal flow E2E — Playwright spec
 *
 * Covers the stack terminal WebSocket contract end-to-end: start the fixture
 * stack, open a shell into a running container via BOTH entry points (the
 * Overview row's Shell action and the Terminal tab's container dropdown), and
 * prove the session is a real duplex PTY by running a command and reading its
 * output back out of xterm's DOM rows.
 *
 * WHY THIS EXISTS (2026-08-21, PR #219): the frontend has always dialed
 * /ws/terminal/:id/:container with the docker container ID, while the
 * stack-membership check added in 96e4d23 matched container NAMES only — so
 * every terminal connect was denied with "Container does not belong to this
 * stack". Both sides' unit tests passed against their own assumption; only a
 * real connect crosses the contract, and nothing exercised one. This spec is
 * that connect. If it starts failing with an immediate disconnect, check the
 * membership check in backend/internal/handlers/terminal.go first.
 *
 * BACKEND REQUIREMENTS (same job as backup-flow.spec.ts):
 *   - AUTH_DISABLED=true backend with the `test-app` stack fixture seeded
 *   - a real Docker daemon: the spec starts the stack (nginx:1.25-alpine,
 *     BusyBox sh — the "/ #" prompt asserted below is its root prompt)
 *
 * SELECTOR CONTRACT (recon-verified 2026-08-21):
 *   - StackPage header:  button "Start" / "Stop" (disabled tracks stack state)
 *   - ContainerList row: button "Shell: <container-name>"
 *   - TerminalToolbar:   combobox (placeholder "Select container"),
 *                        button "Disconnect terminal" — rendered only while
 *                        the session is CONNECTED, so it doubles as the
 *                        connect signal
 *   - xterm:             textbox "Terminal input" (helper textarea),
 *                        .xterm-rows (DOM renderer — terminal text is
 *                        readable innerText)
 *
 * Uses ONE shared page across the serial block: the backend caps WebSocket
 * connections at 10 per user (everyone is "anonymous" with AUTH_DISABLED) and
 * sockets from abandoned pages linger until the 60s read deadline, so a page
 * per test can exhaust the budget and produce false disconnects.
 */

import { test, expect, Page, BrowserContext, APIRequestContext } from 'playwright/test'

// ─── Config ──────────────────────────────────────────────────────────────────

const BASE_URL = process.env.CAPSTAN_BASE_URL ?? 'http://localhost:3001'
const API_URL = process.env.CAPSTAN_API_URL ?? 'http://localhost:5001'
const TEST_USER = process.env.CAPSTAN_TEST_USER ?? 'testadmin@example.com'
const TEST_PASSWORD = process.env.CAPSTAN_TEST_PASSWORD ?? 'TestPass123!'
const AUTH_DISABLED = (process.env.AUTH_DISABLED ?? 'false') === 'true'
const TEST_STACK_NAME = process.env.CAPSTAN_TEST_STACK ?? 'test-app'

let sharedContext: BrowserContext
let sharedPage: Page
let testStackId = ''
let csrfToken = ''

// ─── Helpers ─────────────────────────────────────────────────────────────────

/**
 * Log in via the UI; skip if AUTH_DISABLED.
 *
 * With AUTH_DISABLED (how CI runs this job) there is no login step, so this
 * navigates nowhere: beforeAll only needs the context's request fixture, and
 * TERM-PW-001's own goto is the page's first load. Booting /dashboard here
 * bought nothing and cost a full app boot — auth probe, settings/config,
 * resources/updates, dashboard/stats, backups/status, stacks — plus the events
 * and dashboard-metrics sockets, which also count against the backend's
 * per-user WebSocket cap this spec is already careful about.
 */
async function loginIfNeeded(page: Page): Promise<void> {
  if (AUTH_DISABLED) return
  await page.goto(`${BASE_URL}/login`)
  await page.waitForLoadState('networkidle')
  if (!page.url().includes('login')) return
  await page.getByLabel(/email/i).fill(TEST_USER)
  await page.getByLabel(/password/i).fill(TEST_PASSWORD)
  await page.getByRole('button', { name: /login|sign in/i }).click()
  await page.waitForURL((u) => !u.href.includes('login'), { timeout: 15_000 })
}

/**
 * Bootstrap the CSRF double-submit token (see backup-flow.spec.ts for the full
 * rationale): any GET sets the `capstan_csrf` cookie, whose value must be
 * echoed in X-CSRF-Token on mutations.
 */
async function ensureCsrf(request: APIRequestContext): Promise<void> {
  await request.get(`${API_URL}/api/v1/stacks`)
  const state = await request.storageState()
  const cookie = state.cookies.find((c) => c.name === 'capstan_csrf')
  if (cookie?.value) csrfToken = cookie.value
}

/** Resolve the fixture stack's id and current status from the API. */
async function fetchStack(request: APIRequestContext): Promise<{ id: string; status: string }> {
  const resp = await request.get(`${API_URL}/api/v1/stacks`)
  expect(resp.ok()).toBe(true)
  const body = await resp.json()
  const stacks: Array<{ id: string; projectName?: string; name?: string; status: string }> =
    Array.isArray(body) ? body : body.stacks ?? []
  const stack = stacks.find((s) => (s.projectName ?? s.name ?? s.id).includes(TEST_STACK_NAME))
  expect(stack, `Stack '${TEST_STACK_NAME}' not found`).toBeTruthy()
  return { id: stack!.id, status: stack!.status }
}

// ─── Suite ───────────────────────────────────────────────────────────────────

test.describe.serial('Terminal flow E2E', () => {
  test.beforeAll(async ({ browser }) => {
    sharedContext = await browser.newContext()
    sharedPage = await sharedContext.newPage()
    await loginIfNeeded(sharedPage)
    await ensureCsrf(sharedPage.request)

    // Arrange: the CI fixture is seeded stopped; the terminal needs a running
    // container. Start via the synchronous lifecycle API (docker compose up
    // returns when the container is up), then confirm the API agrees.
    const { id, status } = await fetchStack(sharedPage.request)
    testStackId = id
    if (status !== 'running') {
      const resp = await sharedPage.request.post(
        `${API_URL}/api/v1/stacks/${encodeURIComponent(testStackId)}/start`,
        { headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken } },
      )
      expect(resp.ok(), `stack start returned ${resp.status()}`).toBe(true)
    }
    await expect
      .poll(async () => (await fetchStack(sharedPage.request)).status, {
        // First CI run pulls nginx:1.25-alpine before the container can start.
        timeout: 120_000,
      })
      .toBe('running')
  })

  test.afterAll(async () => {
    await sharedContext?.close()
  })

  test('TERM-PW-001: Overview Shell action deep-links to a connected terminal', async () => {
    await sharedPage.goto(`${BASE_URL}/stacks/${encodeURIComponent(testStackId)}/overview`)
    await sharedPage.waitForLoadState('networkidle')

    // The services table renders one Shell action per container; the fixture
    // has exactly one service ("web" → container test-app-web-1).
    const shellButton = sharedPage.getByRole('button', { name: /^Shell: / }).first()
    await expect(shellButton).toBeVisible({ timeout: 15_000 })
    await shellButton.click()

    // The deep link carries the docker container ID — the exact value the
    // terminal WS membership check must accept (PR #219).
    await expect(sharedPage).toHaveURL(/\/terminal\?container=[0-9a-f]{12,}/)

    // "Disconnect terminal" renders only while the session is connected, so
    // its appearance IS the successful WS handshake + membership pass.
    await expect(
      sharedPage.getByRole('button', { name: 'Disconnect terminal' }),
    ).toBeVisible({ timeout: 20_000 })

    // BusyBox sh in the alpine container prints "/ #" once the PTY attaches.
    await expect(sharedPage.locator('.xterm-rows')).toContainText('/ #', { timeout: 15_000 })
  })

  test('TERM-PW-002: the shell is a real duplex PTY (command output round-trip)', async () => {
    // Still on the connected terminal from 001 (serial block, shared page).
    // $((21+21)) proves execution: the typed line contains the arithmetic
    // expression, but "capstan-e2e-42" only ever appears if the shell ran it —
    // a dead session echoing keystrokes back could not produce it.
    await sharedPage.getByRole('textbox', { name: 'Terminal input' }).click()
    await sharedPage.keyboard.type('echo capstan-e2e-$((21+21))')
    await sharedPage.keyboard.press('Enter')

    await expect(sharedPage.locator('.xterm-rows')).toContainText('capstan-e2e-42', {
      timeout: 15_000,
    })
  })

  test('TERM-PW-003: the Terminal tab dropdown connects (container-ID path contract)', async () => {
    // The dropdown keys its options on container.id, so selecting from it puts
    // the raw docker ID in the WS path — the historic flow that was denied for
    // three weeks.
    //
    // Navigate via the tabs, NOT goto(): a full reload boots the whole app
    // again — the toolbar only renders once the stack query reports running
    // containers, and a cold reload both races that one-shot fetch and opens a
    // fresh socket set against the backend's per-user WS cap (flaked exactly
    // this way on this test's first CI run). Clicking Overview unmounts the
    // connected terminal from 002; clicking Terminal remounts it clean, with
    // no ?container= param, so the dropdown starts from "Select container".
    // A tab click can land in the same tick as a React re-render (the freshly
    // mounted Overview streams metrics and refetches the stack, so the tree
    // churns) and be dispatched into a swapped-out node — observed locally as
    // a click that leaves the route unchanged. Retry the click until the
    // route actually changes instead of asserting on a single shot.
    const clickTabUntil = async (name: string, url: RegExp) => {
      await expect(async () => {
        await sharedPage.getByRole('tab', { name }).click()
        await expect(sharedPage).toHaveURL(url, { timeout: 2_000 })
      }).toPass({ timeout: 20_000 })
    }
    await clickTabUntil('Overview', /\/overview$/)
    await clickTabUntil('Terminal', /\/terminal$/)

    const containerSelect = sharedPage
      .getByRole('tabpanel', { name: 'Terminal' })
      .getByRole('combobox')
    await expect(containerSelect).toBeVisible({ timeout: 15_000 })
    await containerSelect.click()
    await sharedPage.getByRole('option', { name: new RegExp(TEST_STACK_NAME) }).first().click()

    await expect(
      sharedPage.getByRole('button', { name: 'Disconnect terminal' }),
    ).toBeVisible({ timeout: 20_000 })
    await expect(sharedPage.locator('.xterm-rows')).toContainText('/ #', { timeout: 15_000 })
  })
})
