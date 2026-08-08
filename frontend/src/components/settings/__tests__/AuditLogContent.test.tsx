import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { AuditLogContent } from '../AuditLogContent'

/**
 * SettingsPage.test.tsx:49 replaces this panel with an empty div, so nothing
 * rendered it before (0/72 statements, agent-os-m1mu).
 *
 * HistoryRetentionSection is stubbed here — it is a sibling with its own
 * tests and already measures 100%, and stubbing it keeps these assertions
 * about the audit log itself. The component under test is NOT mocked.
 */

const mockGetAuditLog = vi.fn()

vi.mock('@/lib/api', () => ({
  settingsApi: {
    getAuditLog: (...args: unknown[]) => mockGetAuditLog(...args),
  },
}))

vi.mock('../HistoryRetentionSection', () => ({
  HistoryRetentionSection: () => <div data-testid="retention-section" />,
}))

beforeEach(() => {
  Element.prototype.hasPointerCapture = () => false
  Element.prototype.setPointerCapture = () => {}
  Element.prototype.releasePointerCapture = () => {}
})

function createWrapper() {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false, staleTime: 0 },
      mutations: { retry: false },
    },
  })
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  )
}

const renderPanel = () => render(<AuditLogContent />, { wrapper: createWrapper() })

const entry = (over: Record<string, unknown> = {}) => ({
  id: 'e1',
  userId: 'abcdef1234567890',
  action: 'stack.start',
  detail: '',
  createdAt: '2026-08-08T10:00:00Z',
  ...over,
})

const page = (over: Record<string, unknown> = {}) => ({
  entries: [entry()],
  total: 1,
  availableActions: ['stack.start', 'auth.login'],
  ...over,
})

beforeEach(() => {
  vi.clearAllMocks()
  mockGetAuditLog.mockResolvedValue(page())
})

describe('AuditLogContent — loading, empty and error states', () => {
  it('shows a spinner while the first page loads', () => {
    mockGetAuditLog.mockReturnValue(new Promise(() => {}))
    renderPanel()

    expect(screen.queryByRole('table')).not.toBeInTheDocument()
    expect(screen.queryByText('No audit log entries yet.')).not.toBeInTheDocument()
  })

  it('reports a failure to load', async () => {
    mockGetAuditLog.mockRejectedValue(new Error('boom'))
    renderPanel()

    expect(await screen.findByText('Failed to load audit log.')).toBeInTheDocument()
  })

  it('skips the filter bar entirely for a genuinely empty log', async () => {
    mockGetAuditLog.mockResolvedValue(page({ entries: [], total: 0 }))
    renderPanel()

    expect(await screen.findByText('No audit log entries yet.')).toBeInTheDocument()
    // No point offering filters over nothing.
    expect(screen.queryByLabelText('Search')).not.toBeInTheDocument()
    // The scope note is still shown, so the user learns what would be recorded.
    expect(screen.getByText(/Records security-relevant actions/)).toBeInTheDocument()
  })

  it('always renders the retention controls below the log', async () => {
    renderPanel()

    expect(await screen.findByTestId('retention-section')).toBeInTheDocument()
  })
})

