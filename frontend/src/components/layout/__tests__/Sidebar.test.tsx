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
import { resourcesApi, stacksApi, settingsApi } from '@/lib/api'
import { queryKeys } from '@/lib/query-keys'

const checkUpdatesMock = vi.mocked(resourcesApi.checkUpdates)
const listMock = vi.mocked(stacksApi.list)
const getConfigMock = vi.mocked(settingsApi.getConfig)

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
  // sidebar-search/-filter/-sort/-collapsed persist to localStorage; without
  // clearing, state leaks between tests in this file (a pre-existing gap).
  localStorage.clear()
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
      await queryClient.invalidateQueries({ queryKey: queryKeys.resources.updates() })
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

  it('filters the stack list by search query and clears via the Clear button', async () => {
    renderSidebar()
    await waitFor(() => expect(screen.getAllByText('alpha').length).toBeGreaterThan(0))

    const search = screen.getByLabelText('Search stacks')
    await act(async () => {
      fireEvent.change(search, { target: { value: 'alp' } })
    })

    expect(screen.queryByText('bravo')).not.toBeInTheDocument()
    expect(screen.getAllByText('alpha').length).toBeGreaterThan(0)
    expect(screen.getAllByText('1 of 2 stacks').length).toBeGreaterThan(0)

    await act(async () => {
      fireEvent.click(screen.getAllByText('Clear')[0])
    })

    await waitFor(() => expect(screen.getAllByText('bravo').length).toBeGreaterThan(0))
    expect(screen.queryByText(/of 2 stacks/)).not.toBeInTheDocument()
  })

  it('filters the stack list by status', async () => {
    renderSidebar()
    await waitFor(() => expect(screen.getAllByText('alpha').length).toBeGreaterThan(0))

    await act(async () => {
      fireEvent.click(screen.getAllByRole('button', { name: /Stopped/ })[0])
    })

    expect(screen.queryByText('alpha')).not.toBeInTheDocument()
    expect(screen.getAllByText('bravo').length).toBeGreaterThan(0)
  })

  it('shows an empty state message when filters exclude every stack', async () => {
    renderSidebar()
    await waitFor(() => expect(screen.getAllByText('alpha').length).toBeGreaterThan(0))

    const search = screen.getByLabelText('Search stacks')
    await act(async () => {
      fireEvent.change(search, { target: { value: 'nonexistent' } })
    })

    expect(screen.getAllByText('No stacks match filters').length).toBeGreaterThan(0)
  })

  it('toggles sort order between name and status', async () => {
    listMock.mockResolvedValueOnce([
      { id: 's1', projectName: 'alpha', status: 'running', containers: [], directory: '/stacks', isGitRepo: false, gitDirty: false },
      { id: 's2', projectName: 'bravo', status: 'stopped', containers: [], directory: '/stacks', isGitRepo: false, gitDirty: false },
      { id: 's3', projectName: 'charlie', status: 'error', containers: [], directory: '/stacks', isGitRepo: false, gitDirty: false },
    ] as never)
    renderSidebar()
    await waitFor(() => expect(screen.getAllByText('alpha').length).toBeGreaterThan(0))

    // sidebarContent renders twice (mobile overlay + desktop aside), so the
    // matches come in two identical, consecutive triplets; the first 3 are enough.
    const nameOrder = screen
      .getAllByText(/^(alpha|bravo|charlie)$/)
      .slice(0, 3)
      .map((el) => el.textContent)
    expect(nameOrder).toEqual(['alpha', 'bravo', 'charlie'])

    await act(async () => {
      fireEvent.click(screen.getAllByTitle(/Sort by name/)[0])
    })

    await waitFor(() => expect(screen.getAllByTitle(/Sort by status/).length).toBeGreaterThan(0))
    const statusOrder = screen
      .getAllByText(/^(alpha|bravo|charlie)$/)
      .slice(0, 3)
      .map((el) => el.textContent)
    // error < running < stopped alphabetically -> charlie, alpha, bravo.
    expect(statusOrder).toEqual(['charlie', 'alpha', 'bravo'])
  })

  it('persists the search query to localStorage and restores it on remount', async () => {
    const { unmount } = renderSidebar()
    await waitFor(() => expect(screen.getAllByText('alpha').length).toBeGreaterThan(0))

    const search = screen.getByLabelText('Search stacks')
    await act(async () => {
      fireEvent.change(search, { target: { value: 'brav' } })
    })

    await waitFor(() => expect(localStorage.getItem('sidebar-search')).toBe('brav'))
    unmount()

    renderSidebar()
    await waitFor(() => expect(screen.getAllByText('bravo').length).toBeGreaterThan(0))
    expect(screen.queryByText('alpha')).not.toBeInTheDocument()
    expect(screen.getByLabelText('Search stacks')).toHaveValue('brav')
  })

  it('collapses a tree group and persists collapsed state under the versioned storage key', async () => {
    const groupedStacks = [
      { id: 's1', projectName: 'alpha', status: 'running', containers: [], directory: '/stacks/groupA', isGitRepo: false, gitDirty: false },
      { id: 's2', projectName: 'bravo', status: 'stopped', containers: [], directory: '/stacks/groupB', isGitRepo: false, gitDirty: false },
    ] as never
    getConfigMock.mockResolvedValueOnce({ stacksDir: '/stacks', stacksDirectories: ['/stacks'] } as never)
    listMock.mockResolvedValueOnce(groupedStacks)
    const { unmount } = renderSidebar()

    await waitFor(() => expect(screen.getAllByText('alpha').length).toBeGreaterThan(0))
    expect(screen.getAllByText('bravo').length).toBeGreaterThan(0)

    await act(async () => {
      fireEvent.click(screen.getAllByTitle('/stacks/groupA')[0])
    })

    expect(screen.queryByText('alpha')).not.toBeInTheDocument()
    expect(screen.getAllByText('bravo').length).toBeGreaterThan(0)

    await waitFor(() => {
      const stored = JSON.parse(localStorage.getItem('sidebar-collapsed:v1') || '[]')
      expect(stored).toContain('/stacks/groupA')
    })

    unmount()

    // Remount: the collapsed group stays collapsed because state was persisted.
    getConfigMock.mockResolvedValueOnce({ stacksDir: '/stacks', stacksDirectories: ['/stacks'] } as never)
    listMock.mockResolvedValueOnce(groupedStacks)
    renderSidebar()

    await waitFor(() => expect(screen.getAllByText('bravo').length).toBeGreaterThan(0))
    expect(screen.queryByText('alpha')).not.toBeInTheDocument()
  })

  it('renders the collapsed navigation rail with stack count when the sidebar is closed', async () => {
    useUIStore.setState({ sidebarOpen: false, pinnedStacks: [] })
    renderSidebar()

    await waitFor(() => expect(screen.getByLabelText('Expand sidebar')).toBeInTheDocument())
    expect(screen.getByLabelText('Dashboard')).toBeInTheDocument()
    expect(screen.getByLabelText('Settings')).toBeInTheDocument()
    await waitFor(() => expect(screen.getByLabelText('Stacks (2)')).toBeInTheDocument())
  })
})
