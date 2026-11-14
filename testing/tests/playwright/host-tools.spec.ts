/**
 * The dashboard's Host tab (agent-os-mi4r) in a real browser (agent-os-bugc):
 * navigation into and around it, its entry points, and the Free up space
 * dialog against a real Docker daemon.
 *
 * Prerequisites (same backend as a11y.spec.ts):
 *   - App on CAPSTAN_BASE_URL (default http://localhost:3001), backend on
 *     CAPSTAN_API_URL (default http://localhost:5001), AUTH_DISABLED=true in both.
 *   - A running stack named CAPSTAN_TEST_STACK (default test-app).
 *   - The `docker` CLI, talking to the backend's daemon.
 *
 * FIXTURES are named (helpers/docker-fixture.ts) and removed by name in
 * afterEach, even when the test fails.
 *
 * DESTRUCTIVE TESTS (HOST-FREE-001, HOST-FREE-003) click "Remove selected" for
 * real, which removes every unlabelled stopped container on the host (and, with
 * the defaults, unused images and build cache), not only the fixture. They run
 * only with CAPSTAN_E2E_DESTRUCTIVE=1, which the GitHub-hosted backup-flow job
 * sets (an ephemeral VM). Never set it on a developer machine or on the
 * self-hosted runner. HOST-FREE-002 intercepts the request instead, so it runs
 * everywhere.
 *
 * Free up takes the exclusive operation turn; playwright.config.ts runs specs
 * with workers: 1, so no stack operation runs beside it.
 *
 * Run:
 *   AUTH_DISABLED=true npx playwright test testing/tests/playwright/host-tools.spec.ts
 */

import { test, expect, type APIRequestContext, type Page } from 'playwright/test'
import { waitForInvalidationSettle } from './helpers/network-settle'
import {
  DESTRUCTIVE,
  DESTRUCTIVE_SKIP_REASON,
  LEFTOVER_CONTAINER,
  LEFTOVER_VOLUME,
  createLeftoverContainer,
  createLeftoverVolume,
  exists,
  removeLeftoverContainer,
  removeLeftoverVolume,
} from './helpers/docker-fixture'
import { execFileSync } from 'node:child_process'

const API_URL = process.env.CAPSTAN_API_URL ?? 'http://localhost:5001'
const AUTH_DISABLED = (process.env.AUTH_DISABLED ?? 'false') === 'true'
const TEST_STACK_NAME = process.env.CAPSTAN_TEST_STACK ?? 'test-app'

const HOST_CARDS = ['containers', 'images', 'volumes', 'networks', 'build-cache']
const FREE_SPACE_PATH = '**/api/v1/resources/host/free-space'

// ─── Helpers ─────────────────────────────────────────────────────────────────

interface StackRow {
  id: string
  name?: string
  projectName?: string
  status?: string
}

async function listStacks(request: APIRequestContext): Promise<StackRow[]> {
  const resp = await request.get(`${API_URL}/api/v1/stacks`)
  expect(resp.status(), 'GET /api/v1/stacks').toBe(200)
  const body = await resp.json()
  return Array.isArray(body) ? body : (body.stacks ?? [])
}

async function testStack(request: APIRequestContext): Promise<StackRow> {
  const stack = (await listStacks(request)).find((s) =>
    (s.projectName ?? s.name ?? s.id ?? '').includes(TEST_STACK_NAME),
  )
  expect(stack, `Stack '${TEST_STACK_NAME}' not found`).toBeTruthy()
  return stack!
}

/**
 * PUT with the CSRF double-submit header. The backend (middleware/csrf.go)
 * sets `capstan_csrf` on any GET; the request context does not echo it.
 */
async function apiPut(request: APIRequestContext, path: string, data: unknown) {
  await request.get(`${API_URL}/api/v1/stacks`)
  const csrf = (await request.storageState()).cookies.find((c) => c.name === 'capstan_csrf')?.value ?? ''
  const resp = await request.put(`${API_URL}${path}`, {
    data,
    headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf },
  })
  expect(resp.ok(), `PUT ${path} returned ${resp.status()}`).toBe(true)
  return resp.json()
}

async function openAndSettle(page: Page, url: string): Promise<void> {
  await page.goto(url)
  await expect(page.locator('main').first()).toBeVisible()
  await waitForInvalidationSettle(page)
}

function hostTabTrigger(page: Page) {
  return page.getByRole('tab', { name: /^Host/ })
}

async function expectOverview(page: Page): Promise<void> {
  await expect(page.getByRole('region', { name: 'Live' })).toBeVisible()
  await expect(page.getByRole('region', { name: 'Docker disk use' })).toBeVisible()
  for (const card of HOST_CARDS) {
    await expect(page.getByTestId(`host-card-${card}`)).toBeVisible()
  }
}

/** Opens Free up space from the Host tab's button and waits for its rows. */
async function openFreeUpDialog(page: Page) {
  await openAndSettle(page, '/?tab=host')
  const button = page.getByTestId('host-free-space-button')
  await expect(button).toBeEnabled()
  await button.click()
  const dialog = page.getByRole('dialog', { name: 'Free up space' })
  await expect(dialog).toBeVisible()
  await expect(dialog.getByTestId('free-space-row-containers')).toBeVisible()
  return dialog
}

