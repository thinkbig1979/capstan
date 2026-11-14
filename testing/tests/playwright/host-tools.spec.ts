/**
 * The dashboard's Host tab (agent-os-mi4r) in a real browser (agent-os-bugc):
 * navigation into and around it, its entry points, and the Free up space
 * dialog against a real Docker daemon.
 *
 * Prerequisites (same backend as a11y.spec.ts):
 *   - App on CAPSTAN_BASE_URL (default http://localhost:3001), backend on
 *     CAPSTAN_API_URL (default http://localhost:5001), AUTH_DISABLED=true in both.
 *   - A stack named CAPSTAN_TEST_STACK (default test-app); it may be stopped.
 *   - The `docker` CLI, talking to the backend's daemon.
 *
 * FIXTURES are named (helpers/docker-fixture.ts) and removed by name in
 * afterEach, even when the test fails.
 *
 * DESTRUCTIVE TESTS (HOST-FREE-001, HOST-FREE-003) click "Remove selected" for
 * real, which removes every unlabelled stopped container on the host, not only
 * the fixture (both untick images and build cache, so no image goes). They run
 * only with CAPSTAN_E2E_DESTRUCTIVE=1 on a GitHub-hosted runner
 * (GITHUB_ACTIONS=true, RUNNER_ENVIRONMENT=github-hosted): the backup-flow job,
 * an ephemeral VM. Never set it on a developer machine or on the
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
  destructiveSkipReason,
  LABELLED_CONTAINER,
  LEFTOVER_CONTAINER,
  LEFTOVER_VOLUME,
  containerId,
  createLabelledStoppedContainer,
  createListedLeftoverContainer,
  createLeftoverVolume,
  exists,
  freshHostCandidates,
  removeLabelledStoppedContainer,
  removeLeftoverContainer,
  removeLeftoverVolume,
} from './helpers/docker-fixture'

const API_URL = process.env.CAPSTAN_API_URL ?? 'http://localhost:5001'
const AUTH_DISABLED = (process.env.AUTH_DISABLED ?? 'false') === 'true'
const TEST_STACK_NAME = process.env.CAPSTAN_TEST_STACK ?? 'test-app'

const HOST_CARDS = ['containers', 'images', 'volumes', 'networks', 'build-cache']
const FREE_SPACE_PATH = '**/api/v1/resources/host/free-space'
/**
 * A removal's own budget. A real one on a CI runner took 26 s (3.36 GB of
 * images), past the 15 s expect default (agent-os-bugc, run 43592886683).
 */
const FREE_SPACE_RESPONSE_MS = 120_000
/** Everything under the host routes: the fail-closed guard in HOST-FREE-002. */
const HOST_ROUTES = '**/api/v1/resources/host/**'

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

/**
 * Where #automatic-cleanup sits: 'in view' when its top is inside main's
 * VISIBLE box, i.e. main's rect clipped to the window and kept below the header
 * while the header is on screen (it scrolls away with the document: its column
 * is overflow-hidden, so sticky does not hold against the page scroll). main's
 * rect alone is not enough: when the document scrolls instead of
 * main, main's rect runs past the window and any top would count as inside it.
 */
function cleanupPanelPlace(page: Page): Promise<string> {
  return page.evaluate(() => {
    const el = document.getElementById('automatic-cleanup')
    const main = el?.closest('main')
    if (!el || !main) return 'missing'
    const top = el.getBoundingClientRect().top
    const box = main.getBoundingClientRect()
    const headerBottom = document.querySelector('header')?.getBoundingClientRect().bottom ?? 0
    const visibleTop = Math.max(box.top, headerBottom, 0)
    const visibleBottom = Math.min(box.bottom, window.innerHeight)
    return top >= visibleTop - 1 && top < visibleBottom ? 'in view' : `top ${top} outside ${visibleTop}..${visibleBottom}`
  })
}

/**
 * "Change timing" shows only when the stored policy is on and already covers
 * every ticked row. The policy read is answered here rather than switched on
 * for real: an enabled policy would let this backend run cleanup on the host.
 */
/**
 * The free-space POST's response, to be awaited after the click. Registered
 * before it, so a fast response is not missed. A 409 (another operation holds
 * the exclusive turn) then fails on its status, not on a dialog timeout.
 */
function freeSpaceResponse(page: Page) {
  return page.waitForResponse(
    (r) => new URL(r.url()).pathname === '/api/v1/resources/host/free-space' && r.request().method() === 'POST',
    { timeout: FREE_SPACE_RESPONSE_MS },
  )
}

