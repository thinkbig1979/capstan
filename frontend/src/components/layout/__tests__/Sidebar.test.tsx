import { describe, it, expect, vi, beforeAll, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'

const startMock = vi.fn().mockResolvedValue({ outcome: 'success', reason: 'started' })
const stopMock = vi.fn().mockResolvedValue({ outcome: 'success', reason: 'stopped' })

vi.mock('@/lib/api', () => ({
  stacksApi: {
    list: vi.fn().mockResolvedValue([
      { id: 's1', projectName: 'alpha', status: 'running', containers: [], directory: '/stacks', isGitRepo: false, gitDirty: false },
      { id: 's2', projectName: 'bravo', status: 'stopped', containers: [], directory: '/stacks', isGitRepo: false, gitDirty: false },
    ]),
    start: (id: string) => startMock(id),
    stop: (id: string) => stopMock(id),
    restart: vi.fn().mockResolvedValue({ outcome: 'success' }),
    pull: vi.fn().mockResolvedValue({ outcome: 'success' }),
  },
  settingsApi: { getConfig: vi.fn().mockResolvedValue({ stacksDir: '/stacks', stacksDirectories: [] }) },
  resourcesApi: { checkUpdates: vi.fn().mockResolvedValue({ updates: [{ id: 'u1' }, { id: 'u2' }, { id: 'u3' }] }) },
  backupApi: {
    getStatus: vi.fn().mockResolvedValue({
      schedulerRunning: true,
      lastRun: { finishedAt: new Date(Date.now() - 3600_000).toISOString() },
      nextRunAt: new Date(Date.now() + 7200_000).toISOString(),
      enabledStackCount: 2,
    }),
  },
}))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), warning: vi.fn(), info: vi.fn() },
}))

import { Sidebar } from '../Sidebar'
import { useUIStore } from '@/stores/uiStore'
import { resourcesApi } from '@/lib/api'

const checkUpdatesMock = vi.mocked(resourcesApi.checkUpdates)

// The badge only reads `.updates.length`, so a list of n placeholder rows is enough.
function updatesResult(n: number) {
  return { updates: Array.from({ length: n }, (_, i) => ({ id: `u${i}` })) } as never
}

beforeAll(() => {
  Object.defineProperty(window, 'matchMedia', {
    writable: true,
    value: (query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addListener: () => {},
      removeListener: () => {},
      addEventListener: () => {},
      removeEventListener: () => {},
      dispatchEvent: () => false,
    }),
  })
})

beforeEach(() => {
  startMock.mockClear()
  stopMock.mockClear()
  checkUpdatesMock.mockReset()
  checkUpdatesMock.mockResolvedValue(updatesResult(3))
  useUIStore.setState({ sidebarOpen: true, pinnedStacks: [] })
})

function renderSidebar() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })
  const rendered = render(
    <MemoryRouter>
      <QueryClientProvider client={queryClient}>
        <Sidebar />
      </QueryClientProvider>
    </MemoryRouter>,
  )
  return { ...rendered, queryClient }
}

describe('Sidebar', () => {
  it('renders the stack list', async () => {
    renderSidebar()
    await waitFor(() => expect(screen.getAllByText('alpha').length).toBeGreaterThan(0))
    expect(screen.getAllByText('bravo').length).toBeGreaterThan(0)
  })

  it('shows an aggregate update badge linking to the updates tab', async () => {
    renderSidebar()
    await waitFor(() =>
      expect(screen.getAllByTitle(/updates available/).length).toBeGreaterThan(0),
    )
    const link = screen.getAllByTitle(/updates available/)[0].closest('a')
    expect(link).toHaveAttribute('href', '/?tab=updates')
  })

  // Regression: after an update runs or a fresh scan completes, the update hooks
  // invalidate the canonical ['resources','updates'] query. The sidebar badge must
  // share that key so it re-reads the new count instead of staying stale until refresh.
  it('refreshes the badge count when the updates query is invalidated', async () => {
    const { queryClient } = renderSidebar()
    await waitFor(() =>
      expect(screen.getAllByTitle('3 updates available').length).toBeGreaterThan(0),
    )

    // Simulate the post-update state: one fewer update remains, then invalidate the
    // same query key the update mutations use.
    checkUpdatesMock.mockResolvedValue(updatesResult(1))
    await act(async () => {
      await queryClient.invalidateQueries({ queryKey: ['resources', 'updates'] })
    })

    await waitFor(() =>
      expect(screen.getAllByTitle('1 update available').length).toBeGreaterThan(0),
    )
    expect(screen.queryByTitle('3 updates available')).not.toBeInTheDocument()
  })

  it('shows the backup status footer with a next-run countdown', async () => {
    renderSidebar()
    await waitFor(() => expect(screen.getAllByText(/next in/).length).toBeGreaterThan(0))
    expect(screen.getAllByText(/Backup .* ago/).length).toBeGreaterThan(0)
  })

  it('pins a stack into a Pinned section when its star is clicked', async () => {
    renderSidebar()
    await waitFor(() => expect(screen.getAllByText('alpha').length).toBeGreaterThan(0))
    expect(screen.queryByText('Pinned')).not.toBeInTheDocument()

    await act(async () => {
      fireEvent.click(screen.getAllByLabelText('Pin alpha')[0])
    })

    expect(useUIStore.getState().pinnedStacks).toContain('s1')
    expect(screen.getAllByText('Pinned').length).toBeGreaterThan(0)
  })

  it('runs a bulk start on selected stacks', async () => {
    renderSidebar()
    await waitFor(() => expect(screen.getAllByText('alpha').length).toBeGreaterThan(0))

    // Enter selection mode.
    await act(async () => {
      fireEvent.click(screen.getAllByLabelText('Select stacks')[0])
    })
    // Select all visible stacks.
    await act(async () => {
      fireEvent.click(screen.getAllByText('Select all')[0])
    })
    expect(screen.getAllByText('2 selected').length).toBeGreaterThan(0)

    // Trigger the bulk start.
    await act(async () => {
      fireEvent.click(screen.getAllByTitle('Start selected')[0])
    })

    await waitFor(() => expect(startMock).toHaveBeenCalledTimes(2))
    expect(startMock).toHaveBeenCalledWith('s1')
    expect(startMock).toHaveBeenCalledWith('s2')
  })
})
