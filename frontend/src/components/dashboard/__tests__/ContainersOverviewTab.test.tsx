import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, fireEvent, waitFor } from '@testing-library/react'
import { toast } from 'sonner'
import { renderWithProviders } from '@/test/utils'
import { ContainersOverviewTab, NO_STACK_FOR_PULL } from '../ContainersOverviewTab'
import type { DashboardStats, DashboardContainerInfo } from '@/types'

/**
 * agent-os-mc4i. Five action toasts in this tab laundered a Docker-outage
 * recovery paragraph into "503: Something went wrong on the server".
 *
 * WHY EVERY ARM HERE RENDERS mode="stack": start/stop/restart each serve TWO
 * routes from one onError. The container route (resources.go, respondDockerErr)
 * answers an AppError whose message classifyError already reads — PR #407 fixed
 * that half. The defect lives only on the stack route (stack_lifecycle.go,
 * renderDockerResult), which answers a truth.ActionResult: a body carrying
 * `outcome`/`reason` and neither `error` nor `message`, so classifyError cannot
 * see the reason at all. A mode="standalone" arm would go green without the fix
 * and prove nothing.
 *
 * TWO INDEPENDENT THINGS PIN EVERY ARM BELOW TO mode="stack", and the first is
 * the one that would evaporate silently, so it is written down here:
 *  1. The locators are the SINGULAR getByLabelText('Start stack') etc., which
 *     throw on zero or multiple matches. A standalone row's button is labelled
 *     'Start container' (`label` is derived from `mode`), so a passing click is
 *     provably on a stack-mode button. If those aria-labels are ever
 *     de-duplicated across modes this discriminator is gone and these arms stop
 *     proving what they claim -- reintroduce one by asserting on `mode` directly.
 *  2. isStandaloneContainer is `!c.projectName`, and every fixture sets one, so
 *     a standalone row cannot exist in these renders at all.
 *
 * Each arm also asserts toHaveBeenCalledTimes(1). Without it the suite cannot
 * see a handler that fires the description toast AND then falls through to the
 * laundered sentence: toHaveBeenCalledWith passes when ANY recorded call matches.
 */

const DOCKER_REASON =
  'Docker daemon unreachable: the server started without a usable Docker connection. ' +
  'Check that the Docker socket is mounted and the daemon is running, then restart Capstan.'

/** The shape api.ts's interceptor rejects with for a renderDockerResult body. */
const actionResultFailure = () => ({
  outcome: 'failed',
  reason: DOCKER_REASON,
  details: { id: 'c1' },
  status: 503,
})

/** The shape it rejects with for a respondDockerErr body — the control. */
const appErrorFailure = () => ({
  error: 'DOCKER_UNAVAILABLE',
  code: 'DOCKER_UNAVAILABLE',
  message: DOCKER_REASON,
  status: 503,
})

const { stacksMock, resourcesMock, autoUpdateMock } = vi.hoisted(() => ({
  stacksMock: {
    list: vi.fn(),
    start: vi.fn(),
    stop: vi.fn(),
    restart: vi.fn(),
    pull: vi.fn(),
  },
  resourcesMock: {
    deleteContainer: vi.fn(),
    pruneContainers: vi.fn(),
    startContainer: vi.fn(),
    stopContainer: vi.fn(),
    restartContainer: vi.fn(),
  },
  autoUpdateMock: { getPolicies: vi.fn() },
}))

vi.mock('sonner', () => ({
  toast: { error: vi.fn(), success: vi.fn(), info: vi.fn(), warning: vi.fn() },
  Toaster: () => null,
}))

vi.mock('@/lib/api', async () => {
  const actual = await vi.importActual<typeof import('@/lib/api')>('@/lib/api')
  return {
    ...actual,
    stacksApi: { ...actual.stacksApi, ...stacksMock },
    resourcesApi: { ...actual.resourcesApi, ...resourcesMock },
    autoUpdateApi: { ...actual.autoUpdateApi, ...autoUpdateMock },
  }
})

function makeContainer(overrides: Partial<DashboardContainerInfo> = {}): DashboardContainerInfo {
  return {
    id: 'c1',
    name: 'web',
    image: 'nginx:latest',
    state: 'running',
    status: 'Up 2 hours',
    health: '',
    ports: [],
    stackId: 'stack-1',
    projectName: 'myproject',
    restartCount: 0,
    created: '2026-01-01T00:00:00Z',
    startedAt: '2026-01-01T00:00:00Z',
    diskSize: 0,
    imageSize: 0,
    ...overrides,
  }
}

function renderTab(container: DashboardContainerInfo = makeContainer()) {
  const stats = { containers: [container] } as unknown as DashboardStats
  return renderWithProviders(
    <ContainersOverviewTab stats={stats} latestMetrics={{}} metricsStatus="connected" />,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  stacksMock.list.mockResolvedValue([])
  autoUpdateMock.getPolicies.mockResolvedValue({ policies: [] })
})

