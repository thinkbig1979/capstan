import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { BrowserRouter, Routes, Route } from 'react-router'
import type { HostSummary } from '@/types/generated-services'

// agent-os-mi4r: the dashboard's Host tab (/?tab=host and &view=<view>),
// formerly the Docker host page (agent-os-8y80).
const hostSummary = vi.fn()
const dashboardStats = vi.fn()

vi.mock('@/lib/api', () => ({
  resourcesApi: { hostSummary: (...args: unknown[]) => hostSummary(...args) },
  dashboardApi: { stats: (...args: unknown[]) => dashboardStats(...args) },
}))

const metrics = vi.hoisted(() => ({
  value: {
    containers: [] as unknown[],
    aggregates: {} as Record<string, number>,
    latestMetrics: {},
    isConnected: false,
    ws: { status: 'open' },
  },
}))
vi.mock('@/hooks/DashboardMetricsContext', () => ({
  useDashboardMetricsContext: () => metrics.value,
}))

// The list views keep their own coverage; this file pins which one mounts.
vi.mock('@/components/dashboard/ContainersOverviewTab', () => ({
  ContainersOverviewTab: ({ stats, metricsStatus }: { stats?: { totalContainers?: number }; metricsStatus: string }) => (
    <div data-testid="tab-containers" data-total={stats?.totalContainers} data-metrics-status={metricsStatus} />
  ),
}))
vi.mock('@/components/dashboard/ImagesTab', () => ({ ImagesTab: () => <div data-testid="tab-images" /> }))
vi.mock('@/components/dashboard/VolumesTab', () => ({ VolumesTab: () => <div data-testid="tab-volumes" /> }))
vi.mock('@/components/dashboard/NetworksTab', () => ({ NetworksTab: () => <div data-testid="tab-networks" /> }))
vi.mock('@/components/dashboard/BuildCacheTab', () => ({ BuildCacheTab: () => <div data-testid="tab-build-cache" /> }))
// The dialog has its own file (FreeUpSpaceDialog.test.tsx); this one pins who opens it.
vi.mock('@/components/host/FreeUpSpaceDialog', () => ({
  FreeUpSpaceDialog: ({ open, onOpenChange }: { open: boolean; onOpenChange: (o: boolean) => void }) =>
    open ? (
      <div role="dialog" aria-label="Free up space">
        <button onClick={() => onOpenChange(false)}>Close stub</button>
      </div>
    ) : null,
}))

// The panel has its own file (AutoCleanupPanel.test.tsx); this one pins where it mounts.
// HostTabCleanupAnchor.test.tsx runs the real panel for the #automatic-cleanup scroll.
vi.mock('@/components/host/AutoCleanupPanel', () => ({
  AutoCleanupPanel: ({ ready }: { ready?: boolean }) => (
    <section data-testid="auto-cleanup-panel-stub" id="automatic-cleanup" data-ready={String(ready)} />
  ),
}))

import { HostTab } from '../HostTab'
import { getBreadcrumbs } from '@/components/layout/breadcrumbs'
import { queryKeys } from '@/lib/query-keys'
import type { ContainerMetric, ContainerMetricHistory } from '@/hooks/useMetricsBase'

const GiB = 1024 ** 3
const MiB = 1024 ** 2

function makeSummary(overrides: Partial<HostSummary> = {}): HostSummary {
  return {
    disk: { total: 10 * GiB, inUse: 8 * GiB, reclaimable: 2 * GiB, reclaimableAll: 3 * GiB, volumesSizeKnown: true, containersSizeKnown: true },
    containers: { count: 20, running: 14, stopped: 6, reclaimableCount: 3, reclaimableBytes: 420 * MiB, reclaimableBytesKnown: true },
    images: { count: 48, inUse: 31, inUseKnown: true, bytes: 21 * GiB, reclaimableCount: 17, reclaimableBytes: 14 * GiB },
    volumes: { count: 22, attached: 19, attachedKnown: true, bytes: 5 * GiB, sizeKnown: true, reclaimableCount: 3, reclaimableBytes: 1 * GiB },
    networks: { count: 11, builtIn: 3, stackOwned: 8, reclaimableCount: 0 },
    buildCache: { count: 40, bytes: 2 * GiB, reclaimableBytes: 2 * GiB },
    measuredAt: '2026-10-14T10:00:00Z',
    ...overrides,
  }
}

