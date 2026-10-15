/**
 * In-app help (agent-os-y2dh): the requirements only a real browser can prove.
 *
 *   AC3  offline      no request leaves the Capstan origin once the panel is opened (D91)
 *   AC4  page help    the panel opens on the topic of the page it was opened from
 *   AC10 field help   a hint's "Learn more" lands on the section, popover closed
 *   AC12 keyboard     `?` toggles the panel but is just a character in editors and the terminal
 *   AC13 a11y         axe wcag2a/wcag2aa on the open panel, light and dark, 1280 and 375 wide
 *   AC14 mobile       full-width panel, nothing scrolls sideways, 44px Close button
 *
 * Prerequisites (same backend as a11y.spec.ts and terminal-flow.spec.ts):
 *   - App on CAPSTAN_BASE_URL (default http://localhost:3001), backend on
 *     CAPSTAN_API_URL (default http://localhost:5001), served from a production build.
 *   - AUTH_DISABLED=true (backend and this process).
 *   - A stack named CAPSTAN_TEST_STACK (default test-app).
 *   - A Docker daemon, for HELP-PW-009 only (a shell in the stack's container).
 *
 * Run:
 *   AUTH_DISABLED=true npx playwright test help --retries=0
 *
 * LEAVES THE SHARED FIXTURE AS IT FOUND IT. This spec runs in the same CI job
 * as backup-flow and dashboard-backups against the same test-app stack.
 *   - The env-editor case needs a stack that HAS an env file, and the API has
 *     no way to delete one from test-app. So it creates its own throwaway stack
 *     (SCRATCH_STACK) and deletes it afterwards.
 *   - The terminal case needs a running container. The stack's status is read
 *     first; it is stopped again afterwards only if this spec started it.
 *
 * NO FIXED SLEEPS. Every wait is a locator assertion, a poll, or
 * waitForInvalidationSettle.
 */

import { test, expect, Page, APIRequestContext, Locator } from 'playwright/test'
import { waitForInvalidationSettle } from './helpers/network-settle'
import { classify, describeFindings, runAxe, type Scheme } from './helpers/axe-scan'

const BASE_URL = process.env.CAPSTAN_BASE_URL ?? 'http://localhost:3001'
const API_URL = process.env.CAPSTAN_API_URL ?? 'http://localhost:5001'
const AUTH_DISABLED = (process.env.AUTH_DISABLED ?? 'false') === 'true'
const TEST_STACK_NAME = process.env.CAPSTAN_TEST_STACK ?? 'test-app'
const SCRATCH_STACK = 'help-pw-scratch'

const DESKTOP = { width: 1280, height: 800 }
const MOBILE = { width: 375, height: 812 }

let stackId = ''
let scratchId = ''
// Pre-state of the shared stack, restored in afterAll.
let stackWasRunning = false
let startedStackHere = false
let csrfToken = ''

// ─── Helpers ─────────────────────────────────────────────────────────────────

const helpButton = (page: Page): Locator => page.getByTestId('header-help-button')
const panel = (page: Page): Locator => page.getByTestId('help-panel')
const topicHeading = (page: Page): Locator => page.getByTestId('help-panel-topic-heading')

async function openAndSettle(page: Page, url: string): Promise<void> {
  await page.goto(url)
  await expect(page.locator('main').first()).toBeVisible()
  await waitForInvalidationSettle(page)
}

async function openPanel(page: Page): Promise<void> {
  await helpButton(page).click()
  await expect(panel(page)).toBeVisible()
}

/** Open the panel from the header and wait for the library to render a topic. */
async function openPanelOnTopic(page: Page): Promise<void> {
  await openPanel(page)
  await expect(topicHeading(page)).toBeVisible()
}

/** The CSRF double-submit token: any GET sets `capstan_csrf`, mutations echo it. */
async function ensureCsrf(request: APIRequestContext): Promise<void> {
  await request.get(`${API_URL}/api/v1/stacks`)
  const state = await request.storageState()
  const cookie = state.cookies.find((c) => c.name === 'capstan_csrf')
  if (cookie?.value) csrfToken = cookie.value
}

interface StackRow {
  id: string
  name?: string
  projectName?: string
  status: string
}

async function listStacks(request: APIRequestContext): Promise<StackRow[]> {
  const resp = await request.get(`${API_URL}/api/v1/stacks`)
  expect(resp.status(), 'GET /api/v1/stacks').toBe(200)
  const body = await resp.json()
  return Array.isArray(body) ? body : (body.stacks ?? [])
}

const mutationHeaders = () => ({ 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken })

