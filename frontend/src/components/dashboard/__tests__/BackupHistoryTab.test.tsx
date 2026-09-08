import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { BackupHistoryTab } from '../BackupHistoryTab'
import type { BackupRun, BackupRunItem } from '@/types'

/**
 * The tab is rendered for real with only the API layer mocked, so the query
 * key, the hook and the component's own filter/paging state are all exercised.
 */

const mockGetHistory = vi.fn()
const mockGetRun = vi.fn()

vi.mock('@/lib/api', () => ({
  backupApi: {
    getHistory: (...a: unknown[]) => mockGetHistory(...a),
    getRun: (...a: unknown[]) => mockGetRun(...a),
  },
}))

beforeEach(() => {
  // Radix Select drives pointer capture, which jsdom does not implement.
  Element.prototype.hasPointerCapture = () => false
  Element.prototype.setPointerCapture = () => {}
  Element.prototype.releasePointerCapture = () => {}
})

const run = (over: Partial<BackupRun> = {}): BackupRun => ({
  id: 'run-1',
  kind: 'backup',
  trigger: 'scheduled',
  status: 'success',
  startedAt: '2026-09-07T10:00:00Z',
  finishedAt: '2026-09-07T10:00:30Z',
  stacksTotal: 3,
  stacksOk: 3,
  stacksFailed: 0,
  bytesAdded: 2048,
  ...over,
})

const item = (over: Partial<BackupRunItem> = {}): BackupRunItem => ({
  id: 'item-1',
  runId: 'run-1',
  stackId: 'stack-alpha',
  status: 'success',
  snapshotId: 'abcdef123456',
  stopApplied: false,
  durationMs: 1200,
  ...over,
})