function renderHost(route = '/?tab=host') {
  window.history.pushState({}, 'Test page', route)
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: Infinity }, mutations: { retry: false } },
  })
  return {
    ...render(
      <BrowserRouter>
        <QueryClientProvider client={queryClient}>
          <Routes>
            <Route path="/" element={<HostTab />} />
          </Routes>
        </QueryClientProvider>
      </BrowserRouter>,
    ),
    queryClient,
  }
}

const CATEGORIES = ['containers', 'images', 'volumes', 'networks', 'build-cache'] as const

function resetMetrics() {
  metrics.value = { containers: [], aggregates: {}, latestMetrics: {}, isConnected: false, ws: { status: 'open' } }
}

describe('HostTab overview', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    resetMetrics()
    hostSummary.mockResolvedValue(makeSummary())
    dashboardStats.mockResolvedValue({ totalContainers: 7 })
  })

  it('labels the disk bar with what is in use and what is reclaimable', async () => {
    renderHost()
    const bar = await screen.findByTestId('host-disk-bar')
    expect(bar).toHaveAttribute('role', 'img')
    expect(bar).toHaveAttribute('aria-label', '8.00 GB in use, 2.00 GB reclaimable')
  })

  it('renders five category cards, each with a reclaim line and Clean up, or Nothing to clean up and no Clean up', async () => {
    renderHost()
    await screen.findByTestId('host-disk-bar')
    for (const c of CATEGORIES) {
      const card = screen.getByTestId(`host-card-${c}`)
      const reclaim = within(card).queryByTestId(`host-card-${c}-reclaim`)
      const cleanUp = within(card).queryByRole('button', { name: /^Clean up/ })
      if (c === 'networks') {
        expect(reclaim).toBeNull()
        expect(cleanUp).toBeNull()
        expect(within(card).getByText('Nothing to clean up')).toBeInTheDocument()
      } else {
        expect(reclaim).not.toBeNull()
        expect(cleanUp).not.toBeNull()
        expect(within(card).queryByText('Nothing to clean up')).toBeNull()
      }
    }
    // Count and size per card.
    expect(within(screen.getByTestId('host-card-containers')).getByText('20')).toBeInTheDocument()
    expect(screen.getByTestId('host-card-containers-reclaim')).toHaveTextContent(
      '3 stopped containers outside any stack · 420.00 MB',
    )
    expect(within(screen.getByTestId('host-card-images')).getByText('21.00 GB')).toBeInTheDocument()
    expect(screen.getByTestId('host-card-images-reclaim')).toHaveTextContent('17 unused images · about 14.00 GB')
    expect(screen.getByTestId('host-card-build-cache-reclaim')).toHaveTextContent('Unused cache · 2.00 GB')
  })

  it('links every card\'s View all to its list view', async () => {
    renderHost()
    await screen.findByTestId('host-disk-bar')
    for (const c of CATEGORIES) {
      const link = within(screen.getByTestId(`host-card-${c}`)).getByRole('link', { name: /^View all/ })
      expect(link).toHaveAttribute('href', `/?tab=host&view=${c}`)
    }
  })

  it('offers "Free up <reclaimable>", which opens the dialog, and drops ?free=1 when it closes', async () => {
    const user = userEvent.setup()
    renderHost()
    const button = await screen.findByTestId('host-free-space-button')
    expect(button).toHaveTextContent('Free up 2.00 GB')
    expect(button).toBeEnabled()
    expect(screen.queryByRole('dialog')).toBeNull()

    await user.click(button)
    expect(screen.getByRole('dialog', { name: 'Free up space' })).toBeInTheDocument()
    expect(window.location.search).toBe('?tab=host&free=1')

    await user.click(screen.getByRole('button', { name: 'Close stub' }))
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(window.location.search).toBe('?tab=host')
  })

  it('disables the button as "Nothing to free up" when nothing is reclaimable', async () => {
    hostSummary.mockResolvedValue(
      makeSummary({ disk: { total: 8 * GiB, inUse: 8 * GiB, reclaimable: 0, reclaimableAll: 0, volumesSizeKnown: true, containersSizeKnown: true } }),
    )
    renderHost()
    const button = await screen.findByTestId('host-free-space-button')
    expect(button).toHaveTextContent('Nothing to free up')
    expect(button).toBeDisabled()
  })

  it('opens the dialog from /?tab=host&free=1 and from a card\'s Clean up', async () => {
    const user = userEvent.setup()
    const view = renderHost('/?tab=host&free=1')
    await screen.findByTestId('host-disk-bar')
    expect(screen.getByRole('dialog', { name: 'Free up space' })).toBeInTheDocument()
    view.unmount()

    renderHost()
    const card = await screen.findByTestId('host-card-images')
    expect(screen.queryByRole('dialog')).toBeNull()
    await user.click(within(card).getByRole('button', { name: 'Clean up images' }))
    expect(screen.getByRole('dialog', { name: 'Free up space' })).toBeInTheDocument()
  })

  it('reads "size unknown" for volumes when Docker could not size them, never 0 B', async () => {
    hostSummary.mockResolvedValue(
      makeSummary({
        disk: { total: 10 * GiB, inUse: 8 * GiB, reclaimable: 2 * GiB, reclaimableAll: 2 * GiB, volumesSizeKnown: false, containersSizeKnown: true },
        volumes: { count: 4, attached: 2, attachedKnown: true, bytes: 0, sizeKnown: false, reclaimableCount: 2, reclaimableBytes: 0 },
      }),
    )
    renderHost()
    const card = await screen.findByTestId('host-card-volumes')
    expect(within(card).getByText('size unknown')).toBeInTheDocument()
    expect(screen.getByTestId('host-card-volumes-reclaim')).toHaveTextContent('2 unattached volumes · size unknown')
    expect(card).not.toHaveTextContent('0 B')
  })

  // agent-os-rwik: a container Docker could not measure (SizeRw -1) is skipped
  // from the sums, so both the reclaim figure and the disk total are floors.
  it('reads "at least" for containers and notes the total when Docker could not size every container', async () => {
    hostSummary.mockResolvedValue(
      makeSummary({
        disk: { total: 10 * GiB, inUse: 8 * GiB, reclaimable: 2 * GiB, reclaimableAll: 3 * GiB, volumesSizeKnown: true, containersSizeKnown: false },
        containers: { count: 20, running: 14, stopped: 6, reclaimableCount: 3, reclaimableBytes: 420 * MiB, reclaimableBytesKnown: false },
      }),
    )
    renderHost()
    await screen.findByTestId('host-card-containers')
    expect(screen.getByTestId('host-card-containers-reclaim')).toHaveTextContent(
      '3 stopped containers outside any stack · at least 420.00 MB',
    )
    expect(
      screen.getByText('Docker could not measure every container, so the totals count only the containers it could.'),
    ).toBeInTheDocument()
  })

  it('reads the containers figure as exact, with no note, when Docker sized every container', async () => {
    hostSummary.mockResolvedValue(makeSummary())
    renderHost()
    await screen.findByTestId('host-card-containers')
    expect(screen.getByTestId('host-card-containers-reclaim')).toHaveTextContent(
      '3 stopped containers outside any stack · 420.00 MB',
    )
    expect(screen.getByTestId('host-card-containers-reclaim')).not.toHaveTextContent('at least')
    expect(screen.queryByText(/could not measure every container/)).not.toBeInTheDocument()
  })

  it('shows a skeleton while the first fetch is pending', async () => {
    hostSummary.mockReturnValue(new Promise(() => {}))
    renderHost()
    expect(screen.getByTestId('host-overview-loading')).toBeInTheDocument()
    expect(screen.queryByTestId('host-disk-bar')).toBeNull()
    expect(screen.queryByTestId('host-free-space-button')).toBeNull()
  })

  it('shows LoadFailedNotice with Retry when the first fetch fails, and no numbers', async () => {
    const user = userEvent.setup()
    hostSummary.mockRejectedValueOnce(new Error('boom'))
    renderHost()
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('Could not load the Docker host summary.')
    expect(screen.queryByTestId('host-disk-bar')).toBeNull()
    expect(screen.queryByTestId('host-free-space-button')).toBeNull()
    expect(screen.queryByTestId('host-card-images')).toBeNull()

    await user.click(within(alert).getByRole('button', { name: 'Retry' }))
    await screen.findByTestId('host-disk-bar')
    expect(hostSummary).toHaveBeenCalledTimes(2)
  })

  it('keeps the last numbers and shows RefreshFailedNotice when a refetch fails', async () => {
    const { queryClient } = renderHost()
    await screen.findByTestId('host-disk-bar')
    hostSummary.mockRejectedValueOnce(new Error('boom'))
    await queryClient.refetchQueries({ queryKey: queryKeys.resources.hostSummary() })

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('Could not refresh the Docker host summary.')
    expect(screen.getByTestId('host-disk-bar')).toHaveAttribute('aria-label', '8.00 GB in use, 2.00 GB reclaimable')
    expect(screen.queryByText(/Could not load/)).toBeNull()
  })

  it('mounts the automatic cleanup panel on the overview, even when the summary is unreadable', async () => {
    hostSummary.mockRejectedValueOnce(new Error('boom'))
    renderHost()
    await screen.findByRole('alert')
    expect(screen.getByTestId('auto-cleanup-panel-stub')).toBeInTheDocument()
  })

  it('polls the summary every 60 s', async () => {
    const { queryClient } = renderHost()
    await screen.findByTestId('host-disk-bar')
    const observers = queryClient.getQueryCache().find({ queryKey: queryKeys.resources.hostSummary() })!.observers
    expect(observers.length).toBeGreaterThan(0)
    for (const o of observers) expect(o.options.refetchInterval).toBe(60_000)
  })
})