describe('AuditLogContent — the table', () => {
  it('renders a row per entry with the user id truncated to 8 characters', async () => {
    renderPanel()

    expect(await screen.findByText('abcdef12')).toBeInTheDocument()
    expect(screen.getByText('stack.start')).toBeInTheDocument()
  })

  it('shows an em dash when an entry has no detail', async () => {
    renderPanel()

    expect(await screen.findByText('—')).toBeInTheDocument()
    // Nothing to expand, so no raw-detail toggle.
    expect(screen.queryByRole('button', { name: 'View raw detail' })).not.toBeInTheDocument()
  })

  it('summarises a JSON detail as readable key/value pairs', async () => {
    mockGetAuditLog.mockResolvedValue(
      page({ entries: [entry({ detail: '{"stack_name":"web","dry_run":true,"forced":false}' })] }),
    )
    renderPanel()

    // Underscores become spaces and booleans become yes/no.
    expect(
      await screen.findByText('stack name: web · dry run: yes · forced: no'),
    ).toBeInTheDocument()
  })

  it('abbreviates long id-like values in the summary', async () => {
    mockGetAuditLog.mockResolvedValue(
      page({ entries: [entry({ detail: '{"container_id":"0123456789abcdef0123456789abcdef"}' })] }),
    )
    renderPanel()

    expect(await screen.findByText('container id: 01234567…')).toBeInTheDocument()
  })

  it('leaves a long non-hex value alone', async () => {
    mockGetAuditLog.mockResolvedValue(
      page({ entries: [entry({ detail: '{"path":"/srv/stacks/my-long-project"}' })] }),
    )
    renderPanel()

    expect(await screen.findByText('path: /srv/stacks/my-long-project')).toBeInTheDocument()
  })

  it('shows a non-JSON detail verbatim, with nothing to expand', async () => {
    mockGetAuditLog.mockResolvedValue(
      page({ entries: [entry({ detail: 'plain text reason' })] }),
    )
    renderPanel()

    expect(await screen.findByText('plain text reason')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'View raw detail' })).not.toBeInTheDocument()
  })

  it('treats a JSON array detail as plain text, not as key/value pairs', async () => {
    mockGetAuditLog.mockResolvedValue(page({ entries: [entry({ detail: '[1,2,3]' })] }))
    renderPanel()

    expect(await screen.findByText('[1,2,3]')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'View raw detail' })).not.toBeInTheDocument()
  })

  it('expands and collapses the pretty-printed original', async () => {
    mockGetAuditLog.mockResolvedValue(
      page({ entries: [entry({ detail: '{"stack_name":"web"}' })] }),
    )
    renderPanel()

    const toggle = await screen.findByRole('button', { name: 'View raw detail' })
    expect(toggle).toHaveAttribute('aria-expanded', 'false')

    fireEvent.click(toggle)

    const collapse = screen.getByRole('button', { name: 'Hide raw detail' })
    expect(collapse).toHaveAttribute('aria-expanded', 'true')
    expect(screen.getByText(/"stack_name": "web"/)).toBeInTheDocument()

    fireEvent.click(collapse)
    expect(screen.queryByText(/"stack_name": "web"/)).not.toBeInTheDocument()
  })

  it('expands one row without expanding its neighbour', async () => {
    mockGetAuditLog.mockResolvedValue(
      page({
        entries: [
          entry({ id: 'e1', detail: '{"a":"first"}' }),
          entry({ id: 'e2', detail: '{"b":"second"}' }),
        ],
        total: 2,
      }),
    )
    renderPanel()

    const toggles = await screen.findAllByRole('button', { name: 'View raw detail' })
    fireEvent.click(toggles[0])

    expect(screen.getByText(/"a": "first"/)).toBeInTheDocument()
    expect(screen.queryByText(/"b": "second"/)).not.toBeInTheDocument()
  })
})