const historyPage = (over: Record<string, unknown> = {}) => ({
  runs: [run()],
  total: 1,
  page: 1,
  limit: 25,
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

const renderTab = () => render(<BackupHistoryTab />, { wrapper: createWrapper() })

// The four filter triggers carry no accessible name, so they are addressed by
// position — the same approach UpdateLogTab.test.tsx takes.
const statusSelect = () => screen.getAllByRole('combobox')[0]
const kindSelect = () => screen.getAllByRole('combobox')[1]
const triggerSelect = () => screen.getAllByRole('combobox')[2]
const dateSelect = () => screen.getAllByRole('combobox')[3]

beforeEach(() => {
  vi.clearAllMocks()
  mockGetHistory.mockResolvedValue(historyPage())
  mockGetRun.mockResolvedValue({ run: run(), items: [item()] })
})

describe('BackupHistoryTab — the four render states', () => {
  it('shows skeletons and no table while the first page loads', () => {
    mockGetHistory.mockReturnValue(new Promise(() => {}))
    const { container } = renderTab()

    expect(container.querySelectorAll('.animate-pulse').length).toBeGreaterThan(0)
    expect(screen.queryByRole('table')).not.toBeInTheDocument()
  })

  it('offers a retry that re-issues the query when the history fails to load', async () => {
    mockGetHistory.mockRejectedValue(new Error('boom'))
    renderTab()

    expect(
      await screen.findByText('Failed to Load Backup History', {}, { timeout: 5000 }),
    ).toBeInTheDocument()

    const callsBeforeRetry = mockGetHistory.mock.calls.length
    mockGetHistory.mockResolvedValue(historyPage())
    fireEvent.click(screen.getByRole('button', { name: /Retry/ }))

    expect(await screen.findByText('run-1')).toBeInTheDocument()
    expect(mockGetHistory.mock.calls.length).toBeGreaterThan(callsBeforeRetry)
  })

  it('explains an empty history instead of rendering a blank table', async () => {
    mockGetHistory.mockResolvedValue(historyPage({ runs: [], total: 0, totalPages: 0 }))
    renderTab()

    expect(await screen.findByText('No backup history')).toBeInTheDocument()
    expect(screen.getByText(/Settings → Backup/)).toBeInTheDocument()
    expect(screen.queryByRole('table')).not.toBeInTheDocument()
  })

  it('renders one row per run when the history is populated', async () => {
    mockGetHistory.mockResolvedValue(
      historyPage({
        runs: [run({ id: 'run-1' }), run({ id: 'run-2' }), run({ id: 'run-3' })],
        total: 3,
      }),
    )
    renderTab()

    await screen.findByText('run-1')
    // One header row plus three body rows.
    expect(screen.getAllByRole('row')).toHaveLength(4)
    expect(screen.getByText('run-2')).toBeInTheDocument()
    expect(screen.getByText('run-3')).toBeInTheDocument()
  })
})

describe('BackupHistoryTab — the columns', () => {
  it('renders kind, trigger, stacks ok/failed, bytes added and duration', async () => {
    mockGetHistory.mockResolvedValue(
      historyPage({ runs: [run({ stacksOk: 2, stacksFailed: 1, bytesAdded: 2048 })] }),
    )
    renderTab()

    await screen.findByText('run-1')
    expect(screen.getByText('backup')).toBeInTheDocument()
    expect(screen.getByText('scheduled')).toBeInTheDocument()
    expect(screen.getByText('2 ok / 1 failed')).toBeInTheDocument()
    expect(screen.getByText('2.00 KB')).toBeInTheDocument()
    expect(screen.getByText('30.0s')).toBeInTheDocument()
  })

  it('shows a dash for the duration of a run that has not finished', async () => {
    mockGetHistory.mockResolvedValue(
      historyPage({ runs: [run({ status: 'running', finishedAt: null })] }),
    )
    renderTab()

    await screen.findByText('run-1')
    expect(screen.getByTestId('run-duration-run-1')).toHaveTextContent('-')
  })

  it('says "No change" rather than "0 B" when a backup added nothing', async () => {
    mockGetHistory.mockResolvedValue(historyPage({ runs: [run({ bytesAdded: 0 })] }))
    renderTab()

    await screen.findByText('run-1')
    expect(screen.getByText('No change')).toBeInTheDocument()
  })
})

describe('BackupHistoryTab — the status badge', () => {
  it('renders an interrupted run with the neutral slate tone, not the destructive tone', async () => {
    mockGetHistory.mockResolvedValue(historyPage({ runs: [run({ status: 'interrupted' })] }))
    renderTab()

    const badge = await screen.findByText('Interrupted')
    expect(badge.className).toContain('slate')
    expect(badge.className).not.toContain('destructive')
  })

  it('renders a failed run with the destructive tone — the control for the case above', async () => {
    mockGetHistory.mockResolvedValue(historyPage({ runs: [run({ status: 'failed' })] }))
    renderTab()

    const badge = await screen.findByText('Failed')
    expect(badge.className).toContain('destructive')
    expect(badge.className).not.toContain('slate')
  })
})

describe('BackupHistoryTab — the filters', () => {
  it('sends no status/kind/trigger/from keys while every filter is "all"', async () => {
    renderTab()

    await screen.findByText('run-1')
    expect(mockGetHistory).toHaveBeenCalledWith({ page: 1, limit: 25 })
  })

  it('sends the status filter and resets to page 1', async () => {
    const user = userEvent.setup()
    mockGetHistory.mockResolvedValue(historyPage({ total: 60, totalPages: 3 }))
    renderTab()

    await screen.findByText('run-1')
    fireEvent.click(screen.getByRole('button', { name: /Next/ }))
    await waitFor(() =>
      expect(mockGetHistory).toHaveBeenLastCalledWith(expect.objectContaining({ page: 2 })),
    )
    // Page 2 is its own query key, so the toolbar is replaced by skeletons
    // until it resolves; wait for it back before touching a select.
    await screen.findByText('Page 2 of 3')

    await user.click(statusSelect())
    await user.click(await screen.findByRole('option', { name: 'Failed' }))

    await waitFor(() =>
      expect(mockGetHistory).toHaveBeenLastCalledWith(
        expect.objectContaining({ status: 'failed', page: 1 }),
      ),
    )
  })

  it('sends the kind filter and resets to page 1', async () => {
    const user = userEvent.setup()
    mockGetHistory.mockResolvedValue(historyPage({ total: 60, totalPages: 3 }))
    renderTab()

    await screen.findByText('run-1')
    fireEvent.click(screen.getByRole('button', { name: /Next/ }))
    await waitFor(() =>
      expect(mockGetHistory).toHaveBeenLastCalledWith(expect.objectContaining({ page: 2 })),
    )
    // Page 2 is its own query key, so the toolbar is replaced by skeletons
    // until it resolves; wait for it back before touching a select.
    await screen.findByText('Page 2 of 3')

    await user.click(kindSelect())
    await user.click(await screen.findByRole('option', { name: 'Restore' }))

    await waitFor(() =>
      expect(mockGetHistory).toHaveBeenLastCalledWith(
        expect.objectContaining({ kind: 'restore', page: 1 }),
      ),
    )
  })

  it('sends the trigger filter and resets to page 1', async () => {
    const user = userEvent.setup()
    mockGetHistory.mockResolvedValue(historyPage({ total: 60, totalPages: 3 }))
    renderTab()

    await screen.findByText('run-1')
    fireEvent.click(screen.getByRole('button', { name: /Next/ }))
    await waitFor(() =>
      expect(mockGetHistory).toHaveBeenLastCalledWith(expect.objectContaining({ page: 2 })),
    )
    // Page 2 is its own query key, so the toolbar is replaced by skeletons
    // until it resolves; wait for it back before touching a select.
    await screen.findByText('Page 2 of 3')

    await user.click(triggerSelect())
    await user.click(await screen.findByRole('option', { name: 'Manual' }))

    await waitFor(() =>
      expect(mockGetHistory).toHaveBeenLastCalledWith(
        expect.objectContaining({ trigger: 'manual', page: 1 }),
      ),
    )
  })

  it('turns the date range into an ISO "from" bound and resets to page 1', async () => {
    const user = userEvent.setup()
    mockGetHistory.mockResolvedValue(historyPage({ total: 60, totalPages: 3 }))
    renderTab()

    await screen.findByText('run-1')
    fireEvent.click(screen.getByRole('button', { name: /Next/ }))
    await waitFor(() =>
      expect(mockGetHistory).toHaveBeenLastCalledWith(expect.objectContaining({ page: 2 })),
    )
    // Page 2 is its own query key, so the toolbar is replaced by skeletons
    // until it resolves; wait for it back before touching a select.
    await screen.findByText('Page 2 of 3')

    await user.click(dateSelect())
    await user.click(await screen.findByRole('option', { name: 'Last 7 days' }))

    await waitFor(() =>
      expect(mockGetHistory).toHaveBeenLastCalledWith(
        expect.objectContaining({ from: expect.stringMatching(/^\d{4}-\d{2}-\d{2}T/), page: 1 }),
      ),
    )
  })

  it('filters the fetched page client-side on the free-text box', async () => {
    mockGetHistory.mockResolvedValue(
      historyPage({
        runs: [run({ id: 'run-1' }), run({ id: 'run-2', kind: 'prune' })],
        total: 2,
      }),
    )
    renderTab()

    fireEvent.change(await screen.findByPlaceholderText('Filter runs…'), {
      target: { value: 'prune' },
    })

    expect(screen.getByText('run-2')).toBeInTheDocument()
    expect(screen.queryByText('run-1')).not.toBeInTheDocument()
    expect(screen.getByText('1 of 2 records')).toBeInTheDocument()
  })
})

describe('BackupHistoryTab — the pager', () => {
  it('renders no pager at all for a single page', async () => {
    mockGetHistory.mockResolvedValue(historyPage({ totalPages: 1 }))
    renderTab()

    await screen.findByText('run-1')
    expect(screen.queryByRole('button', { name: /Previous/ })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Next/ })).not.toBeInTheDocument()
  })

  it('renders the pager with Previous disabled and Next enabled on page 1 of 3', async () => {
    mockGetHistory.mockResolvedValue(historyPage({ total: 60, totalPages: 3 }))
    renderTab()

    expect(await screen.findByText('Page 1 of 3')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Previous/ })).toBeDisabled()
    expect(screen.getByRole('button', { name: /Next/ })).toBeEnabled()
  })

  it('disables Next on the last page', async () => {
    mockGetHistory.mockResolvedValue(historyPage({ total: 60, totalPages: 2 }))
    renderTab()

    await screen.findByText('Page 1 of 2')
    fireEvent.click(screen.getByRole('button', { name: /Next/ }))

    await screen.findByText('Page 2 of 2')
    expect(screen.getByRole('button', { name: /Next/ })).toBeDisabled()
    expect(screen.getByRole('button', { name: /Previous/ })).toBeEnabled()
  })
})