function metric(cpuPercent: number, overrides: Partial<ContainerMetric> = {}): ContainerMetric {
  return {
    cpuPercent,
    memUsage: 100 * MiB,
    memLimit: 1024 * MiB,
    memPercent: 10,
    netRx: 0,
    netTx: 0,
    blockRead: 0,
    blockWrite: 0,
    memSwap: 0,
    pids: 1,
    ...overrides,
  }
}

function history(name: string, cpu: number[]): ContainerMetricHistory {
  return { containerId: `id-${name}`, name, metrics: cpu.map((c) => metric(c)) }
}

/** Positions in document order; fails when an element is missing. */
function inDocumentOrder(elements: (Element | null)[]) {
  for (const el of elements) expect(el).not.toBeNull()
  for (let i = 1; i < elements.length; i++) {
    const relation = elements[i - 1]!.compareDocumentPosition(elements[i]!)
    expect(relation & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  }
}

describe('HostTab overview layout (AC4)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    resetMetrics()
    hostSummary.mockResolvedValue(makeSummary())
    dashboardStats.mockResolvedValue({ totalContainers: 7, runningContainers: 14 })
  })

  it('shows Live, disk use, the five cards, Busiest containers and the cleanup panel, in that order', async () => {
    renderHost()
    await screen.findByTestId('host-disk-bar')
    inDocumentOrder([
      screen.getByRole('region', { name: 'Live' }),
      screen.getByRole('region', { name: 'Docker disk use' }),
      ...CATEGORIES.map((c) => screen.getByTestId(`host-card-${c}`)),
      screen.getByRole('region', { name: 'Busiest containers' }),
      screen.getByTestId('auto-cleanup-panel-stub'),
    ])
    expect(screen.getByTestId('auto-cleanup-panel-stub')).toHaveAttribute('id', 'automatic-cleanup')
  })

  it('lists at most five busiest containers, highest CPU first, and links to all of them', async () => {
    metrics.value = {
      ...metrics.value,
      isConnected: true,
      aggregates: { totalCpuPercent: 0, totalMemUsage: 0, totalMemLimit: 0, totalMemPercent: 0, totalNetRx: 0, totalNetTx: 0, totalSwap: 0, totalPids: 0 },
      containers: [5, 50, 1, 30, 20, 40, 10].map((cpu) => history(`c${cpu}`, [cpu])),
    }
    renderHost()
    await screen.findByTestId('host-disk-bar')
    const rows = screen.getAllByTestId('busiest-container-row')
    expect(rows.map((r) => r.querySelector('[title]')?.getAttribute('title'))).toEqual(['c50', 'c40', 'c30', 'c20', 'c10'])
    const all = screen.getByRole('link', { name: /^All 20 containers/ })
    expect(all).toHaveAttribute('href', '/?tab=host&view=containers')
  })

  it('says it is waiting for data when no container metrics have arrived', async () => {
    renderHost()
    await screen.findByTestId('host-disk-bar')
    const busiest = screen.getByRole('region', { name: 'Busiest containers' })
    expect(busiest).toHaveTextContent('No live container metrics yet. Waiting for data...')
    expect(within(busiest).queryAllByTestId('busiest-container-row')).toHaveLength(0)
  })

  it('moves swap onto the Memory tile and keeps "unavailable" processes free of a container count', async () => {
    metrics.value = {
      ...metrics.value,
      isConnected: true,
      aggregates: { totalCpuPercent: 12.5, totalMemUsage: 2 * GiB, totalMemLimit: 8 * GiB, totalMemPercent: 25, totalNetRx: 3 * MiB, totalNetTx: 1 * MiB, totalSwap: 210 * MiB, totalPids: 412 },
    }
    dashboardStats.mockResolvedValue({ totalContainers: 7 })
    renderHost()
    const memory = await screen.findByTestId('host-live-memory')
    expect(memory).toHaveTextContent('2.00 GB of 8.00 GB')
    expect(memory).toHaveTextContent('25% · swap 210.00 MB')
    const processes = screen.getByTestId('host-live-processes')
    expect(processes).toHaveTextContent('412')
    expect(processes).not.toHaveTextContent('across')
  })

  it('reads the running-container count beside processes when Docker reports it', async () => {
    metrics.value = {
      ...metrics.value,
      isConnected: true,
      aggregates: { totalCpuPercent: 0, totalMemUsage: 0, totalMemLimit: 0, totalMemPercent: 0, totalNetRx: 0, totalNetTx: 0, totalSwap: 0, totalPids: 412 },
    }
    renderHost()
    await waitFor(() => expect(screen.getByTestId('host-live-processes')).toHaveTextContent('412across 14 running containers'))
  })

  it('shows dashes, not zeros, on the Live tiles until the metrics socket connects', async () => {
    renderHost()
    await screen.findByTestId('host-disk-bar')
    for (const tile of ['cpu', 'memory', 'network', 'processes']) {
      expect(screen.getByTestId(`host-live-${tile}`)).toHaveTextContent('—')
      expect(screen.getByTestId(`host-live-${tile}`)).not.toHaveTextContent(/\b0\b/)
    }
  })

  // The tile reads aggregates.totalCpuPercent (useMetricsBase: the sum of each
  // container's latest cpuPercent). The trend's last point must be that sum,
  // with histories of unequal length aligned on their newest sample.
  it('draws the CPU trend from every container, aligned on the newest sample, ending at the tile number', async () => {
    const containers = [history('old', [10, 20, 30, 40]), history('new', [5, 7])]
    metrics.value = {
      ...metrics.value,
      isConnected: true,
      containers,
      aggregates: { totalCpuPercent: 47, totalMemUsage: 0, totalMemLimit: 0, totalMemPercent: 0, totalNetRx: 0, totalNetTx: 0, totalSwap: 0, totalPids: 0 },
    }
    renderHost()
    const trend = await screen.findByTestId('host-live-cpu-trend')
    expect(trend).toHaveAttribute('data-last', '47')
    expect(screen.getByTestId('host-live-cpu')).toHaveTextContent('47.0%')
  })
})

