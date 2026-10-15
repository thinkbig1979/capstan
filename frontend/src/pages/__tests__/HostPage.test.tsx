import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { BrowserRouter, Routes, Route } from 'react-router'
import type { HostSummary } from '@/types/generated-services'

// agent-os-8y80: the Docker host page (/host and /host/:view).
const hostSummary = vi.fn()
const dashboardStats = vi.fn()

vi.mock('@/lib/api', () => ({
  resourcesApi: { hostSummary: (...args: unknown[]) => hostSummary(...args) },
  dashboardApi: { stats: (...args: unknown[]) => dashboardStats(...args) },
}))

vi.mock('@/hooks/DashboardMetricsContext', () => ({
  useDashboardMetricsContext: () => ({
    containers: [],
    aggregates: {},
    latestMetrics: {},
    isConnected: false,
    ws: { status: 'open' },
  }),
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

import { HostPage } from '../HostPage'
import { getBreadcrumbs } from '@/components/layout/breadcrumbs'
import { queryKeys } from '@/lib/query-keys'

const GiB = 1024 ** 3
const MiB = 1024 ** 2

function makeSummary(overrides: Partial<HostSummary> = {}): HostSummary {
  return {
    disk: { total: 10 * GiB, inUse: 8 * GiB, reclaimable: 2 * GiB, reclaimableAll: 3 * GiB, volumesSizeKnown: true },
    containers: { count: 20, running: 14, stopped: 6, reclaimableCount: 3, reclaimableBytes: 420 * MiB },
    images: { count: 48, inUse: 31, bytes: 21 * GiB, reclaimableCount: 17, reclaimableBytes: 14 * GiB },
    volumes: { count: 22, attached: 19, bytes: 5 * GiB, sizeKnown: true, reclaimableCount: 3, reclaimableBytes: 1 * GiB },
    networks: { count: 11, builtIn: 3, stackOwned: 8, reclaimableCount: 0 },
    buildCache: { count: 40, bytes: 2 * GiB, reclaimableBytes: 2 * GiB },
    measuredAt: '2026-10-14T10:00:00Z',
    ...overrides,
  }
}

function renderHost(route = '/host') {
  window.history.pushState({}, 'Test page', route)
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: Infinity }, mutations: { retry: false } },
  })
  return {
    ...render(
      <BrowserRouter>
        <QueryClientProvider client={queryClient}>
          <Routes>
            <Route path="/host" element={<HostPage />} />
            <Route path="/host/:view" element={<HostPage />} />
          </Routes>
        </QueryClientProvider>
      </BrowserRouter>,
    ),
    queryClient,
  }
}

const CATEGORIES = ['containers', 'images', 'volumes', 'networks', 'build-cache'] as const

describe('HostPage overview', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    hostSummary.mockResolvedValue(makeSummary())
    dashboardStats.mockResolvedValue({ totalContainers: 7 })
  })

  it('renders the page heading', async () => {
    renderHost()
    expect(screen.getByTestId('host-page')).toBeInTheDocument()
    expect(screen.getByRole('heading', { level: 1, name: "What's on this server" })).toBeInTheDocument()
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
      expect(link).toHaveAttribute('href', `/host/${c}`)
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
    expect(window.location.search).toBe('?free=1')

    await user.click(screen.getByRole('button', { name: 'Close stub' }))
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(window.location.search).toBe('')
  })

  it('disables the button as "Nothing to free up" when nothing is reclaimable', async () => {
    hostSummary.mockResolvedValue(
      makeSummary({ disk: { total: 8 * GiB, inUse: 8 * GiB, reclaimable: 0, reclaimableAll: 0, volumesSizeKnown: true } }),
    )
    renderHost()
    const button = await screen.findByTestId('host-free-space-button')
    expect(button).toHaveTextContent('Nothing to free up')
    expect(button).toBeDisabled()
  })

  it('opens the dialog from /host?free=1 and from a card\'s Clean up', async () => {
    const user = userEvent.setup()
    const view = renderHost('/host?free=1')
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
        disk: { total: 10 * GiB, inUse: 8 * GiB, reclaimable: 2 * GiB, reclaimableAll: 2 * GiB, volumesSizeKnown: false },
        volumes: { count: 4, attached: 2, bytes: 0, sizeKnown: false, reclaimableCount: 2, reclaimableBytes: 0 },
      }),
    )
    renderHost()
    const card = await screen.findByTestId('host-card-volumes')
    expect(within(card).getByText('size unknown')).toBeInTheDocument()
    expect(screen.getByTestId('host-card-volumes-reclaim')).toHaveTextContent('2 unattached volumes · size unknown')
    expect(card).not.toHaveTextContent('0 B')
  })

  it('shows a skeleton while the first fetch is pending', async () => {
    hostSummary.mockReturnValue(new Promise(() => {}))
    renderHost()
    expect(screen.getByTestId('host-page-loading')).toBeInTheDocument()
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

  it('polls the summary every 60 s', async () => {
    const { queryClient } = renderHost()
    await screen.findByTestId('host-disk-bar')
    const observers = queryClient.getQueryCache().find({ queryKey: queryKeys.resources.hostSummary() })!.observers
    expect(observers.length).toBeGreaterThan(0)
    for (const o of observers) expect(o.options.refetchInterval).toBe(60_000)
  })
})

describe('HostPage list views', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    hostSummary.mockResolvedValue(makeSummary())
    dashboardStats.mockResolvedValue({ totalContainers: 7 })
  })

  it.each([
    ['images', 'tab-images'],
    ['volumes', 'tab-volumes'],
    ['networks', 'tab-networks'],
    ['build-cache', 'tab-build-cache'],
  ])('/host/%s renders its existing list under the host nav', async (view, testId) => {
    renderHost(`/host/${view}`)
    expect(await screen.findByTestId(testId)).toBeInTheDocument()
    const nav = screen.getByRole('navigation', { name: 'Docker host' })
    expect(within(nav).getByRole('link', { name: 'Overview' })).toHaveAttribute('href', '/host')
    expect(screen.queryByTestId('host-disk-bar')).toBeNull()
  })

  it('/host/containers feeds the dashboard stats and metrics status to the containers list', async () => {
    renderHost('/host/containers')
    const tab = await screen.findByTestId('tab-containers')
    await waitFor(() => expect(tab).toHaveAttribute('data-total', '7'))
    expect(tab).toHaveAttribute('data-metrics-status', 'open')
  })

  it('sends an unknown view back to /host', async () => {
    renderHost('/host/bogus')
    await waitFor(() => expect(window.location.pathname).toBe('/host'))
    expect(await screen.findByTestId('host-disk-bar')).toBeInTheDocument()
  })
})

describe('Header breadcrumbs on /host', () => {
  it('reads Dashboard > Docker host on the overview', () => {
    expect(getBreadcrumbs('/host').map((c) => c.label)).toEqual(['Dashboard', 'Docker host'])
  })

  it('adds the view on a list view', () => {
    expect(getBreadcrumbs('/host/images')).toEqual([
      { label: 'Dashboard', path: '/' },
      { label: 'Docker host', path: '/host' },
      { label: 'Images', path: '/host/images' },
    ])
    expect(getBreadcrumbs('/host/build-cache').map((c) => c.label)).toEqual(['Dashboard', 'Docker host', 'Build cache'])
  })

  it('leaves the other pages alone', () => {
    expect(getBreadcrumbs('/').map((c) => c.label)).toEqual(['Dashboard'])
    expect(getBreadcrumbs('/settings').map((c) => c.label)).toEqual(['Dashboard', 'Settings'])
  })
})
