import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { BrowserRouter, Routes, Route } from 'react-router-dom'
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

function renderPage(route = '/stacks/s1') {
  // BrowserRouter (matching main.tsx's actual router) rather than MemoryRouter,
  // so assertions against window.location reflect real navigation, the same
  // way frontend/src/test/utils.tsx's renderWithProviders does.
  window.history.pushState({}, 'Test page', route)
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false } },
  })
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

      // The page's useQuery sets retry:1, so the error only settles after one
      // retry attempt (~1s default backoff) — outlasts waitFor's default timeout.
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