describe('HostTab view switch and list views (AC2)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    resetMetrics()
    hostSummary.mockResolvedValue(makeSummary())
    dashboardStats.mockResolvedValue({ totalContainers: 7 })
  })

  it.each([
    ['images', 'tab-images'],
    ['volumes', 'tab-volumes'],
    ['networks', 'tab-networks'],
    ['build-cache', 'tab-build-cache'],
    ['containers', 'tab-containers'],
  ])('/?tab=host&view=%s renders its list, not the overview', async (view, testId) => {
    renderHost(`/?tab=host&view=${view}`)
    expect(await screen.findByTestId(testId)).toBeInTheDocument()
    expect(screen.queryByTestId('host-disk-bar')).toBeNull()
    expect(screen.queryByTestId('auto-cleanup-panel-stub')).toBeNull()
    const nav = screen.getByRole('navigation', { name: 'Host views' })
    expect(within(nav).getByRole('link', { current: 'page' })).toHaveAttribute('href', `/?tab=host&view=${view}`)
  })

  it.each(['/?tab=host', '/?tab=host&view=bogus', '/?tab=host&view='])('%s shows the overview', async (url) => {
    renderHost(url)
    expect(await screen.findByTestId('host-disk-bar')).toBeInTheDocument()
    const nav = screen.getByRole('navigation', { name: 'Host views' })
    expect(within(nav).getByRole('link', { current: 'page' })).toHaveTextContent('Overview')
  })

  it('links every view under one switch, with counts for containers, images, volumes and networks', async () => {
    renderHost()
    await screen.findByTestId('host-disk-bar')
    const nav = screen.getByRole('navigation', { name: 'Host views' })
    const links = within(nav).getAllByRole('link')
    expect(links.map((l) => [l.textContent, l.getAttribute('href')])).toEqual([
      ['Overview', '/?tab=host'],
      ['Containers20', '/?tab=host&view=containers'],
      ['Images48', '/?tab=host&view=images'],
      ['Volumes22', '/?tab=host&view=volumes'],
      ['Networks11', '/?tab=host&view=networks'],
      ['Build cache', '/?tab=host&view=build-cache'],
    ])
  })

  it('shows no counts, not zeros, while the summary is unreadable', async () => {
    hostSummary.mockRejectedValue(new Error('boom'))
    renderHost()
    await screen.findByRole('alert')
    const nav = screen.getByRole('navigation', { name: 'Host views' })
    expect(within(nav).getAllByRole('link').map((l) => l.textContent)).toEqual([
      'Overview',
      'Containers',
      'Images',
      'Volumes',
      'Networks',
      'Build cache',
    ])
  })

  it('/?tab=host&view=containers feeds the dashboard stats and metrics status to the containers list', async () => {
    renderHost('/?tab=host&view=containers')
    const tab = await screen.findByTestId('tab-containers')
    await waitFor(() => expect(tab).toHaveAttribute('data-total', '7'))
    expect(tab).toHaveAttribute('data-metrics-status', 'open')
  })
})

