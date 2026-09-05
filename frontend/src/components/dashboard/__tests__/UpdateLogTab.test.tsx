import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { UpdateLogTab } from '../UpdateLogTab'
import type { UpdateHistoryEntry } from '@/types'

/**
 * UpdatesTab.test.tsx:67 replaces this tab with an empty div (0/60 statements,
 * agent-os-m1mu). Rendered for real here with only the API layer mocked.
 */

const mockGetUpdateHistory = vi.fn()

vi.mock('@/lib/api', () => ({
  resourcesApi: {
    getUpdateHistory: (...a: unknown[]) => mockGetUpdateHistory(...a),
  },
}))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), info: vi.fn(), warning: vi.fn() },
}))

beforeEach(() => {
  Element.prototype.hasPointerCapture = () => false
  Element.prototype.setPointerCapture = () => {}
  Element.prototype.releasePointerCapture = () => {}
})

const entry = (over: Partial<UpdateHistoryEntry> = {}): UpdateHistoryEntry => ({
  id: 'u1',
  containerId: 'c1',
  containerName: 'web-1',
  stackId: 'stack-1',
  stackName: 'web',
  image: 'nginx:latest',
  oldDigest: 'sha256:0123456789abcdef',
  newDigest: 'sha256:fedcba9876543210',
  status: 'success',
  trigger: 'manual',
  startedAt: '2026-08-08T10:00:00Z',
  completedAt: '2026-08-08T10:00:05Z',
  durationMs: 5000,
  ...over,
})

const historyPage = (over: Record<string, unknown> = {}) => ({
  entries: [entry()],
  total: 1,
  totalPages: 1,
  ...over,
})

function createWrapper() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: 0 }, mutations: { retry: false } },
  })
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  )
}

const renderTab = () => render(<UpdateLogTab />, { wrapper: createWrapper() })

// The three filter triggers carry no id and no aria-label, so they have no
// accessible name to query by — they are addressed by position instead.
const statusSelect = () => screen.getAllByRole('combobox')[0]
const triggerSelect = () => screen.getAllByRole('combobox')[1]
const dateSelect = () => screen.getAllByRole('combobox')[2]

beforeEach(() => {
  vi.clearAllMocks()
  mockGetUpdateHistory.mockResolvedValue(historyPage())
})

describe('UpdateLogTab — loading, error and empty', () => {
  it('shows skeletons on the first load', () => {
    mockGetUpdateHistory.mockReturnValue(new Promise(() => {}))
    renderTab()

    expect(screen.queryByRole('table')).not.toBeInTheDocument()
  })

  it('offers a retry when the history fails to load', async () => {
    mockGetUpdateHistory.mockRejectedValue(new Error('boom'))
    renderTab()

    // The wrapper's `retry: false` now actually applies: useUpdateHistory no
    // longer restates `retry: 1` per-query, which used to REPLACE that default
    // rather than compose with it (agent-os-tdts). So the error settles on the
    // first failure; the timeout below is headroom for a loaded box, not for a
    // retry backoff.
    expect(
      await screen.findByText('Failed to Load Update History', {}, { timeout: 5000 }),
    ).toBeInTheDocument()

    mockGetUpdateHistory.mockResolvedValue(historyPage())
    fireEvent.click(screen.getByRole('button', { name: /Retry/ }))

    expect(await screen.findByText('web-1')).toBeInTheDocument()
  })

  it('explains the empty history', async () => {
    mockGetUpdateHistory.mockResolvedValue(historyPage({ entries: [], total: 0 }))
    renderTab()

    expect(await screen.findByText('No Update History')).toBeInTheDocument()
    expect(
      screen.getByText('Updates will appear here after containers are updated.'),
    ).toBeInTheDocument()
  })
})

describe('UpdateLogTab — the table', () => {
  it('shows the container, the stack link and the image', async () => {
    renderTab()

    expect(await screen.findByText('web-1')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'web' })).toHaveAttribute('href', '/stacks/stack-1')
    expect(screen.getByText('nginx:latest')).toBeInTheDocument()
  })

  it('marks an entry with no stack as standalone', async () => {
    mockGetUpdateHistory.mockResolvedValue(
      historyPage({ entries: [entry({ stackName: undefined, stackId: undefined })] }),
    )
    renderTab()

    expect(await screen.findByText('standalone')).toBeInTheDocument()
  })

  it('truncates a long image reference', async () => {
    mockGetUpdateHistory.mockResolvedValue(
      historyPage({
        entries: [entry({ image: 'registry.example.com/team/very-long-image:1.2.3' })],
      }),
    )
    renderTab()

    expect(await screen.findByText('registry.example.com/team...')).toBeInTheDocument()
  })

  it('shows the digest change with sha256: stripped and truncated to 12', async () => {
    renderTab()

    expect(await screen.findByText('0123456789ab → fedcba987654')).toBeInTheDocument()
  })

  it('shows a dash for a missing digest', async () => {
    mockGetUpdateHistory.mockResolvedValue(
      historyPage({ entries: [entry({ oldDigest: undefined, newDigest: undefined })] }),
    )
    renderTab()

    expect(await screen.findByText('- → -')).toBeInTheDocument()
  })

  it('shows a dash when the duration was not recorded', async () => {
    mockGetUpdateHistory.mockResolvedValue(
      historyPage({ entries: [entry({ durationMs: undefined })] }),
    )
    renderTab()

    await screen.findByText('web-1')
    expect(screen.getByText('-')).toBeInTheDocument()
  })

  it('renders the status and the trigger', async () => {
    mockGetUpdateHistory.mockResolvedValue(
      historyPage({ entries: [entry({ status: 'failed', trigger: 'auto' })] }),
    )
    renderTab()

    expect(await screen.findByText('failed')).toBeInTheDocument()
    expect(screen.getByText('auto')).toBeInTheDocument()
  })

  it('uses plural wording for the record count', async () => {
    mockGetUpdateHistory.mockResolvedValue(
      historyPage({ entries: [entry({ id: 'a' }), entry({ id: 'b' })], total: 2 }),
    )
    renderTab()

    expect(await screen.findByText('2 records')).toBeInTheDocument()
  })

  it('uses singular wording for exactly one record', async () => {
    renderTab()

    expect(await screen.findByText('1 record')).toBeInTheDocument()
  })
})