describe('BackupHistoryTab — expandable run rows', () => {
  it('fetches the run detail once, and re-expanding does not fetch again', async () => {
    const user = userEvent.setup()
    renderTab()

    await screen.findByText('run-1')
    expect(mockGetRun).toHaveBeenCalledTimes(0)

    await user.click(screen.getByRole('button', { name: /Show details for run run-1/ }))
    expect(await screen.findByText('stack-alpha')).toBeInTheDocument()
    expect(mockGetRun).toHaveBeenCalledTimes(1)
    expect(mockGetRun).toHaveBeenCalledWith('run-1')

    await user.click(screen.getByRole('button', { name: /Hide details for run run-1/ }))
    await waitFor(() => expect(screen.queryByText('stack-alpha')).not.toBeInTheDocument())

    await user.click(screen.getByRole('button', { name: /Show details for run run-1/ }))
    expect(await screen.findByText('stack-alpha')).toBeInTheDocument()
    expect(mockGetRun).toHaveBeenCalledTimes(1)
  })

  it('lists every run item with its per-stack status', async () => {
    const user = userEvent.setup()
    mockGetRun.mockResolvedValue({
      run: run(),
      items: [
        item({ id: 'i1', stackId: 'stack-alpha', status: 'success' }),
        item({ id: 'i2', stackId: 'stack-beta', status: 'failed', errorMessage: 'disk full' }),
        item({ id: 'i3', stackId: 'stack-gamma', status: 'skipped' }),
      ],
    })
    renderTab()

    await screen.findByText('run-1')
    await user.click(screen.getByRole('button', { name: /Show details for run run-1/ }))

    expect(await screen.findByText('stack-alpha')).toBeInTheDocument()
    expect(screen.getByText('stack-beta')).toBeInTheDocument()
    expect(screen.getByText('stack-gamma')).toBeInTheDocument()
    expect(screen.getByTestId('run-item-status-i1')).toHaveTextContent('success')
    expect(screen.getByTestId('run-item-status-i2')).toHaveTextContent('failed')
    expect(screen.getByTestId('run-item-status-i3')).toHaveTextContent('skipped')
    expect(screen.getByText('disk full')).toBeInTheDocument()
  })

  it('explains a run that recorded no per-stack items', async () => {
    const user = userEvent.setup()
    mockGetRun.mockResolvedValue({ run: run(), items: [] })
    renderTab()

    await screen.findByText('run-1')
    await user.click(screen.getByRole('button', { name: /Show details for run run-1/ }))

    expect(await screen.findByText('No per-stack records for this run.')).toBeInTheDocument()
  })

  it('reports a failed run-detail fetch instead of showing an empty panel', async () => {
    const user = userEvent.setup()
    mockGetRun.mockRejectedValue(new Error('nope'))
    renderTab()

    await screen.findByText('run-1')
    await user.click(screen.getByRole('button', { name: /Show details for run run-1/ }))

    expect(
      await screen.findByText('Failed to load run details.', {}, { timeout: 5000 }),
    ).toBeInTheDocument()
  })
})