async function deleteScratchStacks(request: APIRequestContext): Promise<void> {
  for (const s of await listStacks(request)) {
    if ((s.projectName ?? s.name) !== SCRATCH_STACK) continue
    const resp = await request.delete(`${API_URL}/api/v1/stacks/${encodeURIComponent(s.id)}?confirm=true`, {
      headers: mutationHeaders(),
    })
    expect(resp.ok(), `delete scratch stack returned ${resp.status()}`).toBe(true)
  }
}

// ─── Suite ───────────────────────────────────────────────────────────────────

test.describe('help: in-app help panel', () => {
  test.beforeAll(async ({ request }) => {
    expect(
      AUTH_DISABLED,
      'help.spec.ts needs a backend started with AUTH_DISABLED=true, and AUTH_DISABLED=true in this process',
    ).toBe(true)
    await ensureCsrf(request)

    const stacks = await listStacks(request)
    const stack = stacks.find((s) => (s.projectName ?? s.name ?? s.id).includes(TEST_STACK_NAME))
    expect(stack, `Stack '${TEST_STACK_NAME}' not found`).toBeTruthy()
    stackId = stack!.id
    stackWasRunning = stack!.status === 'running'

    // A leftover from an aborted run is ours (the name is reserved for this spec).
    await deleteScratchStacks(request)
    const created = await request.post(`${API_URL}/api/v1/stacks`, {
      headers: mutationHeaders(),
      data: {
        name: SCRATCH_STACK,
        composeContent: 'services:\n  web:\n    image: scratch-never-pulled:0\n',
        envContent: 'HELP_PW=1\n',
        deploy: false,
      },
    })
    expect(created.status(), `create scratch stack: ${await created.text()}`).toBe(201)
    const scratch = (await listStacks(request)).find((s) => (s.projectName ?? s.name) === SCRATCH_STACK)
    expect(scratch, 'scratch stack missing after create').toBeTruthy()
    scratchId = scratch!.id
  })

  test.afterAll(async ({ request }) => {
    // Restore both pre-states even when a test failed part-way.
    await ensureCsrf(request)
    await deleteScratchStacks(request)
    if (startedStackHere && !stackWasRunning) {
      const resp = await request.post(`${API_URL}/api/v1/stacks/${encodeURIComponent(stackId)}/stop`, {
        headers: mutationHeaders(),
      })
      expect(resp.ok(), `stop ${TEST_STACK_NAME} returned ${resp.status()}`).toBe(true)
    }
  })

  // ─── AC3: offline ──────────────────────────────────────────────────────────

  test('HELP-PW-001: opening, reading and searching help makes no request off the Capstan origin', async ({
    page,
  }) => {
    const origin = new URL(BASE_URL).origin
    const seen: string[] = []
    page.on('request', (r) => seen.push(r.url()))

    await openAndSettle(page, '/settings/backup')
    const clicked = seen.length // everything from the help button click onwards

    await openPanelOnTopic(page)
    // An in-doc link: Backup section -> "Configuring backups".
    await page.getByTestId('help-panel-body').getByRole('button', { name: 'Configuring backups' }).click()
    await expect(topicHeading(page)).toHaveText('Configuring Backups')
    // A guide chosen from the index.
    await page.getByRole('button', { name: 'All guides' }).click()
    await page.getByTestId('help-panel-index').getByRole('button', { name: 'Configuration Reference' }).click()
    await expect(topicHeading(page)).toHaveText('Configuration Reference')
    // A search and one of its results.
    await page.getByTestId('help-panel-search-input').fill('retention')
    const results = page.getByTestId('help-search-result')
    await expect(results.first()).toBeVisible()
    await results.first().click()
    await expect(page.getByTestId('help-search-result')).toHaveCount(0)
    await waitForInvalidationSettle(page)

    const after = seen.slice(clicked)
    // Control: the recorder is live. The docs chunk loads lazily on the first
    // open, so a clean run has same-origin requests after the click.
    expect(after.length, 'no request recorded after the help button click: the recorder saw nothing').toBeGreaterThan(0)
    const offOrigin = after.filter((u) => new URL(u).origin !== origin)
    expect(offOrigin, `requests off ${origin} after opening help:\n${offOrigin.join('\n')}`).toEqual([])
    // CI serves the app with the vite dev server, which loads each bundled doc as a
    // module (`/@fs/…/docs/x.md?import&raw`, observed 2026-10-15). That is the bundle
    // arriving, not the app fetching a doc, so `?import` module loads are not counted.
    const markdown = after.filter((u) => {
      const url = new URL(u)
      return url.pathname.endsWith('.md') && !url.searchParams.has('import')
    })
    expect(markdown, `.md files requested at runtime:\n${markdown.join('\n')}`).toEqual([])
  })

  // ─── AC4: page help per route family ───────────────────────────────────────

  // The Volumes row asserts on the heading text only: the Docker host work may
  // move the view to another URL and re-key its topic, and neither is what
  // this proves. The user-visible promise is "help from this page says Volumes".
  const PAGE_TOPICS: Array<{ id: string; url: () => string; heading: string }> = [
    { id: 'HELP-PW-002', url: () => '/?tab=volumes', heading: 'Volumes' },
    { id: 'HELP-PW-003', url: () => `/stacks/${encodeURIComponent(stackId)}/logs`, heading: 'Logs' },
    { id: 'HELP-PW-004', url: () => '/settings/backup', heading: 'Backup' },
  ]
  for (const t of PAGE_TOPICS) {
    test(`${t.id}: help from the ${t.heading} page opens on "${t.heading}"`, async ({ page }) => {
      await openAndSettle(page, t.url())
      await openPanel(page)
      await expect(topicHeading(page)).toHaveText(t.heading)
    })
  }

  // ─── AC10: field help ──────────────────────────────────────────────────────

  test('HELP-PW-005: Retention "Learn more" focuses "Retention and pruning" and closes the popover', async ({
    page,
  }) => {
    await openAndSettle(page, '/settings/backup')
    await page.getByRole('button', { name: 'Help: Retention', exact: true }).click()
    const learnMore = page.getByTestId('help-hint-learn-more-button')
    await expect(learnMore).toBeVisible()
    await learnMore.click()

    await expect(panel(page)).toBeVisible()
    await expect(topicHeading(page)).toHaveText('Retention and pruning')
    // Focus is on the section heading inside the doc, not on the (i) or the panel title.
    const heading = page.getByTestId('help-panel-body').getByRole('heading', { name: 'Retention and pruning' })
    await expect(heading).toBeFocused()
    // The popover is gone: its only button is not in the DOM.
    await expect(learnMore).toHaveCount(0)
  })

  // ─── AC12: keyboard ────────────────────────────────────────────────────────

  test('HELP-PW-006: ? on the page body toggles the panel', async ({ page }) => {
    await openAndSettle(page, '/settings/backup')
    await expect(panel(page)).toHaveCount(0)
    await page.keyboard.press('?')
    await expect(panel(page)).toBeVisible()
    await page.keyboard.press('?')
    await expect(panel(page)).toHaveCount(0)
  })

  test('HELP-PW-007: Escape closes the panel and returns focus to the header button', async ({ page }) => {
    await openAndSettle(page, '/settings/backup')
    await openPanel(page)
    await expect(panel(page).locator(':focus')).toHaveCount(1) // focus moved into the panel
    await page.keyboard.press('Escape')
    await expect(panel(page)).toHaveCount(0)
    await expect(helpButton(page)).toBeFocused()
  })

  test('HELP-PW-008: ? typed into the compose editor and the env editor is a character, not a shortcut', async ({
    page,
  }) => {
    await openAndSettle(page, `/stacks/${encodeURIComponent(scratchId)}/editor`)

    // CodeMirror: a contentEditable.
    const cm = page.locator('.cm-content').first()
    await expect(cm).toBeVisible()
    await cm.click()
    await page.keyboard.press('Control+End')
    await page.keyboard.type('?')
    await expect(cm).toContainText('?')
    await expect(panel(page)).toHaveCount(0)

    // The env editor's raw view: a textarea.
    await page.getByRole('tab', { name: 'Raw Editor' }).click()
    const raw = page.getByPlaceholder('KEY=value')
    await expect(raw).toBeVisible()
    await raw.click()
    await page.keyboard.press('Control+End')
    await page.keyboard.type('?')
    await expect(raw).toHaveValue(/\?/)
    await expect(panel(page)).toHaveCount(0)
  })

  test('HELP-PW-009: ? typed into the terminal reaches the shell and does not open the panel', async ({
    page,
    request,
  }) => {
    await ensureCsrf(request)
    if (!stackWasRunning) {
      startedStackHere = true // set first: afterAll must stop it even if the start half-fails
      const resp = await request.post(`${API_URL}/api/v1/stacks/${encodeURIComponent(stackId)}/start`, {
        headers: mutationHeaders(),
      })
      expect(resp.ok(), `stack start returned ${resp.status()}`).toBe(true)
    }
    await expect
      .poll(async () => (await listStacks(request)).find((s) => s.id === stackId)?.status, {
        timeout: 120_000, // the first run may pull the image
      })
      .toBe('running')

    await openAndSettle(page, `/stacks/${encodeURIComponent(stackId)}/overview`)
    await page.getByRole('button', { name: /^Shell: / }).first().click()
    await expect(page.getByRole('button', { name: 'Disconnect terminal' })).toBeVisible({ timeout: 20_000 })
    const rows = page.locator('.xterm-rows')
    await expect(rows).toContainText('/ #', { timeout: 15_000 })

    // The prompt is painted before the PTY reads input (terminal-flow.spec.ts,
    // waitForTerminalReady), so prove the shell is reading before the real check.
    const input = page.getByRole('textbox', { name: 'Terminal input' })
    let attempt = 0
    await expect(async () => {
      attempt += 1
      const sentinel = `help-ready-${Date.now()}-${attempt}`
      await input.click()
      await page.keyboard.type(`echo ${sentinel}-$((1+1))`)
      await page.keyboard.press('Enter')
      await expect(rows).toContainText(`${sentinel}-2`, { timeout: 2_000 })
    }).toPass({ timeout: 15_000 })

    await input.click()
    await page.keyboard.type('?')
    // The shell echoes the typed character after the prompt: it was input.
    await expect(rows).toContainText(/\/ # \?/)
    await expect(panel(page)).toHaveCount(0)
  })

  // ─── AC13: accessibility ───────────────────────────────────────────────────

  const A11Y_MATRIX: Array<{ label: string; scheme: Scheme; viewport: { width: number; height: number } }> = [
    { label: 'light-1280', scheme: 'light', viewport: DESKTOP },
    { label: 'dark-1280', scheme: 'dark', viewport: DESKTOP },
    { label: 'light-375', scheme: 'light', viewport: MOBILE },
    { label: 'dark-375', scheme: 'dark', viewport: MOBILE },
  ]
  for (const cfg of A11Y_MATRIX) {
    test.describe(`axe ${cfg.label}`, () => {
      test.use({ colorScheme: cfg.scheme, viewport: cfg.viewport })

      test(`HELP-PW-A11Y-${cfg.label}: the open panel on /settings/backup has no critical or serious violation`, async ({
        page,
      }) => {
        await openAndSettle(page, '/settings/backup')
        await openPanelOnTopic(page)
        await waitForInvalidationSettle(page)
        // No allowlist: a violation in the panel is a defect to file, not to waive.
        const { blocking, other } = classify(await runAxe(page), 'help-panel', cfg.scheme)
        for (const o of other) test.info().annotations.push({ type: 'non-blocking', description: o })
        expect(
          blocking.length,
          `${blocking.length} critical/serious violation(s) with help open (${cfg.label}):\n` +
            describeFindings(blocking),
        ).toBe(0)
      })
    })
  }

  // ─── AC14: mobile ──────────────────────────────────────────────────────────

  test.describe('mobile 375', () => {
    test.use({ viewport: MOBILE })

    test('HELP-PW-014: full-width panel, no sideways scroll with a table open, 44px Close button', async ({
      page,
    }) => {
      await openAndSettle(page, '/settings/backup')
      await openPanelOnTopic(page)
      await page.getByRole('button', { name: 'All guides' }).click()
      await page.getByTestId('help-panel-index').getByRole('button', { name: 'Configuration Reference' }).click()
      await expect(topicHeading(page)).toHaveText('Configuration Reference')
      await expect(page.getByTestId('help-panel-body').locator('table').first()).toBeVisible()

      const box = await panel(page).boundingBox()
      expect(box, 'panel has no box').not.toBeNull()
      // Exactly the viewport: the panel is anchored right-0, so one wider than
      // the screen grows off its LEFT edge, which scrollWidth below cannot see.
      expect(
        { x: Math.round(box!.x), width: Math.round(box!.width) },
        `panel box at ${MOBILE.width}px: ${JSON.stringify(box)}`,
      ).toEqual({ x: 0, width: MOBILE.width })

      const scroll = await page.evaluate(() => {
        const el = document.scrollingElement!
        return { scrollWidth: el.scrollWidth, clientWidth: el.clientWidth }
      })
      expect(scroll.scrollWidth, `page scrolls sideways: ${JSON.stringify(scroll)}`).toBeLessThanOrEqual(
        scroll.clientWidth,
      )

      const close = await page.getByTestId('help-panel-close-button').boundingBox()
      expect(close, 'Close button has no box').not.toBeNull()
      expect(close!.width, 'Close width').toBeGreaterThanOrEqual(44)
      expect(close!.height, 'Close height').toBeGreaterThanOrEqual(44)
    })
  })
})