describe('UpdateLogTab — filters', () => {
  it('filters client-side on the free-text box', async () => {
    mockGetUpdateHistory.mockResolvedValue(
      historyPage({
        entries: [
          entry({ id: 'a', containerName: 'web-1' }),
          entry({ id: 'b', containerName: 'api-1' }),
        ],
        total: 2,
      }),
    )
    renderTab()

    fireEvent.change(await screen.findByPlaceholderText('Filter events…'), {
      target: { value: 'api' },
    })

    expect(screen.getByText('api-1')).toBeInTheDocument()
    expect(screen.queryByText('web-1')).not.toBeInTheDocument()
    expect(screen.getByText('1 of 2 records')).toBeInTheDocument()
  })

  it('says so when the free-text filter matches nothing', async () => {
    renderTab()

    fireEvent.change(await screen.findByPlaceholderText('Filter events…'), {
      target: { value: 'zzz' },
    })

    expect(screen.getByText('No events match "zzz".')).toBeInTheDocument()
  })

  it('sends the status filter to the server', async () => {
    const user = userEvent.setup()
    renderTab()

    await screen.findByText('web-1')
    await user.click(statusSelect())
    await user.click(await screen.findByRole('option', { name: 'Failed' }))

    await waitFor(() =>
      expect(mockGetUpdateHistory).toHaveBeenLastCalledWith(
        expect.objectContaining({ status: 'failed', page: 1 }),
      ),
    )
  })

  it('sends the trigger filter to the server', async () => {
    const user = userEvent.setup()
    renderTab()

    await screen.findByText('web-1')
    await user.click(triggerSelect())
    await user.click(await screen.findByRole('option', { name: 'Auto' }))

    await waitFor(() =>
      expect(mockGetUpdateHistory).toHaveBeenLastCalledWith(
        expect.objectContaining({ trigger: 'auto' }),
      ),
    )
  })

  it('turns a date range into an ISO "from" bound', async () => {
    const user = userEvent.setup()
    renderTab()

    await screen.findByText('web-1')
    await user.click(dateSelect())
    await user.click(await screen.findByRole('option', { name: 'Last 7 days' }))

    await waitFor(() =>
      expect(mockGetUpdateHistory).toHaveBeenLastCalledWith(
        expect.objectContaining({ from: expect.stringMatching(/^\d{4}-\d{2}-\d{2}T/) }),
      ),
    )
  })

  it('sends no status/trigger/from keys while everything is "all"', async () => {
    renderTab()

    await screen.findByText('web-1')
    expect(mockGetUpdateHistory).toHaveBeenCalledWith({ page: 1, limit: 25 })
  })
})

describe('UpdateLogTab — pagination', () => {
  it('hides the pager for a single page', async () => {
    renderTab()

    await screen.findByText('web-1')
    expect(screen.queryByRole('button', { name: /Next/ })).not.toBeInTheDocument()
  })

  it('pages forward and back', async () => {
    mockGetUpdateHistory.mockResolvedValue(historyPage({ total: 60, totalPages: 3 }))
    renderTab()

    expect(await screen.findByText('Page 1 of 3')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Previous/ })).toBeDisabled()

    fireEvent.click(screen.getByRole('button', { name: /Next/ }))

    await waitFor(() =>
      expect(mockGetUpdateHistory).toHaveBeenLastCalledWith(expect.objectContaining({ page: 2 })),
    )
    expect(await screen.findByText('Page 2 of 3')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /Previous/ }))
    await waitFor(() =>
      expect(mockGetUpdateHistory).toHaveBeenLastCalledWith(expect.objectContaining({ page: 1 })),
    )
  })

  it('resets to page 1 when a filter changes', async () => {
    mockGetUpdateHistory.mockResolvedValue(historyPage({ total: 60, totalPages: 3 }))
    const user = userEvent.setup()
    renderTab()

    fireEvent.click(await screen.findByRole('button', { name: /Next/ }))
    // Page 2 is a fresh query key, so the toolbar is replaced by skeletons
    // until it resolves — wait for it back before touching the filters.
    expect(await screen.findByText('Page 2 of 3')).toBeInTheDocument()

    await user.click(statusSelect())
    await user.click(await screen.findByRole('option', { name: 'Success' }))

    await waitFor(() =>
      expect(mockGetUpdateHistory).toHaveBeenLastCalledWith(
        expect.objectContaining({ page: 1, status: 'success' }),
      ),
    )
  })
})