describe('ContainersOverviewTab — ActionResult reasons reach the operator', () => {
  it('start (stack route) shows the ActionResult reason as the toast description', async () => {
    stacksMock.start.mockRejectedValue(actionResultFailure())
    renderTab(makeContainer({ state: 'exited' }))

    fireEvent.click(screen.getByLabelText('Start stack'))

    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith('Failed to start stack', {
        description: DOCKER_REASON,
      }),
    )
    // Pins the else arm: the laundered sentence must not follow the description.
    expect(toast.error).toHaveBeenCalledTimes(1)
    expect(stacksMock.start).toHaveBeenCalledWith('stack-1')
  })

  it('stop (stack route) shows the ActionResult reason as the toast description', async () => {
    stacksMock.stop.mockRejectedValue(actionResultFailure())
    renderTab()

    fireEvent.click(screen.getByLabelText('Stop stack'))

    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith('Failed to stop stack', {
        description: DOCKER_REASON,
      }),
    )
    // Pins the else arm: the laundered sentence must not follow the description.
    expect(toast.error).toHaveBeenCalledTimes(1)
    expect(stacksMock.stop).toHaveBeenCalledWith('stack-1')
  })

  it('restart (stack route) shows the ActionResult reason as the toast description', async () => {
    stacksMock.restart.mockRejectedValue(actionResultFailure())
    renderTab()

    fireEvent.click(screen.getByLabelText('Restart stack'))

    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith('Failed to restart stack', {
        description: DOCKER_REASON,
      }),
    )
    // Pins the else arm: the laundered sentence must not follow the description.
    expect(toast.error).toHaveBeenCalledTimes(1)
    expect(stacksMock.restart).toHaveBeenCalledWith('stack-1')
  })

  it('pull shows the ActionResult reason as the toast description', async () => {
    stacksMock.pull.mockRejectedValue(actionResultFailure())
    renderTab()

    fireEvent.click(screen.getByLabelText('Pull images for stack'))

    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith('Failed to pull images', {
        description: DOCKER_REASON,
      }),
    )
    // Pins the else arm: the laundered sentence must not follow the description.
    expect(toast.error).toHaveBeenCalledTimes(1)
    expect(stacksMock.pull).toHaveBeenCalledWith('stack-1')
  })

  it('delete container shows the ActionResult reason as the toast description', async () => {
    resourcesMock.deleteContainer.mockRejectedValue(actionResultFailure())
    renderTab()

    fireEvent.click(screen.getByLabelText('Remove stack'))
    fireEvent.click(await screen.findByRole('button', { name: 'Remove' }))

    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith('Failed to remove container', {
        description: DOCKER_REASON,
      }),
    )
    // Pins the else arm: the laundered sentence must not follow the description.
    expect(toast.error).toHaveBeenCalledTimes(1)
    expect(resourcesMock.deleteContainer).toHaveBeenCalledWith('c1', true)
  })
})

describe('ContainersOverviewTab — non-ActionResult errors keep the classifier sentence', () => {
  // The other side of the same instrument: an AppError body must NOT take the
  // new branch, and must stay a single-argument call (sonner renders
  // toast.error(t, undefined) and toast.error(t) alike; a spy does not).
  it('start keeps the single-argument classifier toast for an AppError body', async () => {
    stacksMock.start.mockRejectedValue(appErrorFailure())
    renderTab(makeContainer({ state: 'exited' }))

    fireEvent.click(screen.getByLabelText('Start stack'))

    await waitFor(() => expect(toast.error).toHaveBeenCalled())
    expect(toast.error).toHaveBeenCalledTimes(1)
    expect(vi.mocked(toast.error).mock.calls[0]).toHaveLength(1)
    expect(vi.mocked(toast.error).mock.calls[0][0]).toContain(DOCKER_REASON)
  })

  // Pins the `&& err.reason` conjunct. A failed ActionResult may carry an empty
  // reason; without the conjunct this renders an empty toast description, which
  // is worse than the generic sentence it replaced.
  it('start falls through to the classifier when the ActionResult reason is empty', async () => {
    stacksMock.start.mockRejectedValue({ outcome: 'failed', reason: '', details: {}, status: 503 })
    renderTab(makeContainer({ state: 'exited' }))

    fireEvent.click(screen.getByLabelText('Start stack'))

    await waitFor(() => expect(toast.error).toHaveBeenCalled())
    expect(toast.error).toHaveBeenCalledTimes(1)
    expect(vi.mocked(toast.error).mock.calls[0]).toHaveLength(1)
    expect(vi.mocked(toast.error).mock.calls[0][0]).toBe('503: Something went wrong on the server')
  })

  it('delete keeps the single-argument classifier toast for an AppError body', async () => {
    resourcesMock.deleteContainer.mockRejectedValue(appErrorFailure())
    renderTab()

    fireEvent.click(screen.getByLabelText('Remove stack'))
    fireEvent.click(await screen.findByRole('button', { name: 'Remove' }))

    await waitFor(() => expect(toast.error).toHaveBeenCalled())
    expect(vi.mocked(toast.error).mock.calls[0]).toHaveLength(1)
    expect(vi.mocked(toast.error).mock.calls[0][0]).toContain(DOCKER_REASON)
  })
})

