import { describe, it, expect, vi, beforeAll, beforeEach } from 'vitest'
import { screen, within } from '@testing-library/react'
import { renderWithProviders } from '@/test/utils'
import { HostStrip } from '../HostStrip'
import { DashboardMetricsTab } from '../DashboardMetricsTab'
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
  diskUsage: { total: 0, images: 0, containers: 0, volumes: 0, buildCache: 0 },
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

const hostDetail = (label: string) =>
  within(screen.getByRole('button', { name: new RegExp(`^${label}`) })).queryByText(/./, {
    selector: 'span',
  })?.textContent

describe('HostStrip — omitted stats', () => {
  it('shows unavailable, not 0, when the container and disk reads failed', () => {
    renderWithProviders(<HostStrip stats={bothReadsFailed} onNavigate={() => {}} />)

    // Precondition: the strip rendered its links for a loaded stats object.
    expect(screen.getByTestId('host-strip')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /^Networks/ })).toBeInTheDocument()

    expect(hostDetail('Containers')).toBe('unavailable')
    expect(hostDetail('Images')).toBe('unavailable')
    expect(hostDetail('Volumes')).toBe('unavailable')
    expect(hostDetail('Build cache')).toBe('unavailable')
  })

  it('still shows 0 for a genuinely empty host', () => {
    renderWithProviders(<HostStrip stats={emptyHost} onNavigate={() => {}} />)

    expect(hostDetail('Containers')).toBe('0')
    expect(hostDetail('Images')).toBe('0 B')
    expect(screen.queryByText('unavailable')).not.toBeInTheDocument()
  })
})

// DashboardPage passes runningContainers as `dashboardStats?.runningContainers
// || 0`, so an omitted count arrives here as the prop 0. The component has the
// stats object and must read the absence from it.
const renderMetricsTab = (stats: DashboardStats) =>
  renderWithProviders(
    <DashboardMetricsTab
      stats={stats}
      aggregates={aggregates}
      isConnected={true}
      totalStacks={2}
      runningStacks={0}
      stoppedStacks={2}
      totalContainers={0}
      runningContainers={stats.runningContainers ?? 0}
      directoryCount={1}
      containers={[]}
    />,
  )

describe('DashboardMetricsTab — omitted stats', () => {
  it('shows the host container count and disk usage as unavailable when their reads failed', () => {
    renderMetricsTab(bothReadsFailed)

    // Precondition: the loaded layout rendered, not the skeleton.
    expect(screen.getByText('Disk Usage')).toBeInTheDocument()
    expect(screen.getByText('Performance')).toBeInTheDocument()

    expect(screen.queryByText('0 on host')).not.toBeInTheDocument()
    expect(screen.getByText('Host count unavailable')).toBeInTheDocument()
    expect(screen.queryByText(/across 0 containers/)).not.toBeInTheDocument()
    // The per-category rows would each read 0 B. (Swap stays: it comes from
    // the metrics stream, not from this read.)
    expect(screen.queryByText('Build Cache')).not.toBeInTheDocument()
    expect(screen.getByText('Disk usage is unavailable.')).toBeInTheDocument()
  })

  it('still shows zeros for a genuinely empty host', () => {
    renderMetricsTab(emptyHost)

    expect(screen.getByText('0 on host')).toBeInTheDocument()
    expect(screen.getByText('Build Cache')).toBeInTheDocument()
    expect(screen.getByText(/across 0 containers/)).toBeInTheDocument()
    expect(screen.queryByText('Disk usage is unavailable.')).not.toBeInTheDocument()
    expect(screen.queryByText('Host count unavailable')).not.toBeInTheDocument()
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
