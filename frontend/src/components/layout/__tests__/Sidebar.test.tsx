import { describe, it, expect, vi, beforeAll, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor, act, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router'

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
      lastRun: { kind: 'backup', finishedAt: new Date(Date.now() - 3600_000).toISOString() },
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

const row = (id: string, projectName: string, directory: string, containers: unknown[] = []) =>
  ({ id, projectName, status: 'running', containers, directory, isGitRepo: false, gitDirty: false })

// Sidebar renders its body twice (mobile overlay + desktop aside); scope to one.
const desktopAside = (container: HTMLElement) => {
  const aside = container.querySelector<HTMLElement>('aside.hidden')
  if (!aside) throw new Error('desktop aside not rendered')
  return aside
}
const stackLinks = (aside: HTMLElement) =>
  within(aside).getAllByRole('link', { name: /^[a-z]+ - (running|stopped)$/ })
const names = (links: HTMLElement[]) => links.map((l) => l.getAttribute('aria-label')?.split(' - ')[0])

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
    // The footer names the run's kind (agent-os-4zx0).
    expect(screen.getAllByText(/Last backup .* ago/).length).toBeGreaterThan(0)
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

  // agent-os-n97z: paused is a real StackStatus with its own filter button.
  it('filters the stack list to paused stacks', async () => {
    listMock.mockResolvedValueOnce([
      { id: 's1', projectName: 'alpha', status: 'running', containers: [], directory: '/stacks', isGitRepo: false, gitDirty: false },
      { id: 's3', projectName: 'charlie', status: 'paused', containers: [], directory: '/stacks', isGitRepo: false, gitDirty: false },
    ] as never)
    renderSidebar()
    await waitFor(() => expect(screen.getAllByText('charlie').length).toBeGreaterThan(0))

    await act(async () => {
      fireEvent.click(screen.getAllByRole('button', { name: /Paused/ })[0])
    })

    expect(screen.queryByText('alpha')).not.toBeInTheDocument()
    expect(screen.getAllByText('charlie').length).toBeGreaterThan(0)
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

  // The sidebar sort toggle was removed in the phase-2 redesign — sorting is
  // the fleet table's job; the sidebar always lists by name.
  it('lists stacks sorted by name', async () => {
    listMock.mockResolvedValueOnce([
      { id: 's2', projectName: 'bravo', status: 'stopped', containers: [], directory: '/stacks', isGitRepo: false, gitDirty: false },
      { id: 's1', projectName: 'alpha', status: 'running', containers: [], directory: '/stacks', isGitRepo: false, gitDirty: false },
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

    expect(screen.queryByTitle(/Sort by/)).not.toBeInTheDocument()
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

  // agent-os-uxhe: the folder tree is gone. More than one configured directory
  // is the case that used to force grouping (useSidebarData's useGroups).
  describe('flat list, with the directory as a tooltip', () => {
    const twoDirs = () =>
      getConfigMock.mockResolvedValueOnce({ stacksDir: '/srv/a', stacksDirectories: ['/srv/a', '/srv/b'] } as never)

    it('renders one alphabetical row per stack and no folder rows with two configured directories', async () => {
      twoDirs()
      listMock.mockResolvedValueOnce([
        row('s1', 'zeta', '/srv/a'),
        row('s2', 'alpha', '/srv/b'),
        row('s3', 'mike', '/srv/a'),
        row('s4', 'bravo', '/srv/b'),
      ] as never)
      const { container } = renderSidebar()
      const aside = desktopAside(container)
      await waitFor(() => expect(stackLinks(aside)).toHaveLength(4))

      // No folder header, chevron, folder icon or per-folder count.
      expect(aside.querySelectorAll('button[title^="/srv"]')).toHaveLength(0)
      expect(aside.querySelector('.lucide-folder-open, .lucide-chevron-down, .lucide-chevron-right')).toBeNull()
      expect(names(stackLinks(aside))).toEqual(['alpha', 'bravo', 'mike', 'zeta'])
    })

    it('renders two stacks with the same name in different directories as two rows, each titled with its own directory', async () => {
      twoDirs()
      listMock.mockResolvedValueOnce([row('s1', 'web', '/srv/a'), row('s2', 'web', '/srv/b')] as never)
      const { container } = renderSidebar()
      const aside = desktopAside(container)
      await waitFor(() => expect(stackLinks(aside)).toHaveLength(2))

      const links = stackLinks(aside)
      expect(links.map((l) => l.getAttribute('href')).sort()).toEqual(['/stacks/s1', '/stacks/s2'])
      expect(links.map((l) => l.getAttribute('title')).sort()).toEqual(['/srv/a', '/srv/b'])
    })

    it('keeps the container-count badge only on stacks that have containers', async () => {
      listMock.mockResolvedValueOnce([
        row('s1', 'alpha', '/stacks', [{ id: 'c1' }, { id: 'c2' }]),
        row('s2', 'bravo', '/stacks'),
      ] as never)
      const { container } = renderSidebar()
      const aside = desktopAside(container)
      await waitFor(() => expect(stackLinks(aside)).toHaveLength(2))

      const [alpha, bravo] = stackLinks(aside)
      expect(within(alpha).getByText('2')).toBeInTheDocument()
      expect(bravo.textContent).toBe('bravo')
    })
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

// agent-os-eldv: pinning MOVES a stack to the top of the one list. It used to
// render the stack in a "Pinned" block AND again in the main list.
describe('Sidebar — pinned stacks move to the top', () => {
  const five = (statuses: Record<string, string> = {}) =>
    ['alpha', 'bravo', 'charlie', 'delta', 'echo'].map((n, i) => ({
      ...row(`s${i + 1}`, n, '/stacks'),
      status: statuses[n] ?? 'running',
    })) as never

  it('pinning a stack leaves exactly one row for it, and no Pinned section', async () => {
    const { container } = renderSidebar()
    const aside = desktopAside(container)
    await waitFor(() => expect(stackLinks(aside)).toHaveLength(2))

    await act(async () => {
      fireEvent.click(within(aside).getByLabelText('Pin alpha'))
    })

    expect(useUIStore.getState().pinnedStacks).toContain('s1')
    expect(within(aside).getAllByRole('link', { name: /^alpha - / })).toHaveLength(1)
    expect(stackLinks(aside)).toHaveLength(2)
    expect(within(aside).queryByText('Pinned')).not.toBeInTheDocument()
  })

  it('sorts pinned stacks above every unpinned one, name order inside each group', async () => {
    useUIStore.setState({ pinnedStacks: ['s4', 's2'] })
    listMock.mockResolvedValueOnce(five())
    const { container } = renderSidebar()
    const aside = desktopAside(container)
    await waitFor(() => expect(stackLinks(aside).length).toBeGreaterThan(0))

    expect(names(stackLinks(aside))).toEqual(['bravo', 'delta', 'alpha', 'charlie', 'echo'])
    // The filled pin icon is the only marker: one pin control per row, no section glyph.
    expect(aside.querySelectorAll('.lucide-pin')).toHaveLength(5)
    expect(aside.querySelector('.lucide-star')).toBeNull()
    expect(within(aside).queryByText('Pinned')).not.toBeInTheDocument()
  })

  it('keeps the status sort inside each group when sorting by status', async () => {
    localStorage.setItem('sidebar-sort', 'status')
    useUIStore.setState({ pinnedStacks: ['s4', 's2'] })
    listMock.mockResolvedValueOnce(five({ alpha: 'stopped', bravo: 'stopped' }))
    const { container } = renderSidebar()
    const aside = desktopAside(container)
    await waitFor(() => expect(stackLinks(aside).length).toBeGreaterThan(0))

    // pinned: delta (running) before bravo (stopped); unpinned: charlie, echo (running) before alpha (stopped)
    expect(names(stackLinks(aside))).toEqual(['delta', 'bravo', 'charlie', 'echo', 'alpha'])
  })

  it('hides a pinned stack that fails the search filter instead of floating it to the top', async () => {
    localStorage.setItem('sidebar-search', 'alp')
    useUIStore.setState({ pinnedStacks: ['s4'] })
    listMock.mockResolvedValueOnce(five())
    const { container } = renderSidebar()
    const aside = desktopAside(container)
    await waitFor(() => expect(stackLinks(aside).length).toBeGreaterThan(0))

    expect(names(stackLinks(aside))).toEqual(['alpha'])
  })

  it('hides a pinned stack that fails the status filter', async () => {
    localStorage.setItem('sidebar-filter', 'stopped')
    useUIStore.setState({ pinnedStacks: ['s4'] })
    listMock.mockResolvedValueOnce(five({ alpha: 'stopped' }))
    const { container } = renderSidebar()
    const aside = desktopAside(container)
    await waitFor(() => expect(stackLinks(aside).length).toBeGreaterThan(0))

    expect(names(stackLinks(aside))).toEqual(['alpha'])
  })
})

// agent-os-kdqm: the stacks query used to default `data` to [] and drop the
// error, so a failed list request read as "No stacks found" and "Stacks (0)".
describe('Sidebar — failed stack list', () => {
  it('first load fails: an error with Retry, NOT "No stacks found" or a count of 0', async () => {
    listMock.mockRejectedValueOnce(new Error('boom'))
    renderSidebar()

    await waitFor(() =>
      expect(screen.getAllByText('Could not load the stack list.').length).toBeGreaterThan(0),
    )
    expect(screen.queryByText('No stacks found')).not.toBeInTheDocument()
    expect(screen.queryByText('(0)')).not.toBeInTheDocument()

    listMock.mockResolvedValueOnce([
      { id: 's1', projectName: 'alpha', status: 'running', containers: [], directory: '/stacks', isGitRepo: false, gitDirty: false },
    ] as never)
    await act(async () => {
      fireEvent.click(screen.getAllByRole('button', { name: 'Retry' })[0])
    })
    await waitFor(() => expect(screen.getAllByText('alpha').length).toBeGreaterThan(0))
    expect(screen.queryByText('Could not load the stack list.')).not.toBeInTheDocument()
  })

  // Waits on the QUERY state, not on the notice, so this case fails on the
  // header count alone when the count is the only thing still wrong.
  it('first load fails: the header shows no stack count', async () => {
    listMock.mockRejectedValueOnce(new Error('boom'))
    const { queryClient } = renderSidebar()

    await waitFor(() =>
      expect(queryClient.getQueryState(queryKeys.stacks())?.status).toBe('error'),
    )
    expect(screen.queryByText('(0)')).not.toBeInTheDocument()
  })

  it('a refetch fails over a loaded list: keeps the stacks AND says so', async () => {
    const { queryClient } = renderSidebar()
    await waitFor(() => expect(screen.getAllByText('alpha').length).toBeGreaterThan(0))

    listMock.mockRejectedValueOnce(new Error('boom'))
    await act(async () => {
      await queryClient.refetchQueries({ queryKey: queryKeys.stacks() })
    })

    await waitFor(() =>
      expect(
        screen.getAllByText(
          'Could not refresh the stack list. The values shown are the last ones the server sent.',
        ).length,
      ).toBeGreaterThan(0),
    )
    expect(screen.getAllByText('alpha').length).toBeGreaterThan(0)
    expect(screen.getAllByText('bravo').length).toBeGreaterThan(0)
    expect(screen.queryByText('Could not load the stack list.')).not.toBeInTheDocument()
  })

  it('a genuinely empty list still shows "No stacks found", with no error', async () => {
    listMock.mockResolvedValueOnce([] as never)
    renderSidebar()

    await waitFor(() => expect(screen.getAllByText('No stacks found').length).toBeGreaterThan(0))
    expect(screen.getAllByText('(0)').length).toBeGreaterThan(0)
    expect(screen.queryByText(/Could not (load|refresh) the stack list/)).not.toBeInTheDocument()
  })
})
