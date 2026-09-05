import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { BrowserRouter, Routes, Route } from 'react-router'
import type { Stack } from '@/types'
import { useStackStore } from '@/stores/stackStore'
import { useUpdateJobStore } from '@/stores/updateJobStore'
import { useUpdateScanStore } from '@/stores/updateScanStore'

// Page-level test: pins StackPage's own contract (route param -> active tab,
// loading/error/not-found branches, delete wiring) without exercising
// StackDetail's internals, which have their own dedicated coverage.
const getStack = vi.fn()
const deleteStack = vi.fn()
const checkUpdates = vi.fn()
const getUpdateJobs = vi.fn()
const updateStack = vi.fn()

vi.mock('@/lib/api', () => ({
  stacksApi: {
    get: (...args: unknown[]) => getStack(...args),
    delete: (...args: unknown[]) => deleteStack(...args),
  },
  resourcesApi: {
    checkUpdates: (...args: unknown[]) => checkUpdates(...args),
    getUpdateJobs: (...args: unknown[]) => getUpdateJobs(...args),
    updateStack: (...args: unknown[]) => updateStack(...args),
  },
}))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), warning: vi.fn(), info: vi.fn() },
}))

// The lifecycle action bar moved from StackDetail to the page header in the
// phase-3 redesign, and its streaming operation now lives here too.
const execute = vi.fn()
const reset = vi.fn()
// Mutated (via Object.assign) per test to drive the running/success branches.
const operationState = { status: 'idle', action: '', error: null as string | null, lines: [] as string[] }

vi.mock('@/hooks/useStreamingOperation', () => ({
  useStreamingOperation: () => ({ ...operationState, execute, cancel: vi.fn(), reset }),
}))

vi.mock('@/components/stack/OperationProgress', () => ({
  OperationProgress: ({ status, action }: { status: string; action: string }) => (
    <div data-testid="operation-progress" data-status={status} data-action={action} />
  ),
}))

vi.mock('@/components/stack/StackDetail', () => ({
  StackDetail: ({ activeTab, onTabChange }: { activeTab: string; onTabChange: (tab: string) => void }) => (
    <div data-testid="stack-detail">
      <span data-testid="active-tab">{activeTab}</span>
      <button onClick={() => onTabChange('logs')}>Go to logs</button>
    </div>
  ),
}))
vi.mock('@/components/stack/StackUpdateBadge', () => ({
  StackUpdateBadge: () => <div data-testid="stack-update-badge" />,
}))
// The git header chip has its own direct test (GitStatus.test.tsx).
vi.mock('@/components/git/GitStatus', () => ({
  GitStatus: () => <div data-testid="git-status-chip" />,
}))
vi.mock('@/components/updates/UpdateJobLog', () => ({
  UpdateJobLog: () => <div data-testid="update-job-log" />,
}))

import { StackPage } from '../StackPage'

function makeStack(overrides: Partial<Stack> = {}): Stack {
  return {
    id: 's1',
    directory: '/stacks/s1',
    composeFile: 'docker-compose.yml',
    projectName: 'my-stack',
    status: 'running',
    isGitRepo: false,
    gitDirty: false,
    gitAhead: 0,
    gitBehind: 0,
    containers: [],
    ...overrides,
  }
}

function renderPage(
  route = '/stacks/s1',
  // Injectable so a test can spy on the client before mount-time effects run.
  queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false } },
  }),
) {
  // BrowserRouter (matching main.tsx's actual router) rather than MemoryRouter,
  // so assertions against window.location reflect real navigation, the same
  // way frontend/src/test/utils.tsx's renderWithProviders does.
  window.history.pushState({}, 'Test page', route)
  return {
    ...render(
      <BrowserRouter>
        <QueryClientProvider client={queryClient}>
          <Routes>
            <Route path="/stacks/:id" element={<StackPage />} />
            <Route path="/stacks/:id/:tab" element={<StackPage />} />
            {/* No :id param at all — exercises the disabled-query "not found" branch. */}
            <Route path="/no-id" element={<StackPage />} />
          </Routes>
        </QueryClientProvider>
      </BrowserRouter>,
    ),
    queryClient,
  }
}