describe('AuditLogContent — filters', () => {
  it('debounces the free-text search before refetching', async () => {
    renderPanel()

    const search = await screen.findByPlaceholderText('Search detail or action')
    fireEvent.change(search, { target: { value: 'login' } })

    // Not sent on the keystroke itself.
    expect(mockGetAuditLog).toHaveBeenCalledTimes(1)

    await waitFor(() =>
      expect(mockGetAuditLog).toHaveBeenLastCalledWith(1, 50, {
        action: '',
        search: 'login',
        dateFrom: '',
        dateTo: '',
      }),
    )
  })

  it('trims the search term', async () => {
    renderPanel()

    fireEvent.change(await screen.findByPlaceholderText('Search detail or action'), {
      target: { value: '  login  ' },
    })

    await waitFor(() =>
      expect(mockGetAuditLog).toHaveBeenLastCalledWith(
        1,
        50,
        expect.objectContaining({ search: 'login' }),
      ),
    )
  })

  it('filters by action, offering the actions the server reported', async () => {
    const user = userEvent.setup()
    renderPanel()

    await user.click(await screen.findByRole('combobox', { name: 'Action' }))
    await user.click(await screen.findByRole('option', { name: 'auth.login' }))

    await waitFor(() =>
      expect(mockGetAuditLog).toHaveBeenLastCalledWith(
        1,
        50,
        expect.objectContaining({ action: 'auth.login' }),
      ),
    )
  })

  it('maps the "All actions" sentinel back to an empty filter', async () => {
    const user = userEvent.setup()
    renderPanel()

    await user.click(await screen.findByRole('combobox', { name: 'Action' }))
    await user.click(await screen.findByRole('option', { name: 'auth.login' }))
    await waitFor(() => expect(screen.getByRole('button', { name: /Clear/ })).toBeInTheDocument())

    await user.click(screen.getByRole('combobox', { name: 'Action' }))
    await user.click(await screen.findByRole('option', { name: 'All actions' }))

    await waitFor(() =>
      expect(mockGetAuditLog).toHaveBeenLastCalledWith(
        1,
        50,
        expect.objectContaining({ action: '' }),
      ),
    )
  })

  it('filters by date range', async () => {
    renderPanel()

    fireEvent.change(await screen.findByLabelText('From'), { target: { value: '2026-08-01' } })
    fireEvent.change(screen.getByLabelText('To'), { target: { value: '2026-08-08' } })

    await waitFor(() =>
      expect(mockGetAuditLog).toHaveBeenLastCalledWith(
        1,
        50,
        expect.objectContaining({ dateFrom: '2026-08-01', dateTo: '2026-08-08' }),
      ),
    )
  })

  it('offers Clear only once a filter is active, and resets everything', async () => {
    renderPanel()

    await screen.findByLabelText('From')
    expect(screen.queryByRole('button', { name: /Clear/ })).not.toBeInTheDocument()

    fireEvent.change(screen.getByLabelText('From'), { target: { value: '2026-08-01' } })

    const clear = await screen.findByRole('button', { name: /Clear/ })
    fireEvent.click(clear)

    expect(screen.getByLabelText('From')).toHaveValue('')
    await waitFor(() =>
      expect(mockGetAuditLog).toHaveBeenLastCalledWith(1, 50, {
        action: '',
        search: '',
        dateFrom: '',
        dateTo: '',
      }),
    )
  })

  it('keeps the filter bar and explains the empty result when filters match nothing', async () => {
    // Unfiltered the log has entries; the filtered query returns none. That is
    // the branch that must NOT collapse to "No audit log entries yet.", or the
    // user loses the filter bar and cannot undo their own filter.
    mockGetAuditLog.mockImplementation((_page: number, _size: number, filters: { dateFrom: string }) =>
      Promise.resolve(filters.dateFrom ? page({ entries: [], total: 0 }) : page()),
    )
    renderPanel()

    fireEvent.change(await screen.findByLabelText('From'), { target: { value: '2026-01-01' } })

    expect(await screen.findByText('No entries match these filters.')).toBeInTheDocument()
    expect(screen.queryByText('No audit log entries yet.')).not.toBeInTheDocument()
    // The filter bar survives, so Clear is reachable.
    expect(screen.getByRole('button', { name: /Clear/ })).toBeInTheDocument()
  })
})

describe('AuditLogContent — pagination', () => {
  it('hides the pager for a single page', async () => {
    renderPanel()

    await screen.findByText('abcdef12')
    expect(screen.queryByRole('button', { name: /Next/ })).not.toBeInTheDocument()
  })

  it('pages forward and back, and reports the visible range', async () => {
    mockGetAuditLog.mockResolvedValue(page({ total: 120 }))
    renderPanel()

    expect(await screen.findByText('Showing 1-50 of 120')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Previous/ })).toBeDisabled()

    fireEvent.click(screen.getByRole('button', { name: /Next/ }))

    await waitFor(() => expect(mockGetAuditLog).toHaveBeenLastCalledWith(2, 50, expect.anything()))
    expect(await screen.findByText('Showing 51-100 of 120')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /Previous/ }))
    await waitFor(() => expect(mockGetAuditLog).toHaveBeenLastCalledWith(1, 50, expect.anything()))
  })

  it('caps the last page range at the total and disables Next there', async () => {
    mockGetAuditLog.mockResolvedValue(page({ total: 60 }))
    renderPanel()

    fireEvent.click(await screen.findByRole('button', { name: /Next/ }))

    expect(await screen.findByText('Showing 51-60 of 60')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Next/ })).toBeDisabled()
  })

  it('returns to page 1 when a filter changes', async () => {
    mockGetAuditLog.mockResolvedValue(page({ total: 120 }))
    renderPanel()

    fireEvent.click(await screen.findByRole('button', { name: /Next/ }))
    await waitFor(() => expect(mockGetAuditLog).toHaveBeenLastCalledWith(2, 50, expect.anything()))

    fireEvent.change(screen.getByLabelText('From'), { target: { value: '2026-08-01' } })

    await waitFor(() =>
      expect(mockGetAuditLog).toHaveBeenLastCalledWith(
        1,
        50,
        expect.objectContaining({ dateFrom: '2026-08-01' }),
      ),
    )
  })

  it('renders the table inside a bordered container with four columns', async () => {
    renderPanel()

    const table = await screen.findByRole('table')
    const headers = within(table).getAllByRole('columnheader').map((h) => h.textContent)
    expect(headers).toEqual(['Timestamp', 'User', 'Action', 'Detail'])
  })
})
