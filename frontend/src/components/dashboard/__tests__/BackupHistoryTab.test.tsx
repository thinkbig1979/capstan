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

// agent-os-4zx0: only a FINISHED backup run writes stacksOk/stacksFailed
// (services.RunBackup counts them in its per-stack loop and saves them when the
// run ends). Every other kind, and a backup that is still running or was
// interrupted, carries the column's zero default, which rendered as a made-up
// "0 ok / 0 failed".
describe('BackupHistoryTab — the Stacks column per kind', () => {
  const stacksCell = () => screen.getByTestId('run-stacks-run-1')

  it.each(['success', 'partial', 'failed'] as const)(
    'shows the counts for a finished backup run (%s)',
    async (status) => {
      mockGetHistory.mockResolvedValue(
        historyPage({ runs: [run({ status, stacksOk: 2, stacksFailed: 1 })] }),
      )
      renderTab()

      await screen.findByText('run-1')
      expect(stacksCell()).toHaveTextContent('2 ok / 1 failed')
    },
  )

  it('keeps a real zero for a finished backup run that attempted no stacks', async () => {
    mockGetHistory.mockResolvedValue(
      historyPage({ runs: [run({ status: 'failed', stacksTotal: 0, stacksOk: 0, stacksFailed: 0 })] }),
    )
    renderTab()

    await screen.findByText('run-1')
    expect(stacksCell()).toHaveTextContent('0 ok / 0 failed')
  })

  it.each([
    ['restore', 'Not applicable to restore runs'],
    ['sync', 'Not applicable to sync runs'],
    ['prune', 'Not applicable to prune runs'],
    ['dr_restore', 'Not applicable to DR restore runs'],
    ['verify', 'Not applicable to repository check runs'],
  ] as const)(
    'shows a dash, not 0 ok / 0 failed, for a %s run',
    async (kind, title) => {
      mockGetHistory.mockResolvedValue(
        historyPage({ runs: [run({ kind, stacksTotal: 0, stacksOk: 0, stacksFailed: 0 })] }),
      )
      renderTab()

      await screen.findByText('run-1')
      expect(stacksCell()).toHaveTextContent(/^-$/)
      expect(stacksCell().querySelector('span')).toHaveAttribute('title', title)
    },
  )

  it.each([
    ['running', 'Counted when the run finishes'],
    ['interrupted', 'Not recorded'],
    ['skipped', 'Nothing ran'],
  ] as const)('shows a dash for a backup run that is %s', async (status, title) => {
    mockGetHistory.mockResolvedValue(
      historyPage({
        runs: [run({ status, finishedAt: undefined, stacksOk: 0, stacksFailed: 0 })],
      }),
    )
    renderTab()

    await screen.findByText('run-1')
    expect(stacksCell()).toHaveTextContent(/^-$/)
    expect(stacksCell().querySelector('span')).toHaveAttribute('title', title)
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

  // MUST-STAY-GREEN guard, not a fail-first test: the Kind cell renders
  // {run.kind} raw, so a verify run already reads "verify" there the moment the
  // type admits the kind, exactly as a dr_restore run reads "dr_restore". This
  // pins that no label map is introduced for verify alone (agent-os-5lpz A2).
  it('renders a verify run under its own kind label', async () => {
    mockGetHistory.mockResolvedValue(historyPage({ runs: [run({ kind: 'verify' })] }))
    renderTab()

    await screen.findByText('run-1')
    expect(screen.getByText('verify')).toBeInTheDocument()
  })

  it('shows a dash for the duration of a run that has not finished', async () => {
    mockGetHistory.mockResolvedValue(
      historyPage({ runs: [run({ status: 'running', finishedAt: undefined })] }),
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

  // agent-os-4i7r: a scheduled backup that never started is grey, and its
  // reason (error_message) is shown when the row is expanded.
  it('renders a skipped run grey with its reason, not the destructive tone', async () => {
    mockGetHistory.mockResolvedValue(
      historyPage({
        runs: [run({ status: 'skipped', errorMessage: 'scheduled backup skipped: backup engine unavailable' })],
      }),
    )
    renderTab()

    const badge = await screen.findByText('Skipped')
    expect(badge.className).toContain('gray')
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

  it('offers Skipped as a status filter and sends it', async () => {
    const user = userEvent.setup()
    mockGetHistory.mockResolvedValue(historyPage({}))
    renderTab()

    await screen.findByText('run-1')
    await user.click(statusSelect())
    await user.click(await screen.findByRole('option', { name: 'Skipped' }))

    await waitFor(() =>
      expect(mockGetHistory).toHaveBeenLastCalledWith(expect.objectContaining({ status: 'skipped' })),
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

  // agent-os-5lpz A4, the client half. Only the OUTBOUND side is testable here:
  // that picking Verify sends kind: 'verify'. The other half -- that the other
  // five kinds are then hidden -- is the SERVER's, done by GetBackupRunsFiltered
  // and pinned by the {"kind", models.BackupHistoryFilters{Kind: "backup"}, ...}
  // table case in backend/internal/database/backup_test.go and by
  // TestGetStatus_LastVerifySurfacesFailure. There is deliberately no
  // client-side kind filter to test.
  it('sends the verify kind filter and resets to page 1', async () => {
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
    await user.click(await screen.findByRole('option', { name: 'Verify' }))

    await waitFor(() =>
      expect(mockGetHistory).toHaveBeenLastCalledWith(
        expect.objectContaining({ kind: 'verify', page: 1 }),
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
  it('caches a terminal run: fetches once, and re-expanding does not fetch again', async () => {
    // The default run() fixture is status 'success' — a terminal run.
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

  it('does NOT cache a still-running run: re-expanding refetches', async () => {
    // The other arm of the guard above. A terminal run is cached because its
    // items can no longer change; a running run is still writing them, so the
    // same interaction must produce a second request. Without this test the
    // suite could not tell a correct staleTime from one that is simply too
    // broad.
    const user = userEvent.setup()
    const live = run({ status: 'running', finishedAt: undefined })
    mockGetHistory.mockResolvedValue(historyPage({ runs: [live] }))
    mockGetRun.mockResolvedValue({ run: live, items: [item()] })
    renderTab()

    await screen.findByText('run-1')
    expect(mockGetRun).toHaveBeenCalledTimes(0)

    await user.click(screen.getByRole('button', { name: /Show details for run run-1/ }))
    expect(await screen.findByText('stack-alpha')).toBeInTheDocument()
    expect(mockGetRun).toHaveBeenCalledTimes(1)

    await user.click(screen.getByRole('button', { name: /Hide details for run run-1/ }))
    await waitFor(() => expect(screen.queryByText('stack-alpha')).not.toBeInTheDocument())

    await user.click(screen.getByRole('button', { name: /Show details for run run-1/ }))
    await waitFor(() => expect(mockGetRun).toHaveBeenCalledTimes(2))
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

  /**
   * agent-os-evtz. A restore run has no per-stack items, so the run's own
   * errorMessage is the only durable record of WHY a failed restore left the
   * stack stopped, or that a restored stack did not fully restart. It used to
   * be fetched and never rendered: the panel said "No per-stack records".
   */
  const STOPPED_MSG =
    'restic restore: exit 1; stack left stopped deliberately so you can inspect /opt/stacks/app and retry (not auto-restarting over a possibly partial restore)'
  const PARTIAL_MSG =
    'restore completed, but restarting stack app partially succeeded: 1 of 3 containers not running'

  it.each([
    ['failed', STOPPED_MSG, 'text-destructive'],
    ['partial', PARTIAL_MSG, 'text-yellow-700'],
  ] as const)('shows a %s restore run\'s error message instead of "no per-stack records"', async (status, msg, tone) => {
    const user = userEvent.setup()
    const restoreRun = run({ kind: 'restore', trigger: 'manual', status, stacksTotal: 0, stacksOk: 0, stacksFailed: 0, errorMessage: msg })
    mockGetHistory.mockResolvedValue(historyPage({ runs: [restoreRun] }))
    mockGetRun.mockResolvedValue({ run: restoreRun, items: [] })
    renderTab()

    await screen.findByText('run-1')
    await user.click(screen.getByRole('button', { name: /Show details for run run-1/ }))

    const shown = await screen.findByTestId('run-error-message-run-1')
    expect(shown).toHaveTextContent(msg)
    expect(shown).toHaveClass(tone)
    expect(screen.queryByText('No per-stack records for this run.')).not.toBeInTheDocument()
  })

  it('still shows the run\'s error message when the detail fetch fails', async () => {
    const user = userEvent.setup()
    mockGetHistory.mockResolvedValue(
      historyPage({ runs: [run({ kind: 'restore', status: 'failed', errorMessage: STOPPED_MSG })] }),
    )
    mockGetRun.mockRejectedValue(new Error('nope'))
    renderTab()

    await screen.findByText('run-1')
    await user.click(screen.getByRole('button', { name: /Show details for run run-1/ }))

    expect(
      await screen.findByText('Failed to load run details.', {}, { timeout: 5000 }),
    ).toBeInTheDocument()
    expect(screen.getByTestId('run-error-message-run-1')).toHaveTextContent(STOPPED_MSG)
  })

  it('explains a run that recorded no per-stack items', async () => {
    const user = userEvent.setup()
    mockGetRun.mockResolvedValue({ run: run(), items: [] })
    renderTab()

    await screen.findByText('run-1')
    await user.click(screen.getByRole('button', { name: /Show details for run run-1/ }))

    expect(await screen.findByText('No per-stack records for this run.')).toBeInTheDocument()
  })

  /**
   * agent-os-rtn8. getRunDetail (handlers/backup.go:873-907) answers with
   * THREE distinct refusals and this branch rendered one sentence for all of
   * them: a 404 "Backup run not found", and two 500s the handler deliberately
   * keeps apart -- "Failed to load backup run" (the run row) and "Failed to
   * fetch backup run items" (the per-stack rows). The 404 says the run is gone
   * and the row is stale; the 500s say the database is broken and name WHICH
   * read failed. The operator saw "Failed to load run details." for all three.
   *
   * The 5xx cases reach us at all only because agent-os-mc4i stopped
   * classifyError's 5xx arm answering a bare status code, and they carry that
   * arm's deliberate `${status}: ` prefix -- 503 vs 500 is itself diagnostic.
   *
   * The fixture is the FLAT shape api.ts's interceptor rejects with
   * (api.ts:127-131), which is what actually reaches the component.
   */
  it.each([
    [404, 'NOT_FOUND', 'Backup run not found', 'Backup run not found'],
    [500, 'INTERNAL_ERROR', 'Failed to load backup run', '500: Failed to load backup run'],
    [500, 'INTERNAL_ERROR', 'Failed to fetch backup run items', '500: Failed to fetch backup run items'],
  ])('names the cause the backend sent for a %s %s failure', async (status, code, message, rendered) => {
    const user = userEvent.setup()
    mockGetRun.mockRejectedValue({ status, code, message })
    renderTab()

    await screen.findByText('run-1')
    await user.click(screen.getByRole('button', { name: /Show details for run run-1/ }))

    expect(
      await screen.findByText('Failed to load run details.', {}, { timeout: 5000 }),
    ).toBeInTheDocument()
    expect(screen.getByText(rendered)).toBeInTheDocument()
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

// Same render as renderTab, but hands back the QueryClient so a test can drive
// a REFETCH. The component exposes refetch only from inside the branch under
// test, so asserting through it would be circular.
function renderTabWithClient() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: 0 }, mutations: { retry: false } },
  })
  const view = render(
    <QueryClientProvider client={queryClient}>
      <BackupHistoryTab />
    </QueryClientProvider>,
  )
  return { ...view, queryClient }
}

/**
 * agent-os-wczm. `staleTime` is 30s app-wide, `refetchOnWindowFocus` is on and
 * a 500 is not auto-retryable, so "tab away, tab back, one 500" is routine. A
 * bare `isError` guard is true for that refetch failure as well as for a first
 * load, so it replaced rows the server had ALREADY sent with an error card.
 *
 * Both arms RESOLVE first and reject a REFETCH. A first-fetch-rejects fixture
 * is structurally blind to this class: it leaves `data` undefined, so it stays
 * green whether the guard reads `isError` or `isError && !data`.
 */
describe('BackupHistoryTab — a failed REFETCH must not discard data', () => {
  it('keeps the populated history table when a REFETCH fails', async () => {
    const { queryClient } = renderTabWithClient()

    expect(await screen.findByText('run-1')).toBeInTheDocument()

    mockGetHistory.mockRejectedValue(new Error('boom'))
    await queryClient.refetchQueries({ queryKey: ['backup', 'history'] })

    // The query IS in the error state now — that is the point of the arm.
    await waitFor(() =>
      expect(
        queryClient.getQueryCache().find({ queryKey: ['backup', 'history'], exact: false })
          ?.state.status,
      ).toBe('error'),
    )
    // …and the rows the server already sent are still on screen.
    expect(screen.getByText('run-1')).toBeInTheDocument()
    expect(screen.queryByText('Failed to Load Backup History')).not.toBeInTheDocument()
    // Both halves of RefreshFailedNotice's `beforeSave` contract are pinned, one
    // per arm, because all twelve arms pass through the same component: asserting
    // only the shared "Could not refresh …" prefix would stay green if the
    // beforeSave branch were deleted outright. This is the READ-ONLY variant — a
    // table nobody writes back, so no save warning. The write-back variant is
    // pinned in HistoryRetentionSection.test.tsx.
    expect(
      screen.getByText(/Could not refresh the backup history\. The values shown are the last ones the server sent\./),
    ).toBeInTheDocument()
    expect(screen.queryByText(/check them before saving/)).not.toBeInTheDocument()
  })

  it('keeps the populated run-detail rows when a REFETCH fails', async () => {
    const user = userEvent.setup()
    const { queryClient } = renderTabWithClient()

    await screen.findByText('run-1')
    await user.click(screen.getByRole('button', { name: /Show details for run run-1/ }))
    expect(await screen.findByText('stack-alpha')).toBeInTheDocument()

    mockGetRun.mockRejectedValue(new Error('boom'))
    await queryClient.refetchQueries({ queryKey: ['backup', 'runs', 'run-1'] })

    await waitFor(() =>
      expect(queryClient.getQueryState(['backup', 'runs', 'run-1'])?.status).toBe('error'),
    )
    expect(screen.getByText('stack-alpha')).toBeInTheDocument()
    expect(screen.queryByText('Failed to load run details.')).not.toBeInTheDocument()
    expect(screen.getByText(/Could not refresh the run details/)).toBeInTheDocument()

    // agent-os-3k31: the notice offers Retry, and Retry re-runs the read that failed.
    expect(screen.queryByRole('button', { name: 'Retry' })).toBeInTheDocument()
    mockGetRun.mockResolvedValue({ run: run(), items: [item()] })
    const callsBeforeRetry = mockGetRun.mock.calls.length
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    await waitFor(() => expect(mockGetRun.mock.calls.length).toBeGreaterThan(callsBeforeRetry))
    await waitFor(() => expect(screen.queryByText(/Could not refresh the run details/)).not.toBeInTheDocument())
  })
})
