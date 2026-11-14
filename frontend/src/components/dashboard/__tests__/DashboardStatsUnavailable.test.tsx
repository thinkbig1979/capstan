import { describe, it, expect, vi, beforeAll, beforeEach } from 'vitest'
import { screen } from '@testing-library/react'
import { renderWithProviders } from '@/test/utils'
import { HostLiveRow } from '@/components/host/HostLiveRow'
import { ContainersOverviewTab } from '../ContainersOverviewTab'
import type { DashboardStats } from '@/types'
import type { DashboardAggregateMetrics } from '@/hooks/useDashboardMetrics'

/**
 * agent-os-p9e1. GET /dashboard/stats OMITS the container-derived keys when
 * Docker's container list could not be read, and the disk keys when its disk
 * usage could not be read. Before that it sent zeros, which rendered as "0
 * containers" and "0 B" on a host that has both. These components must not
 * turn the omission back into a zero: absence reads "unavailable", while a
 * genuinely empty host still reads 0.
 *
 * "Unavailable" rather than "Docker could not be reached" because an absent key
 * has two readings the payload cannot separate (this server faulted, or it
 * predates the fix) and the word is true under both, as in agent-os-xppj.
 */

const { stacksMock, autoUpdateMock } = vi.hoisted(() => ({
  stacksMock: { list: vi.fn() },
  autoUpdateMock: { getPolicies: vi.fn() },
}))

vi.mock('@/lib/api', async () => {
  const actual = await vi.importActual<typeof import('@/lib/api')>('@/lib/api')
  return {
    ...actual,
    stacksApi: { ...actual.stacksApi, ...stacksMock },
    autoUpdateApi: { ...actual.autoUpdateApi, ...autoUpdateMock },
  }
})

beforeAll(() => {
  globalThis.ResizeObserver = class {
    observe() {}
    unobserve() {}
    disconnect() {}
  }
})

beforeEach(() => {
  vi.clearAllMocks()
  stacksMock.list.mockResolvedValue([])
  autoUpdateMock.getPolicies.mockResolvedValue({ policies: [] })
})

// What the server sends when BOTH Docker reads failed: only the stack count.
const bothReadsFailed = { totalStacks: 2 } as unknown as DashboardStats

// What it sends for a healthy host with nothing on it.
const emptyHost: DashboardStats = {
  totalStacks: 2,
  runningStacks: 0,
  stoppedStacks: 2,
  totalContainers: 0,
  runningContainers: 0,
  imageDiskUsage: 0,
  diskUsage: { total: 0, images: 0, containers: 0, volumes: 0, buildCache: 0, volumesSizeKnown: true, containersSizeKnown: true },
  containers: [],
}

const aggregates: DashboardAggregateMetrics = {
  totalCpuPercent: 0,
  totalMemUsage: 0,
  totalMemLimit: 0,
  totalMemPercent: 0,
  totalNetRx: 0,
  totalNetTx: 0,
  totalBlockRead: 0,
  totalBlockWrite: 0,
  totalSwap: 0,
  totalPids: 0,
}

// agent-os-mi4r: the Metrics tab's Performance card is the Host tab's Live
// row. The disk card went with it; the Host tab's disk figures come from the
// host summary and keep their own "could not measure" tests (HostTab.test.tsx).
const renderLiveRow = (stats: DashboardStats) =>
  renderWithProviders(<HostLiveRow stats={stats} aggregates={aggregates} isConnected={true} containers={[]} />)

describe('HostLiveRow — omitted stats', () => {
  it('shows processes without a container count when the host count read failed', () => {
    renderLiveRow(bothReadsFailed)

    // Precondition: the connected layout rendered, not the dashes.
    expect(screen.getByTestId('host-live-processes')).toHaveTextContent('0')
    expect(screen.getByTestId('host-live-processes')).not.toHaveTextContent('—')
    expect(screen.queryByText(/across 0 running containers/)).not.toBeInTheDocument()
  })

  it('still shows zeros for a genuinely empty host', () => {
    renderLiveRow(emptyHost)

    expect(screen.getByText('across 0 running containers')).toBeInTheDocument()
  })
})

describe('ContainersOverviewTab — omitted container list', () => {
  it('says the container list is unavailable instead of "0 containers"', () => {
    renderWithProviders(
      <ContainersOverviewTab stats={bothReadsFailed} latestMetrics={{}} metricsStatus="connected" />,
    )

    // Precondition: past the loading skeleton.
    expect(screen.getByText('The container list is unavailable.')).toBeInTheDocument()
    expect(screen.queryByText('0 containers')).not.toBeInTheDocument()
    expect(screen.queryByText('No Stack Containers')).not.toBeInTheDocument()
  })

  it('still reports an empty list for a genuinely empty host', () => {
    renderWithProviders(
      <ContainersOverviewTab stats={emptyHost} latestMetrics={{}} metricsStatus="connected" />,
    )

    expect(screen.getByText('0 containers')).toBeInTheDocument()
    expect(screen.getByText('No Stack Containers')).toBeInTheDocument()
    expect(screen.queryByText('The container list is unavailable.')).not.toBeInTheDocument()
  })
})
