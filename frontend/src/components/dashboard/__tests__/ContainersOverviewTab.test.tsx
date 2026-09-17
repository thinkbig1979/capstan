import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, fireEvent, waitFor } from '@testing-library/react'
import { toast } from 'sonner'
import { renderWithProviders } from '@/test/utils'
import { ContainersOverviewTab } from '../ContainersOverviewTab'
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
 * and prove nothing. Containers land in the stack tab by having a projectName
 * (isStandaloneContainer), so every fixture below carries one.
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