describe('StackPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    Object.assign(operationState, { status: 'idle', action: '', error: null, lines: [] })
    getStack.mockResolvedValue(makeStack())
    checkUpdates.mockResolvedValue({ updates: [] })
    getUpdateJobs.mockResolvedValue({ jobs: [] })
    useStackStore.getState().reset()
    useUpdateJobStore.setState({ jobs: {} })
    useUpdateScanStore.getState().resetScan()
  })

  describe('tab routing', () => {
    it('derives the active tab from the URL and defaults to overview', async () => {
      renderPage('/stacks/s1')
      await waitFor(() => expect(screen.getByTestId('stack-detail')).toBeInTheDocument())
      expect(screen.getByTestId('active-tab')).toHaveTextContent('overview')
    })

    it('passes the tab segment from the URL through to StackDetail', async () => {
      renderPage('/stacks/s1/containers')
      await waitFor(() => expect(screen.getByTestId('stack-detail')).toBeInTheDocument())
      expect(screen.getByTestId('active-tab')).toHaveTextContent('containers')
    })

    it('navigates to the new tab path when StackDetail requests a tab change', async () => {
      renderPage('/stacks/s1')
      await waitFor(() => expect(screen.getByTestId('stack-detail')).toBeInTheDocument())

      fireEvent.click(screen.getByText('Go to logs'))

      await waitFor(() => expect(screen.getByTestId('active-tab')).toHaveTextContent('logs'))
      expect(window.location.pathname).toBe('/stacks/s1/logs')
    })

    // The 11-tab layout merged into 6 tabs; old deep links must land on the
    // merged tab (with the Activity section in ?view=).
    it.each([
      ['/stacks/s1/compose', '/stacks/s1/editor', ''],
      ['/stacks/s1/environment', '/stacks/s1/editor', ''],
      ['/stacks/s1/split', '/stacks/s1/editor', ''],
      ['/stacks/s1/history', '/stacks/s1/activity', '?view=history'],
      ['/stacks/s1/updates', '/stacks/s1/activity', '?view=updates'],
      ['/stacks/s1/backups', '/stacks/s1/activity', '?view=backups'],
    ])('redirects the old %s deep link', async (from, toPath, toSearch) => {
      renderPage(from)
      await waitFor(() => expect(window.location.pathname).toBe(toPath))
      expect(window.location.search).toBe(toSearch)
    })
  })

  describe('header status pill and uptime', () => {
    it('shows running-count and uptime derived from container data', async () => {
      getStack.mockResolvedValue(
        makeStack({
          status: 'running',
          containers: [
            { id: 'c1', name: 'web', image: 'nginx:1', state: 'running', status: 'Up 2 hours', ports: [] },
            { id: 'c2', name: 'db', image: 'pg:16', state: 'running', status: 'Up 6 days', ports: [] },
          ],
        }),
      )

      renderPage('/stacks/s1')

      await waitFor(() => expect(screen.getByText('Running · 2/2')).toBeInTheDocument())
      // The chip reports the youngest running container, never more than the
      // stack as a whole has been up.
      expect(screen.getByText('up 2 hours')).toBeInTheDocument()
    })

    it('reports a partial stack as such', async () => {
      getStack.mockResolvedValue(
        makeStack({
          status: 'partial',
          containers: [
            { id: 'c1', name: 'web', image: 'nginx:1', state: 'running', status: 'Up 1 hour', ports: [] },
            { id: 'c2', name: 'db', image: 'pg:16', state: 'exited', status: 'Exited (0)', ports: [] },
          ],
        }),
      )

      renderPage('/stacks/s1')

      await waitFor(() => expect(screen.getByText('Partial · 1/2')).toBeInTheDocument())
    })

    it('shows a plain status pill with no counts for a stopped stack', async () => {
      getStack.mockResolvedValue(makeStack({ status: 'stopped', containers: [] }))

      renderPage('/stacks/s1')

      await waitFor(() => expect(screen.getByText('Stopped')).toBeInTheDocument())
      expect(screen.queryByText(/up /)).not.toBeInTheDocument()
    })
  })

  describe('header action bar (moved from StackDetail in phase 3)', () => {
    it('runs the matching operation for each button', async () => {
      getStack.mockResolvedValue(makeStack({ status: 'running' }))
      renderPage('/stacks/s1')
      await waitFor(() => expect(screen.getByTestId('stack-detail')).toBeInTheDocument())

      fireEvent.click(screen.getByRole('button', { name: /Restart/ }))
      expect(execute).toHaveBeenCalledWith('s1', 'restart')

      fireEvent.click(screen.getByRole('button', { name: /Pull Images/ }))
      expect(execute).toHaveBeenCalledWith('s1', 'pull')

      fireEvent.click(screen.getByRole('button', { name: /Stop/ }))
      expect(execute).toHaveBeenCalledWith('s1', 'stop')
    })

    it('offers Start but not Stop on a stopped stack', async () => {
      getStack.mockResolvedValue(makeStack({ status: 'stopped' }))
      renderPage('/stacks/s1')

      await waitFor(() => expect(screen.getByRole('button', { name: /Start/ })).toBeEnabled())
      expect(screen.getByRole('button', { name: /Stop/ })).toBeDisabled()
      expect(screen.getByRole('button', { name: /Restart/ })).toBeDisabled()

      fireEvent.click(screen.getByRole('button', { name: /Start/ }))
      expect(execute).toHaveBeenCalledWith('s1', 'start')
    })

    it('offers Stop but not Start on a running stack', async () => {
      getStack.mockResolvedValue(makeStack({ status: 'running' }))
      renderPage('/stacks/s1')

      await waitFor(() => expect(screen.getByRole('button', { name: /Stop/ })).toBeEnabled())
      expect(screen.getByRole('button', { name: /Start/ })).toBeDisabled()
    })

    it('lets a partially-running stack be started', async () => {
      getStack.mockResolvedValue(makeStack({ status: 'partial' }))
      renderPage('/stacks/s1')

      // "partial" means some services are down, so Start is the useful action.
      await waitFor(() => expect(screen.getByRole('button', { name: /Start/ })).toBeEnabled())
      expect(screen.getByRole('button', { name: /Stop/ })).toBeDisabled()
    })

    it('disables every action while one is already running', async () => {
      Object.assign(operationState, { status: 'running', action: 'start' })
      getStack.mockResolvedValue(makeStack({ status: 'stopped' }))
      renderPage('/stacks/s1')

      await waitFor(() => expect(screen.getByRole('button', { name: /Starting\.\.\./ })).toBeDisabled())
      expect(screen.getByRole('button', { name: /Stop/ })).toBeDisabled()
      expect(screen.getByRole('button', { name: /Restart/ })).toBeDisabled()
      // Pull has no status precondition of its own, but must not race a start.
      expect(screen.getByRole('button', { name: /Pull Images/ })).toBeDisabled()
    })

    it.each([
      ['start', 'Starting...'],
      ['stop', 'Stopping...'],
      ['restart', 'Restarting...'],
      ['pull', 'Pulling...'],
    ])('labels the in-flight %s button "%s"', async (action, label) => {
      Object.assign(operationState, { status: 'running', action })
      renderPage('/stacks/s1')

      await waitFor(() => expect(screen.getByText(label)).toBeInTheDocument())
    })

    it('passes the operation through to the progress panel', async () => {
      Object.assign(operationState, { status: 'running', action: 'pull', lines: ['pulling…'] })
      renderPage('/stacks/s1')

      await waitFor(() => {
        const progress = screen.getByTestId('operation-progress')
        expect(progress).toHaveAttribute('data-status', 'running')
        expect(progress).toHaveAttribute('data-action', 'pull')
      })
    })

    it('refreshes the stack and confirms the action by name after success', async () => {
      Object.assign(operationState, { status: 'success', action: 'restart' })

      // The spy has to be in place before the mount, because the effect that
      // invalidates runs on it.
      const queryClient = new QueryClient({
        defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false } },
      })
      const invalidate = vi.spyOn(queryClient, 'invalidateQueries')

      renderPage('/stacks/s1', queryClient)

      const { toast } = await import('sonner')
      await waitFor(() => {
        expect(toast.success).toHaveBeenCalledWith('Restart completed')
      })
      // Stale data after a restart is the visible bug this guards: the status
      // pill would still read "stopped".
      expect(invalidate).toHaveBeenCalledWith({ queryKey: ['stack', 's1'] })
      expect(invalidate).toHaveBeenCalledWith({ queryKey: ['stacks'] })
    })
  })

  describe('loading state', () => {
    it('shows skeletons while the stack is loading', async () => {
      getStack.mockReturnValue(new Promise(() => {}))
      const { container } = renderPage('/stacks/s1')

      // Flush the (already-resolved) update-related queries' microtasks inside
      // act, so their unrelated state updates don't leak past this test as an
      // unwrapped act() warning. The stack query itself never resolves, so the
      // page stays in its loading branch throughout.
      await act(async () => {})

      expect(screen.queryByTestId('stack-detail')).not.toBeInTheDocument()
      expect(container.querySelectorAll('.animate-pulse').length).toBeGreaterThan(0)
    })
  })

  describe('error path', () => {
    it('shows a failure card with Retry when the fetch rejects', async () => {
      getStack.mockRejectedValue(new Error('stack lookup exploded'))

      renderPage('/stacks/s1')

      // The wrapper's `retry: false` now actually applies: the page no longer
      // restates `retry: 1` per-query, which used to REPLACE that default rather
      // than compose with it (agent-os-tdts). The error settles on the first
      // failure; the timeout is headroom for a loaded box, not a retry backoff.
      await waitFor(() => expect(screen.getByText('Failed to load stack')).toBeInTheDocument(), { timeout: 3000 })
      // The message is rendered twice: once as the page subtitle, once in the card body.
      expect(screen.getAllByText('stack lookup exploded').length).toBeGreaterThan(0)
      expect(screen.queryByTestId('stack-detail')).not.toBeInTheDocument()

      getStack.mockClear()
      fireEvent.click(screen.getByRole('button', { name: /Retry/ }))
      await waitFor(() => expect(getStack).toHaveBeenCalledTimes(1))
    })

    it('shows a plain not-found message (no error card) when there is no :id param', async () => {
      // The query is `enabled: !!id` (StackPage.tsx:49), so with no id it never
      // runs: isLoading is false, error is null, and stack is undefined — the
      // "not found, but not a fetch failure" branch (StackPage.tsx:175,229-234).
      // (A queryFn resolving `undefined` isn't a valid way to hit this branch:
      // TanStack Query itself throws "Query data cannot be undefined".)
      renderPage('/no-id')

      await waitFor(() => expect(screen.getByText('Stack Not Found')).toBeInTheDocument())
      expect(screen.getByText('The requested stack could not be found.')).toBeInTheDocument()
      expect(screen.queryByText('Failed to load stack')).not.toBeInTheDocument()
      expect(screen.getByRole('button', { name: /Back to Dashboard/ })).toBeInTheDocument()
      expect(getStack).not.toHaveBeenCalled()
    })
  })
})
