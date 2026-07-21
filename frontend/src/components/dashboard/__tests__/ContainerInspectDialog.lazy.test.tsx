/**
 * Verifies the lazy() + Suspense boundary around ContainerInspectDialog
 * (bundle-size fix — codemirror moved out of the Containers tab's eager
 * import graph). Exercises the real dynamic import(), not a mocked module,
 * so this proves the fallback-then-dialog path actually works at runtime.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, fireEvent, waitFor } from '@testing-library/react'
import { renderWithProviders } from '../../../test/utils'
import { ContainersOverviewTab } from '../ContainersOverviewTab'
import type { DashboardStats } from '@/types'

vi.mock('@/lib/api', () => ({
  stacksApi: {
    list: vi.fn().mockResolvedValue([]),
    start: vi.fn(),
    stop: vi.fn(),
    restart: vi.fn(),
    pull: vi.fn(),
  },
  resourcesApi: {
    startContainer: vi.fn(),
    stopContainer: vi.fn(),
    restartContainer: vi.fn(),
    deleteContainer: vi.fn(),
    pruneContainers: vi.fn(),
    inspectContainer: vi.fn().mockResolvedValue({ Id: 'c1', State: { Status: 'running' } }),
  },
  autoUpdateApi: {
    getPolicies: vi.fn().mockResolvedValue({ policies: [], globalEnabled: true }),
  },
}))

const mockStats: DashboardStats = {
  totalStacks: 1,
  runningStacks: 1,
  stoppedStacks: 0,
  totalContainers: 1,
  runningContainers: 1,
  imageDiskUsage: 0,
  diskUsage: { images: 0, containers: 0, volumes: 0, buildCache: 0, total: 0 },
  containers: [
    {
      id: 'c1',
      name: 'web',
      image: 'nginx:latest',
      state: 'running',
      status: 'Up 2 minutes',
      health: '',
      ports: [],
      stackId: 'stack-1',
      projectName: 'myproject',
      restartCount: 0,
      created: new Date().toISOString(),
      startedAt: new Date().toISOString(),
      diskSize: 0,
      imageSize: 0,
    },
  ],
}

describe('ContainerInspectDialog lazy boundary', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    // jsdom doesn't implement matchMedia; the dialog's dark-mode detection reads it.
    vi.stubGlobal(
      'matchMedia',
      vi.fn(() => ({
        matches: false,
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
      })),
    )
  })

  it('shows the Suspense fallback on first click, then the real dialog, with no double-fetch', async () => {
    const { resourcesApi } = await import('@/lib/api')
    renderWithProviders(
      <ContainersOverviewTab stats={mockStats} latestMetrics={{}} metricsStatus="connected" />,
    )

    const inspectButton = await screen.findByTitle('Inspect container')
    fireEvent.click(inspectButton)

    // Fallback renders synchronously on the same click — the dialog chunk
    // hasn't resolved yet.
    expect(screen.getByTestId('inspect-dialog-loading')).toBeInTheDocument()

    // Once the lazy import resolves, the real dialog swaps in and the
    // fallback is gone.
    expect(await screen.findByText('Inspect: web')).toBeInTheDocument()
    await waitFor(() =>
      expect(screen.queryByTestId('inspect-dialog-loading')).not.toBeInTheDocument(),
    )

    // The dialog's own data fetch (not the JS chunk fetch) should fire exactly
    // once — proves no double-fetch from the lazy boundary re-mounting.
    expect(resourcesApi.inspectContainer).toHaveBeenCalledTimes(1)
    expect(resourcesApi.inspectContainer).toHaveBeenCalledWith('c1')
  })
})