describe('HostTab Free up (AC5)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    resetMetrics()
    hostSummary.mockResolvedValue(makeSummary())
    dashboardStats.mockResolvedValue({ totalContainers: 7 })
  })

  it.each(['containers', 'images', 'volumes', 'networks', 'build-cache'])(
    'opens the dialog from view=%s and keeps the view when it closes',
    async (view) => {
      const user = userEvent.setup()
      renderHost(`/?tab=host&view=${view}`)
      await user.click(await screen.findByTestId('host-free-space-button'))
      expect(screen.getByRole('dialog', { name: 'Free up space' })).toBeInTheDocument()
      expect(new URLSearchParams(window.location.search).get('view')).toBe(view)
      expect(new URLSearchParams(window.location.search).get('free')).toBe('1')

      await user.click(screen.getByRole('button', { name: 'Close stub' }))
      expect(screen.queryByRole('dialog')).toBeNull()
      expect(window.location.search).toBe(`?tab=host&view=${view}`)
    },
  )

  it('opens from /?tab=host&view=images&free=1', async () => {
    renderHost('/?tab=host&view=images&free=1')
    expect(await screen.findByRole('dialog', { name: 'Free up space' })).toBeInTheDocument()
    expect(screen.getByTestId('tab-images')).toBeInTheDocument()
  })

  it('keeps the #hash when the dialog opens and closes', async () => {
    const user = userEvent.setup()
    renderHost('/?tab=host#automatic-cleanup')
    await user.click(await screen.findByTestId('host-free-space-button'))
    expect(window.location.hash).toBe('#automatic-cleanup')
    await user.click(screen.getByRole('button', { name: 'Close stub' }))
    expect(window.location.search).toBe('?tab=host')
    expect(window.location.hash).toBe('#automatic-cleanup')
  })
})