describe('ContainersOverviewTab — a stack-mode row with no stackId', () => {
  /**
   * agent-os-yke1. pullMutation guarded the WORK on `stackId` and the SUCCESS
   * MESSAGE on `mode` alone, so a stack-mode row with a falsy stackId resolved
   * undefined without calling stacksApi.pull, React Query read that as success,
   * and the operator was told "Images pulled" for a request never sent.
   *
   * This is reachable by ordinary operation, not a synthetic shape. Backend
   * GetDashboardContainers declares `var stackID string` and leaves it "" when
   * lookupStackByProject returns no stack and no error — a compose project
   * running on the host that Capstan has no stack row for. That branch does not
   * even log. projectName still comes off the com.docker.compose.project label,
   * and isStandaloneContainer is `!c.projectName`, so the row lands in the Stack
   * Containers tab and renders mode="stack" with stackId="".
   *
   * THE TWO ARMS ARE ONE INSTRUMENT. The first asserts toast.success is NOT
   * called; on its own that would also pass if toast.success were unreachable in
   * this fixture for any unrelated reason. The second varies ONLY stackId and
   * requires toast.success to fire, which is what makes the first arm's absence
   * mean something.
   */
  it('does not report success, and says why, when stackId is empty', async () => {
    renderTab(makeContainer({ stackId: '' }))

    fireEvent.click(screen.getByLabelText('Pull images for stack'))

    await waitFor(() => expect(toast.error).toHaveBeenCalled())
    // Single-argument, matching the classifier arm: an Error carries no
    // ActionResult reason, so onError takes its else branch. Asserted by arity
    // rather than toHaveBeenCalledWith(msg, undefined), which does not match a
    // one-argument call.
    expect(vi.mocked(toast.error).mock.calls[0]).toHaveLength(1)
    expect(vi.mocked(toast.error).mock.calls[0][0]).toBe(NO_STACK_FOR_PULL)
    expect(toast.success).not.toHaveBeenCalled()
    expect(stacksMock.pull).not.toHaveBeenCalled()
  })

  it('still reports success for a row that does have a stackId', async () => {
    stacksMock.pull.mockResolvedValue(undefined)
    renderTab(makeContainer({ stackId: 'stack-1' }))

    fireEvent.click(screen.getByLabelText('Pull images for stack'))

    await waitFor(() => expect(toast.success).toHaveBeenCalledWith('Images pulled'))
    expect(stacksMock.pull).toHaveBeenCalledWith('stack-1')
    expect(toast.error).not.toHaveBeenCalled()
  })
})

/**
 * agent-os-bueb. A FAILED policies query was rendered as a deliberate
 * configuration state: `globalDisabled={!policiesData?.globalEnabled}` made
 * `undefined` and `false` the same thing, so a failed GET told the operator to
 * go flip a switch in Settings that was never the cause.
 *
 * TWO-SIDED ON ONE INSTRUMENT, and that is the point: the same query, rejected
 * in one arm and resolved to `globalEnabled: false` in the other. A one-sided
 * arm cannot tell a fixed conflation from a removed lock — a fix that turned
 * every falsy value into an error state would be worse than the bug.
 *
 * Asserted on the aria-label, NOT on the tooltip copy: Radix renders
 * TooltipContent in a portal that is not in the DOM until the trigger is
 * hovered or focused, so a queryByText over the copy returns null whether or
 * not the defect is present.
 */
describe('ContainersOverviewTab — the auto-update lock names the state it is in', () => {
  const findToggle = () =>
    screen.findByRole('switch', { name: /^Auto-update container c1/ })

  it('does not blame the global switch when the policies query fails', async () => {
    autoUpdateMock.getPolicies.mockRejectedValue(new Error('policies unreachable'))
    renderTab()

    const toggle = await findToggle()
    await waitFor(() =>
      expect(toggle.getAttribute('aria-label')).toContain('auto-update policy state unavailable'),
    )
    expect(toggle.getAttribute('aria-label')).not.toContain('global auto-update is off')
    // The lock is the safe failure and stays: only the explanation was wrong.
    expect(toggle).toBeDisabled()
  })

  it('still blames the global switch when it is genuinely off', async () => {
    autoUpdateMock.getPolicies.mockResolvedValue({ policies: [], globalEnabled: false })
    renderTab()

    const toggle = await findToggle()
    await waitFor(() =>
      expect(toggle.getAttribute('aria-label')).toContain('global auto-update is off'),
    )
    expect(toggle).toBeDisabled()
  })

  it('leaves the toggle interactive when auto-update is globally on', async () => {
    autoUpdateMock.getPolicies.mockResolvedValue({ policies: [], globalEnabled: true })
    renderTab()

    await waitFor(async () => expect(await findToggle()).not.toBeDisabled())
    expect((await findToggle()).getAttribute('aria-label')).toBe('Auto-update container c1')
  })
})
