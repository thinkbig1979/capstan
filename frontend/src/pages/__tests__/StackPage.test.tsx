import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
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
    envFile: '',
    gitBranch: '',
    gitCommit: '',
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
            {
              health: '', id: 'c1', name: 'web', image: 'nginx:1', state: 'running', status: 'Up 2 hours', ports: [] },
            {
              health: '', id: 'c2', name: 'db', image: 'pg:16', state: 'running', status: 'Up 6 days', ports: [] },
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
            {
              health: '', id: 'c1', name: 'web', image: 'nginx:1', state: 'running', status: 'Up 1 hour', ports: [] },
            {
              health: '', id: 'c2', name: 'db', image: 'pg:16', state: 'exited', status: 'Exited (0)', ports: [] },
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

    /**
     * agent-os-lurn. `if (error || !stack)` was true for a failed REFETCH as
     * well as a failed first load, and TanStack retains `stack` in both cases,
     * so one focus refetch that 500s rendered "Stack Not Found" over a stack
     * already on screen (staleTime 30s, refetchOnWindowFocus on, 500 is not
     * auto-retryable). RESOLVES first, then rejects a REFETCH; a
     * first-fetch-rejects fixture is blind to this class.
     *
     * The two preservation controls are the arms either side of this one:
     * "shows a failure card with Retry when the fetch rejects" (error, no data)
     * and "shows a plain not-found message ... no :id param" (no error, no
     * data). Neither can fail first; both are pinned by mutation evidence.
     */
    it('keeps the stack on screen when a REFETCH fails (agent-os-lurn)', async () => {
      const { queryClient } = renderPage('/stacks/s1')

      await waitFor(() => expect(screen.getByTestId('stack-detail')).toBeInTheDocument())

      getStack.mockRejectedValue(new Error('boom'))
      await queryClient.refetchQueries()
      await waitFor(() =>
        expect(queryClient.getQueryCache().getAll().some((q) => q.state.status === 'error')).toBe(true),
      )

      // The stack the server already sent is still on screen...
      expect(screen.getByTestId('stack-detail')).toBeInTheDocument()
      // ...and "Stack Not Found" has NOT replaced it.
      expect(screen.queryByText('Stack Not Found')).not.toBeInTheDocument()
      // ...and the refresh failure is reported rather than swallowed.
      expect(
        screen.getByText(/Could not refresh this stack\. The values shown are the last ones the server sent\./),
      ).toBeInTheDocument()
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
  describe('delete failures (agent-os-5obt)', () => {
    // Copied verbatim from handlers/respond.go:186 DockerUnavailableMessage,
    // which renderDockerResult puts in the 503 ActionResult when the socket is
    // missing — the worst case the fixed string used to swallow, because this
    // text IS the recovery instruction. Kept verbatim rather than paraphrased
    // so it stays honest about what the operator actually receives; at 170
    // characters it is also why the reason is the description and not the
    // title.
    const DOCKER_REASON =
      'Docker daemon unreachable: the server started without a usable Docker connection. ' +
      'Check that the Docker socket is mounted and the daemon is running, then restart Capstan.'

    // Walks the whole real delete path rather than poking the mutation: the
    // portalled Radix menu, then the typed confirmation the destructive dialog
    // demands. deleteStackWithCollateralConfirm is NOT mocked, so what reaches
    // onError is whatever that wrapper re-throws.
    async function requestDelete(user: ReturnType<typeof userEvent.setup>) {
      await user.click(await screen.findByRole('button', { name: 'More stack actions' }))
      await user.click(await screen.findByText('Delete Stack'))
      await user.type(await screen.findByLabelText('Type my-stack to confirm'), 'my-stack')
      await user.click(screen.getByRole('button', { name: 'Delete' }))
    }

    it('renders the ActionResult reason as the toast description', async () => {
      // A FAILED delete answers 5xx with a truth.ActionResult body, and api.ts's
      // interceptor rejects with {...body, status} — so this object, verbatim,
      // is what onError sees (stack-delete.ts re-throws it untouched: its
      // collateral branch keys on `code`, which an ActionResult has not got).
      deleteStack.mockRejectedValue({ outcome: 'failed', reason: DOCKER_REASON, status: 503 })
      renderPage()
      await screen.findByTestId('stack-detail')

      await requestDelete(userEvent.setup())

      const { toast } = await import('sonner')
      await waitFor(() => {
        expect(toast.error).toHaveBeenCalledWith('Failed to delete stack', {
          description: DOCKER_REASON,
        })
      })
      // ONE toast, not two. This is the arm that gates the double-toast: delete
      // the `else` and `if (c) { X } else { Y }` becomes `if (c) { X } { Y }`, a
      // bare block that always runs, so this path fires the description toast
      // AND the bare one. toHaveBeenCalledWith alone still passes that mutant —
      // the first call matches — so the count is what sees it.
      expect(toast.error).toHaveBeenCalledTimes(1)
    })

    it('leaves the generic sentence alone when the failure carries no reason', async () => {
      // agent-os-5g8a: the generic sentence is still the TITLE and is never
      // replaced. `new Error('boom')` DOES carry a message, so causeOf finds
      // one and it lands in the description -- the operator now sees both.
      // The single-argument shape is pinned instead by the presenter's own
      // unit tests (src/lib/__tests__/error-presenter.test.ts), which drive the
      // genuinely cause-less case that no component fixture produces.
      deleteStack.mockRejectedValue(new Error('boom'))
      renderPage()
      await screen.findByTestId('stack-detail')

      await requestDelete(userEvent.setup())

      const { toast } = await import('sonner')
      await waitFor(() =>
        expect(toast.error).toHaveBeenCalledWith('Failed to delete stack', { description: 'boom' }),
      )
      expect(toast.error).toHaveBeenCalledTimes(1)
    })

    it('falls back to the bare sentence when the ActionResult reason is empty', async () => {
      // Pins the `&& err.reason` conjunct, NOT the isActionResult type check.
      // This body IS an ActionResult, so the type guard alone passes it through
      // and the description would render empty — a toast with a blank second
      // line, worse than the generic sentence on its own. Dropping that one
      // token is a mutation the other three arms do not notice.
      deleteStack.mockRejectedValue({ outcome: 'failed', reason: '', status: 503 })
      renderPage()
      await screen.findByTestId('stack-detail')

      await requestDelete(userEvent.setup())

      const { toast } = await import('sonner')
      // The discriminator survives agent-os-5g8a: the description must be the
      // CLASSIFIER's sentence, never the ActionResult's empty reason. Asserting
      // the exact string is what keeps this arm able to see the `&& err.reason`
      // token being dropped -- an empty description would not match.
      await waitFor(() =>
        expect(toast.error).toHaveBeenCalledWith('Failed to delete stack', {
          description: '503: Something went wrong on the server',
        }),
      )
      // Not this arm's own mutant — an empty reason fails the guard, so the
      // `else`-deletion mutant still fires exactly one toast here. Kept so all
      // three positive arms state the same thing and a reader is not left
      // wondering which one is deliberately weaker.
      expect(toast.error).toHaveBeenCalledTimes(1)
    })

    it('shows no toast at all when the collateral confirmation is declined', async () => {
      // A declined second confirmation is a user cancel, not a failure. This is
      // the arm most likely to break silently: it stays green only while the
      // StackDeleteCancelledError early-return sits OUTSIDE the reason branch.
      deleteStack.mockRejectedValue({
        code: 'STACK_DELETE_COLLATERAL',
        message: 'stack directory holds more than the stack itself',
        details: { directory: '/stacks/s1', collateral: ['data/'] },
        status: 428,
      })
      const user = userEvent.setup()
      renderPage()
      await screen.findByTestId('stack-detail')

      await requestDelete(user)

      await screen.findByText('Also delete these files?')
      await user.click(screen.getByRole('button', { name: 'Cancel' }))

      // One call only: declining must never re-issue the delete with the
      // confirm-collateral flag.
      await waitFor(() => expect(deleteStack).toHaveBeenCalledTimes(1))
      await act(async () => { await Promise.resolve() })

      const { toast } = await import('sonner')
      expect(toast.error).not.toHaveBeenCalled()
      expect(toast.success).not.toHaveBeenCalled()
    })
  })
})