async function fakeCoveringPolicy(page: Page): Promise<void> {
  const policyResp = await page.request.get(`${API_URL}/api/v1/resources/cleanup/policy`)
  expect(policyResp.status(), 'GET cleanup policy').toBe(200)
  const policy = await policyResp.json()
  await page.route('**/api/v1/resources/cleanup/policy', async (route) => {
    if (route.request().method() !== 'GET') return route.fallback()
    await route.fulfill({
      status: 200,
      json: {
        ...policy,
        enabled: true,
        categories: { danglingImages: true, unusedImages: true, buildCache: true, containers: true, volumes: false, networks: false },
      },
    })
  })
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
    removeLabelledStoppedContainer()
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
    await expect.poll(() => cleanupPanelPlace(page)).toBe('in view')
  })

  test('HOST-NAV-005: the Free up dialog\'s "Change timing" scrolls to the panel', async ({ page }) => {
    await createListedLeftoverContainer(API_URL) // reclaimable > 0, so the Free up button is enabled
    await fakeCoveringPolicy(page)

    const dialog = await openFreeUpDialog(page)
    // Below the fold before the click, so the scroll is what brings it in.
    await expect(cleanupPanelPlace(page)).resolves.toMatch(/^top /)
    await dialog.getByTestId('free-space-auto-covered').getByRole('link', { name: 'Change timing' }).click()
    await expect(dialog).toHaveCount(0)
    await expect(page).toHaveURL(/\/\?tab=host#automatic-cleanup$/)
    await expect.poll(() => cleanupPanelPlace(page)).toBe('in view')
  })

  test('HOST-NAV-006: "Change timing" from an already scrolled page lands on the panel, not past it', async ({
    page,
  }) => {
    await createListedLeftoverContainer(API_URL)
    await fakeCoveringPolicy(page)
    await openAndSettle(page, '/?tab=host')
    // A window short enough that the page end does not absorb an overshoot:
    // the panel is the page's last block, so at taller sizes a scroll that goes
    // too far is cut off at the end of the page and the panel still shows.
    await page.setViewportSize({ width: 1280, height: 200 })
    await page.evaluate(() => {
      document.scrollingElement!.scrollTop = 80
    })
    // Clicks dispatched in the page: Playwright's own click would first scroll
    // its target into view and change the starting offset under test.
    await page.getByTestId('host-free-space-button').dispatchEvent('click')
    const dialog = page.getByRole('dialog', { name: 'Free up space' })
    const changeTiming = dialog.getByTestId('free-space-auto-covered').getByRole('link', { name: 'Change timing' })
    await expect(changeTiming).toBeVisible()
    expect(await page.evaluate(() => document.scrollingElement!.scrollTop), 'starting offset').toBe(80)
    await changeTiming.dispatchEvent('click')
    await expect(dialog).toHaveCount(0)
    await expect(page).toHaveURL(/\/\?tab=host#automatic-cleanup$/)
    await expect.poll(() => cleanupPanelPlace(page)).toBe('in view')
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
      await createListedLeftoverContainer(API_URL)
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

      // The alert needs both the host summary (the badge shows it has loaded)
      // and the cleanup policy, so its absence is read only after both.
      const policyRead = page.waitForResponse(
        (r) => r.url().includes('/api/v1/resources/cleanup/policy') && r.request().method() === 'GET' && r.ok(),
      )
      await page.reload()
      await policyRead
      await expect(hostTabTrigger(page).getByTestId('host-tab-badge')).toBeVisible()
      await waitForInvalidationSettle(page)
      await expect(alert).toHaveCount(0)
    })
  })

  test('HOST-FREE-002: an unlabelled volume is listed unticked and a default Remove selected does not send it', async ({
    page,
  }) => {
    createLeftoverVolume()
    // reclaimable > 0, so the Free up button is enabled; the same fresh read
    // lists the volume.
    const leftoverId = await createListedLeftoverContainer(API_URL)

    // FAIL CLOSED: this test runs on developer machines, so its safety cannot
    // rest on one glob. The guard (registered first, so tried last) aborts any
    // non-GET host request the free-space handler did not take; a typo in
    // FREE_SPACE_PATH then fails the test with nothing removed.
    const blocked: string[] = []
    await page.route(HOST_ROUTES, async (route) => {
      if (route.request().method() === 'GET') return route.fallback()
      blocked.push(`${route.request().method()} ${route.request().url()}`)
      await route.abort()
    })
    // Answered here: the request never reaches the server.
    let sent: Record<string, unknown> | undefined
    await page.route(FREE_SPACE_PATH, async (route) => {
      if (route.request().method() !== 'POST') return route.fallback()
      sent = route.request().postDataJSON()
      await route.fulfill({ status: 200, json: { outcome: 'success', reason: 'intercepted by HOST-FREE-002', details: {} } })
    })

    const dialog = await openFreeUpDialog(page)
    const volumes = dialog.getByTestId('free-space-row-volumes')
    await expect(volumes.getByRole('checkbox')).not.toBeChecked()
    await volumes.getByText(/^Show the \d+ items$/).click()
    await expect(volumes.getByRole('listitem').filter({ hasText: LEFTOVER_VOLUME })).toBeVisible()

    const response = freeSpaceResponse(page)
    await dialog.getByRole('button', { name: 'Remove selected' }).click()
    expect((await response).status(), 'free-space response status').toBe(200)
    await expect
      .poll(() => sent, { message: `the free-space handler was never hit; the guard blocked: ${blocked.join(', ') || 'nothing'}` })
      .toBeDefined()
    expect(blocked, 'no other host request may have been attempted').toEqual([])
    expect(sent!.volumes, 'a default Remove selected must not send volumes').toBeUndefined()
    expect(sent!.containers, 'the ticked containers row is sent').toContain(leftoverId)
    await expect(dialog).toHaveCount(0)
    expect(exists('volume', LEFTOVER_VOLUME)).toBe(true)
    // The container was in the request too, so it still being here shows the
    // request stopped at the route and removed nothing.
    expect(exists('container', LEFTOVER_CONTAINER)).toBe(true)
  })

  test('HOST-FREE-001 (destructive): Containers only removes the leftover, not a stopped compose-labelled container', async ({
    page,
  }) => {
    const skipReason = destructiveSkipReason()
    test.skip(skipReason !== undefined, skipReason)
    // The rule under test: a stopped container with a compose project label is
    // never a candidate (a stopped stack's container looks like this). A
    // fixture of our own, not test-app, whose state depends on which specs ran
    // before this one; a running container is never a candidate anyway.
    createLabelledStoppedContainer()
    const leftoverId = await createListedLeftoverContainer(API_URL)
    const labelledId = containerId(LABELLED_CONTAINER)
    const listed = (await freshHostCandidates(API_URL)).containers.map((c) => c.id)
    expect(listed, 'the leftover is a candidate').toContain(leftoverId)
    expect(listed, 'the compose-labelled container is not').not.toContain(labelledId)

    const dialog = await openFreeUpDialog(page)
    for (const row of ['images', 'buildCache', 'volumes', 'networks']) {
      const box = dialog.getByTestId(`free-space-row-${row}`).getByRole('checkbox')
      if (await box.isChecked()) await box.uncheck()
    }
    await expect(dialog.getByTestId('free-space-row-containers').getByRole('checkbox')).toBeChecked()
    const response = freeSpaceResponse(page)
    await dialog.getByRole('button', { name: 'Remove selected' }).click()
    expect((await response).status(), 'free-space response status').toBe(200)
    await expect(dialog).toHaveCount(0)

    await expect.poll(() => exists('container', LEFTOVER_CONTAINER), { timeout: 30_000 }).toBe(false)
    expect(exists('container', LABELLED_CONTAINER), 'the compose-labelled container survives').toBe(true)
  })

  test('HOST-FREE-003 (destructive): an unlabelled volume survives a real Remove selected (volumes at their default)', async ({
    page,
  }) => {
    const skipReason = destructiveSkipReason()
    test.skip(skipReason !== undefined, skipReason)
    createLeftoverVolume()
    await createListedLeftoverContainer(API_URL)

    const dialog = await openFreeUpDialog(page)
    await expect(dialog.getByTestId('free-space-row-volumes').getByRole('checkbox')).not.toBeChecked()
    // Images and build cache unticked: a runner's unused images are gigabytes
    // and a 26 s removal, and they are not what this test is about. Containers
    // stays ticked, so the removal visibly runs; Volumes is left as it opened.
    for (const row of ['images', 'buildCache']) {
      const box = dialog.getByTestId(`free-space-row-${row}`).getByRole('checkbox')
      if (await box.isChecked()) await box.uncheck()
    }
    await expect(dialog.getByTestId('free-space-row-containers').getByRole('checkbox')).toBeChecked()
    const response = freeSpaceResponse(page)
    await dialog.getByRole('button', { name: 'Remove selected' }).click()
    expect((await response).status(), 'free-space response status').toBe(200)
    await expect(dialog).toHaveCount(0)

    // The container going is the proof the removal ran.
    await expect.poll(() => exists('container', LEFTOVER_CONTAINER), { timeout: 30_000 }).toBe(false)
    expect(exists('volume', LEFTOVER_VOLUME)).toBe(true)
  })
})