function containerId(name: string): string {
  return execFileSync('docker', ['container', 'inspect', '--format', '{{.Id}}', name], { encoding: 'utf8' }).trim()
}

// ─── Specs ───────────────────────────────────────────────────────────────────

test.describe('Host tab', () => {
  // Desktop: the tab triggers and the header vitals are hidden below md / lg.
  test.use({ viewport: { width: 1280, height: 720 } })

  test.beforeAll(async ({ request }) => {
    expect(AUTH_DISABLED, 'host-tools.spec.ts needs AUTH_DISABLED=true in the backend and in this process').toBe(true)
    await testStack(request)
  })

  test.afterEach(() => {
    removeLeftoverContainer()
    removeLeftoverVolume()
  })

  test('HOST-NAV-001: the Host tab opens the overview, View all opens a list, Overview returns', async ({ page }) => {
    await openAndSettle(page, '/')
    await hostTabTrigger(page).click()
    await expect(page).toHaveURL(/\/\?tab=host$/)
    await expectOverview(page)

    await page.getByRole('link', { name: 'View all images' }).click()
    await expect(page).toHaveURL(/\/\?tab=host&view=images$/)
    await expect(page.getByRole('columnheader', { name: 'Repository Tags' })).toBeVisible()

    const views = page.getByRole('navigation', { name: 'Host views' })
    await expect(views.getByRole('link', { name: /^Images/ })).toHaveAttribute('aria-current', 'page')
    await views.getByRole('link', { name: 'Overview' }).click()
    await expect(page).toHaveURL(/\/\?tab=host$/)
    await expectOverview(page)
  })

  test('HOST-NAV-002: a view URL opens its list, an unknown view the overview, /host goes to /', async ({ page }) => {
    // A volume of our own, so the Volumes list has a table on a host with none.
    createLeftoverVolume()
    await openAndSettle(page, '/?tab=host&view=volumes')
    await expect(page.getByRole('columnheader', { name: 'Driver' })).toBeVisible()
    await expect(page.getByRole('cell', { name: LEFTOVER_VOLUME, exact: true })).toBeVisible()

    await openAndSettle(page, '/?tab=host&view=bogus')
    await expectOverview(page)
    await expect(
      page.getByRole('navigation', { name: 'Host views' }).getByRole('link', { name: 'Overview' }),
    ).toHaveAttribute('aria-current', 'page')

    await page.goto('/host')
    await expect(page).toHaveURL(/\/$/)
    await expect(page.locator('main').first()).toBeVisible()
    await expect(page.getByTestId('host-tab')).toHaveCount(0)
  })

  test('HOST-NAV-003: Settings "Open automatic cleanup" lands with the panel in view', async ({ page }) => {
    await openAndSettle(page, '/settings/docker-cleanup')
    await page.getByRole('link', { name: 'Open automatic cleanup' }).click()
    await expect(page).toHaveURL(/\/\?tab=host#automatic-cleanup$/)
    await expect(page.getByRole('heading', { name: 'Automatic cleanup' })).toBeVisible()
    // "In view" is the panel's top inside main's VISIBLE box: main's rect
    // clipped to the window. main alone is not enough: with the document
    // scrolling instead of main, main's rect runs past the window and any top
    // would count as inside it.
    await expect
      .poll(() =>
        page.evaluate(() => {
          const el = document.getElementById('automatic-cleanup')
          const main = el?.closest('main')
          if (!el || !main) return 'missing'
          const top = el.getBoundingClientRect().top
          const box = main.getBoundingClientRect()
          const visibleTop = Math.max(box.top, 0)
          const visibleBottom = Math.min(box.bottom, window.innerHeight)
          return top >= visibleTop - 1 && top < visibleBottom
            ? 'in view'
            : `top ${top} outside ${visibleTop}..${visibleBottom}`
        }),
      )
      .toBe('in view')
  })

  test('HOST-NAV-004: the header vitals link opens the Host tab', async ({ page }) => {
    await openAndSettle(page, '/settings/appearance')
    const link = page.getByTestId('header-vitals-link')
    await expect(link).toBeVisible()
    await link.click()
    await expect(page).toHaveURL(/\/\?tab=host$/)
    await expect(page.getByTestId('host-tab')).toBeVisible()
  })

  test.describe('with reclaimable space and a 1-byte alert threshold', () => {
    let priorThreshold: number | undefined

    test.beforeEach(async ({ request }) => {
      createLeftoverContainer()
      const resp = await request.get(`${API_URL}/api/v1/resources/cleanup/policy`)
      expect(resp.status(), 'GET cleanup policy').toBe(200)
      priorThreshold = (await resp.json()).alertThresholdBytes
      await apiPut(request, '/api/v1/resources/cleanup/policy', { alertThresholdBytes: 1 })
    })

    test.afterEach(async ({ request }) => {
      if (priorThreshold !== undefined) {
        await apiPut(request, '/api/v1/resources/cleanup/policy', { alertThresholdBytes: priorThreshold })
      }
    })

    test('HOST-ENTRY-001: the Host tab badge, the Stacks alert opening Free up, and a dismiss that survives reload', async ({
      page,
    }) => {
      await openAndSettle(page, '/')
      // A dismiss left over from an earlier run would hide the alert.
      await page.evaluate(() => localStorage.removeItem('ui-storage'))
      await openAndSettle(page, '/')

      await expect(hostTabTrigger(page).getByTestId('host-tab-badge')).toBeVisible()

      const alert = page.getByTestId('attention-host-space')
      await expect(alert).toBeVisible()
      await alert.getByRole('button', { name: /Free up space/ }).click()
      await expect(page).toHaveURL(/\/\?tab=host&free=1$/)
      await expect(page.getByRole('dialog', { name: 'Free up space' })).toBeVisible()

      await openAndSettle(page, '/')
      await expect(alert).toBeVisible()
      await alert.getByRole('button', { name: 'Dismiss for 7 days' }).click()
      await expect(alert).toHaveCount(0)

      await page.reload()
      // The badge shows once the host summary has loaded, which is what the
      // alert waits on too; then nothing may bring it back.
      await expect(hostTabTrigger(page).getByTestId('host-tab-badge')).toBeVisible()
      await waitForInvalidationSettle(page)
      await expect(alert).toHaveCount(0)
    })
  })

  test('HOST-FREE-002: an unlabelled volume is listed unticked and a default Remove selected does not send it', async ({
    page,
  }) => {
    createLeftoverVolume()
    createLeftoverContainer() // reclaimable > 0, so the Free up button is enabled
    const leftoverId = containerId(LEFTOVER_CONTAINER)

    // Intercepted and answered here: the request never reaches the server, so
    // this test removes nothing.
    let sent: Record<string, unknown> | undefined
    await page.route(FREE_SPACE_PATH, async (route) => {
      sent = route.request().postDataJSON()
      await route.fulfill({ status: 200, json: { outcome: 'success', reason: 'intercepted by HOST-FREE-002', details: {} } })
    })

    const dialog = await openFreeUpDialog(page)
    const volumes = dialog.getByTestId('free-space-row-volumes')
    await expect(volumes.getByRole('checkbox')).not.toBeChecked()
    await volumes.getByText(/^Show the \d+ items$/).click()
    await expect(volumes.getByRole('listitem').filter({ hasText: LEFTOVER_VOLUME })).toBeVisible()

    await dialog.getByRole('button', { name: 'Remove selected' }).click()
    await expect.poll(() => sent, { message: 'the free-space request was never sent' }).toBeDefined()
    expect(sent!.volumes, 'a default Remove selected must not send volumes').toBeUndefined()
    expect(sent!.containers, 'the ticked containers row is sent').toContain(leftoverId)
    await expect(dialog).toHaveCount(0)
    expect(exists('volume', LEFTOVER_VOLUME)).toBe(true)
    // The container was in the request too, so it still being here shows the
    // request stopped at the route and removed nothing.
    expect(exists('container', LEFTOVER_CONTAINER)).toBe(true)
  })

  test('HOST-FREE-001 (destructive): Containers only removes the leftover and leaves the test stack alone', async ({
    page,
    request,
  }) => {
    test.skip(!DESTRUCTIVE, DESTRUCTIVE_SKIP_REASON)
    createLeftoverContainer()

    const dialog = await openFreeUpDialog(page)
    for (const row of ['images', 'buildCache', 'volumes', 'networks']) {
      const box = dialog.getByTestId(`free-space-row-${row}`).getByRole('checkbox')
      if (await box.isChecked()) await box.uncheck()
    }
    await expect(dialog.getByTestId('free-space-row-containers').getByRole('checkbox')).toBeChecked()
    await dialog.getByRole('button', { name: 'Remove selected' }).click()
    await expect(dialog).toHaveCount(0)

    await expect.poll(() => exists('container', LEFTOVER_CONTAINER), { timeout: 30_000 }).toBe(false)
    expect((await testStack(request)).status).toBe('running')
  })

  test('HOST-FREE-003 (destructive): an unlabelled volume survives a real default Remove selected', async ({ page }) => {
    test.skip(!DESTRUCTIVE, DESTRUCTIVE_SKIP_REASON)
    createLeftoverVolume()
    createLeftoverContainer()

    const dialog = await openFreeUpDialog(page)
    await expect(dialog.getByTestId('free-space-row-volumes').getByRole('checkbox')).not.toBeChecked()
    await dialog.getByRole('button', { name: 'Remove selected' }).click()
    await expect(dialog).toHaveCount(0)

    // The container going is the proof the removal ran.
    await expect.poll(() => exists('container', LEFTOVER_CONTAINER), { timeout: 30_000 }).toBe(false)
    expect(exists('volume', LEFTOVER_VOLUME)).toBe(true)
  })
})
