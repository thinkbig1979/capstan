import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { BrowserRouter } from 'react-router'
import type { ConfiguredDir, Stack } from '@/types'

// Page-level test: pins DashboardPage's own contract (which tab renders for a
// given URL, loading/error branches, the subtitle it computes) without
// exercising each tab component's internals — those have their own coverage.
const listDirectories = vi.fn()
const listStacks = vi.fn()
const scanDirectories = vi.fn()
const dashboardStats = vi.fn()
const getConfig = vi.fn()

vi.mock('@/lib/api', () => ({
  directoriesApi: {
    list: (...args: unknown[]) => listDirectories(...args),
    scan: (...args: unknown[]) => scanDirectories(...args),
  },
  stacksApi: {
    list: (...args: unknown[]) => listStacks(...args),
    start: vi.fn().mockResolvedValue({ outcome: 'success' }),
    stop: vi.fn().mockResolvedValue({ outcome: 'success' }),
    restart: vi.fn().mockResolvedValue({ outcome: 'success' }),
    delete: vi.fn().mockResolvedValue({ outcome: 'success' }),
  },
  dashboardApi: {
    stats: (...args: unknown[]) => dashboardStats(...args),
  },
  settingsApi: {
    getConfig: (...args: unknown[]) => getConfig(...args),
  },
}))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), warning: vi.fn(), info: vi.fn() },
}))

// useDashboardMetrics opens a real WebSocket via useMetricsBase; there is no WS
// shim in test/setup.ts and the hook has its own dedicated coverage, so it's
// mocked here to keep this suite focused on page-level wiring.
vi.mock('@/hooks/DashboardMetricsContext', () => ({
  useDashboardMetricsContext: () => ({
    containers: [],
    aggregates: {},
    latestMetrics: {},
    isConnected: false,
    ws: { status: 'closed' },
  }),
}))

// Thin testid stubs for every tab/header child — page-level test targets
// routing/data-plumbing into these components, not their internals.
vi.mock('@/components/dashboard/DashboardHeader', () => ({
  DashboardHeader: ({ subtitle, onCreateStack, onRefresh, isRefreshing }: {
    subtitle?: string
    onCreateStack: () => void
    onRefresh: () => void
    isRefreshing: boolean
  }) => (
    <div data-testid="dashboard-header">
      <span>{subtitle}</span>
      <button onClick={onCreateStack}>New Stack</button>
      <button onClick={onRefresh} disabled={isRefreshing}>Refresh</button>
    </div>
  ),
}))
vi.mock('@/components/dashboard/DashboardMetricsTab', () => ({
  DashboardMetricsTab: () => <div data-testid="tab-overview" />,
}))
// Surfaces the sort/filter the page hands down, so the localStorage-restore
// contract is observable without exercising StacksTab's internals.
vi.mock('@/components/dashboard/StacksTab', () => ({
  StacksTab: ({ sortBy, statusFilter }: { sortBy: string; statusFilter: string }) => (
    <div data-testid="tab-stacks" data-sort-by={sortBy} data-status-filter={statusFilter} />
  ),
}))
vi.mock('@/components/dashboard/ContainersOverviewTab', () => ({
  ContainersOverviewTab: () => <div data-testid="tab-containers" />,
}))
vi.mock('@/components/dashboard/DirectoriesTab', () => ({
  DirectoriesTab: () => <div data-testid="tab-directories" />,
}))
vi.mock('@/components/dashboard/UpdatesTab', () => ({
  UpdatesTab: () => <div data-testid="tab-updates" />,
}))
vi.mock('@/components/dashboard/ImagesTab', () => ({
  ImagesTab: () => <div data-testid="tab-images" />,
}))
vi.mock('@/components/dashboard/VolumesTab', () => ({
  VolumesTab: () => <div data-testid="tab-volumes" />,
}))
vi.mock('@/components/dashboard/NetworksTab', () => ({
  NetworksTab: () => <div data-testid="tab-networks" />,
}))
vi.mock('@/components/dashboard/BuildCacheTab', () => ({
  BuildCacheTab: () => <div data-testid="tab-build-cache" />,
}))
vi.mock('@/components/dashboard/AttentionStrip', () => ({
  AttentionStrip: () => <div data-testid="attention-strip" />,
}))
vi.mock('@/components/dashboard/HostStrip', () => ({
  HostStrip: ({ activeView }: { activeView?: string }) => (
    <div data-testid="host-strip" data-active-view={activeView} />
  ),
}))