describe('HostTab cleanup anchor readiness (AC8)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    resetMetrics()
    dashboardStats.mockResolvedValue({ totalContainers: 7 })
  })

  it('holds the panel scroll while the summary loads and releases it once the summary settles', async () => {
    let resolve!: (s: HostSummary) => void
    hostSummary.mockReturnValue(new Promise<HostSummary>((r) => (resolve = r)))
    renderHost('/?tab=host#automatic-cleanup')
    expect(screen.getByTestId('auto-cleanup-panel-stub')).toHaveAttribute('data-ready', 'false')
    resolve(makeSummary())
    await screen.findByTestId('host-disk-bar')
    expect(screen.getByTestId('auto-cleanup-panel-stub')).toHaveAttribute('data-ready', 'true')
  })

  it('releases it on a failed first load too, so an unreadable summary does not block the scroll', async () => {
    hostSummary.mockRejectedValue(new Error('boom'))
    renderHost('/?tab=host#automatic-cleanup')
    await screen.findByRole('alert')
    expect(screen.getByTestId('auto-cleanup-panel-stub')).toHaveAttribute('data-ready', 'true')
  })
})

describe('No /host breadcrumbs (AC3)', () => {
  it('treats /host as an unknown page', () => {
    expect(getBreadcrumbs('/host').map((c) => c.label)).toEqual(['Dashboard'])
    expect(getBreadcrumbs('/host/images').map((c) => c.label)).toEqual(['Dashboard'])
  })

  it('leaves the other pages alone', () => {
    expect(getBreadcrumbs('/').map((c) => c.label)).toEqual(['Dashboard'])
    expect(getBreadcrumbs('/settings').map((c) => c.label)).toEqual(['Dashboard', 'Settings'])
  })
})