import { DashboardPage } from '../DashboardPage'

function makeStack(overrides: Partial<Stack>): Stack {
  return {
    id: 's1',
    directory: '/stacks/s1',
    composeFile: 'docker-compose.yml',
    projectName: 's1',
    status: 'running',
    isGitRepo: false,
    gitDirty: false,
    gitAhead: 0,
    gitBehind: 0,
    containers: [],
    ...overrides,
  }
}

function makeDir(overrides: Partial<ConfiguredDir>): ConfiguredDir {
  return {
    path: '/stacks/s1',
    name: 's1',
    ...overrides,
  } as ConfiguredDir
}

function renderPage(route = '/') {
  // BrowserRouter (matching main.tsx's actual router) rather than MemoryRouter,
  // so assertions against window.location reflect real navigation the same way
  // frontend/src/test/utils.tsx's renderWithProviders does.
  window.history.pushState({}, 'Test page', route)
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false } },
  })
  return {
    ...render(
      <BrowserRouter>
        <QueryClientProvider client={queryClient}>
          <DashboardPage />
        </QueryClientProvider>
      </BrowserRouter>,
    ),
    queryClient,
  }
}

describe('DashboardPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    // The page persists sort/filter on every change, so state leaks between
    // tests unless this is cleared.
    localStorage.clear()
    listDirectories.mockResolvedValue([])
    listStacks.mockResolvedValue([])
    dashboardStats.mockResolvedValue({ runningContainers: 0 })
    getConfig.mockResolvedValue({ stacksDirectories: [] })
  })

  describe('tab routing', () => {
    it('renders the Stacks (fleet) tab by default when no ?tab is present', async () => {
      renderPage('/')
      await waitFor(() => expect(screen.getByTestId('tab-stacks')).toBeInTheDocument())
      expect(screen.queryByTestId('tab-overview')).not.toBeInTheDocument()
    })

    it('keeps the historical ?tab=overview URL working for Metrics', async () => {
      renderPage('/?tab=overview')
      await waitFor(() => expect(screen.getByTestId('tab-overview')).toBeInTheDocument())
      expect(screen.queryByTestId('tab-stacks')).not.toBeInTheDocument()
    })

    it('renders the tab named in the ?tab query param', async () => {
      renderPage('/?tab=updates')
      await waitFor(() => expect(screen.getByTestId('tab-updates')).toBeInTheDocument())
      expect(screen.queryByTestId('tab-overview')).not.toBeInTheDocument()
    })

    it('switches tabs and updates the URL when a tab trigger is clicked', async () => {
      const user = userEvent.setup()
      renderPage('/')
      await waitFor(() => expect(screen.getByTestId('tab-stacks')).toBeInTheDocument())

      // Radix Tabs switches on focus (default activationMode="automatic"), which
      // fireEvent.click does not simulate — userEvent.click does.
      await user.click(screen.getByRole('tab', { name: 'Metrics' }))

      await waitFor(() => expect(screen.getByTestId('tab-overview')).toBeInTheDocument())
      expect(screen.queryByTestId('tab-stacks')).not.toBeInTheDocument()
      expect(window.location.search).toBe('?tab=overview')
    })
  })

  describe('loading state', () => {
    it('shows loading skeletons and no tab content while directories/stacks are in flight', async () => {
      listDirectories.mockReturnValue(new Promise(() => {}))
      listStacks.mockReturnValue(new Promise(() => {}))

      renderPage('/')

      expect(screen.getByText('Loading...')).toBeInTheDocument()
      expect(screen.queryByTestId('tab-stacks')).not.toBeInTheDocument()
      expect(screen.queryByTestId('dashboard-header')).not.toBeInTheDocument()
    })
  })

  describe('error path', () => {
    it('shows an error card when stacks fail to load, and Retry re-fetches', async () => {
      listStacks.mockRejectedValue(new Error('stacks down'))
      listDirectories.mockResolvedValue([])

      renderPage('/')

      await waitFor(() => expect(screen.getByText('Failed to load dashboard')).toBeInTheDocument(), { timeout: 3000 })
      expect(screen.getByText('stacks down')).toBeInTheDocument()
      expect(screen.queryByTestId('tab-stacks')).not.toBeInTheDocument()

      listStacks.mockClear()
      fireEvent.click(screen.getByRole('button', { name: /Retry/ }))
      await waitFor(() => expect(listStacks).toHaveBeenCalledTimes(1))
    })

    it('shows an error card when directories fail to load', async () => {
      listDirectories.mockRejectedValue(new Error('dirs down'))
      listStacks.mockResolvedValue([])

      renderPage('/')

      await waitFor(() => expect(screen.getByText('Failed to load dashboard')).toBeInTheDocument(), { timeout: 3000 })
      expect(screen.getByText('dirs down')).toBeInTheDocument()
    })
  })

  describe('page-owned data plumbing', () => {
    it('computes the header subtitle from stack counts', async () => {
      listStacks.mockResolvedValue([
        makeStack({ id: 's1', status: 'running', containers: [{ id: 'c1' } as never] }),
        makeStack({ id: 's2', status: 'stopped', containers: [] }),
      ])
      listDirectories.mockResolvedValue([])

      renderPage('/')

      await waitFor(() =>
        expect(screen.getByText('2 stacks · 1 running · 1 container')).toBeInTheDocument(),
      )
    })

    it('shows the Quick Start empty state when there are no directories or stacks', async () => {
      listStacks.mockResolvedValue([])
      listDirectories.mockResolvedValue([])

      renderPage('/')

      await waitFor(() => expect(screen.getByText('Quick Start')).toBeInTheDocument())
      expect(screen.getByText('Create Your First Stack')).toBeInTheDocument()
    })

    // Pins the localStorage-restore contract itself, which had no coverage
    // before agent-os-14b moved the read out of a mount effect and into the
    // useState initialiser. These assert the restored values are what the page
    // hands to StacksTab; they deliberately do NOT claim to distinguish the two
    // implementations. They cannot: StacksTab is gated behind the loading state,
    // so it first renders after effects have already flushed, and both versions
    // look identical from the DOM. Verified by reverting the fix — these still
    // passed. The guard against a relapse to setState-in-effect is the
    // react-hooks/set-state-in-effect lint rule, not this file.
    it('restores the persisted sort and filter', async () => {
      localStorage.setItem('dashboard-sort', 'status')
      localStorage.setItem('dashboard-filter', 'running')
      listStacks.mockResolvedValue([makeStack({ id: 's1' })])
      listDirectories.mockResolvedValue([makeDir({ path: '/stacks/s1' })])

      renderPage('/?tab=stacks')

      const tab = await screen.findByTestId('tab-stacks')
      expect(tab).toHaveAttribute('data-sort-by', 'status')
      expect(tab).toHaveAttribute('data-status-filter', 'running')
    })

    it('falls back to the defaults when nothing is persisted', async () => {
      listStacks.mockResolvedValue([makeStack({ id: 's1' })])
      listDirectories.mockResolvedValue([makeDir({ path: '/stacks/s1' })])

      renderPage('/?tab=stacks')

      const tab = await screen.findByTestId('tab-stacks')
      expect(tab).toHaveAttribute('data-sort-by', 'name')
      expect(tab).toHaveAttribute('data-status-filter', 'all')
    })

    it('hides the Quick Start empty state once stacks exist', async () => {
      listStacks.mockResolvedValue([makeStack({ id: 's1' })])
      listDirectories.mockResolvedValue([makeDir({ path: '/stacks/s1' })])

      renderPage('/')

      await waitFor(() => expect(screen.getByTestId('dashboard-header')).toBeInTheDocument())
      expect(screen.queryByText('Quick Start')).not.toBeInTheDocument()
    })
  })
})
