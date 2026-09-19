import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { HistoryRetentionSection } from '../HistoryRetentionSection'

// update_history and backup_runs previously grew without bound, and the
// retention endpoint had no caller in the UI at all (agent-os-0jp).

const mockGetRetention = vi.fn()
const mockUpdateRetention = vi.fn()

vi.mock('@/lib/api', () => ({
  settingsApi: {
    getRetention: (...args: unknown[]) => mockGetRetention(...args),
    updateRetention: (...args: unknown[]) => mockUpdateRetention(...args),
  },
}))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), info: vi.fn(), warning: vi.fn() },
}))

function createWrapper() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  )
}

const SETTINGS = {
  retentionDays: 90,
  updateHistoryRetentionDays: 45,
  backupHistoryRetentionDays: 30,
  minRetentionDays: 7,
}

function renderSection() {
  return render(<HistoryRetentionSection />, { wrapper: createWrapper() })
}

describe('HistoryRetentionSection', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockGetRetention.mockResolvedValue(SETTINGS)
    mockUpdateRetention.mockResolvedValue(undefined)
  })

  it('shows the configured retention for all three history tables', async () => {
    renderSection()

    expect(await screen.findByLabelText('Audit log')).toHaveValue(90)
    expect(screen.getByLabelText('Update history')).toHaveValue(45)
    expect(screen.getByLabelText('Backup history')).toHaveValue(30)
  })

  it('sends only the fields that changed', async () => {
    renderSection()

    const updateHistory = await screen.findByLabelText('Update history')
    fireEvent.change(updateHistory, { target: { value: '60' } })
    fireEvent.click(screen.getByRole('button', { name: /save retention/i }))

    await waitFor(() => expect(mockUpdateRetention).toHaveBeenCalledTimes(1))
    expect(mockUpdateRetention).toHaveBeenCalledWith({ updateHistoryRetentionDays: 60 })
  })

  it('disables saving until something changes', async () => {
    renderSection()

    const save = await screen.findByRole('button', { name: /save retention/i })
    expect(save).toBeDisabled()

    fireEvent.change(screen.getByLabelText('Audit log'), { target: { value: '120' } })
    expect(save).toBeEnabled()
  })

  // A prune is irreversible, so the floor is enforced server-side too. The UI
  // blocks the request rather than letting the operator get a 400 back.
  it('refuses to submit a value below the server floor', async () => {
    renderSection()

    const auditLog = await screen.findByLabelText('Audit log')
    fireEvent.change(auditLog, { target: { value: '2' } })

    expect(screen.getByRole('button', { name: /save retention/i })).toBeDisabled()
    expect(screen.getByText(/at least 7 days/i)).toBeInTheDocument()
    expect(mockUpdateRetention).not.toHaveBeenCalled()
  })

  // agent-os-r1kc. The retention endpoint now refuses (500) rather than serving
  // a fabricated 90 when the settings row cannot be READ, because the same
  // number drives three irreversible DELETEs. This component used to undo that
  // one layer up: `valueOf` fell through to a hardcoded `?? 90`, so a refused
  // request still displayed 90 as the configured retention — agreeing with a
  // prune the operator had no other way to notice.
  describe('when the retention settings cannot be read', () => {
    beforeEach(() => {
      mockGetRetention.mockRejectedValue(new Error('500 read retention setting'))
    })

    it('does not display a retention value it never received', async () => {
      renderSection()
      // The heading renders in BOTH the loaded form and the refusal state, and
      // in neither while loading — so waiting on it settles the component
      // without presupposing which branch it took.
      await screen.findByRole('heading', { name: /history retention/i })

      // The defect first, so this arm fails on it rather than on the new copy:
      // the pre-fix component fell through to a hardcoded 90 and rendered the
      // form as though the server had answered.
      expect(screen.queryByDisplayValue('90')).not.toBeInTheDocument()
      expect(screen.queryByLabelText('Audit log')).not.toBeInTheDocument()
      expect(screen.getByText(/could not be read/i)).toBeInTheDocument()
    })

    it('offers no Save, so a fabricated form cannot be written back', async () => {
      renderSection()
      await screen.findByRole('heading', { name: /history retention/i })

      expect(screen.queryByRole('button', { name: /save retention/i })).not.toBeInTheDocument()
      expect(mockUpdateRetention).not.toHaveBeenCalled()
      expect(screen.getByText(/could not be read/i)).toBeInTheDocument()
    })
  })

  // CONTROL for the two arms above: a healthy response must still render the
  // form. Otherwise "does not display 90" would be satisfied by a component that
  // renders nothing at all.
  it('still renders the form when the settings load', async () => {
    renderSection()

    expect(await screen.findByLabelText('Audit log')).toHaveValue(90)
    expect(screen.queryByText(/could not be read/i)).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: /save retention/i })).toBeInTheDocument()
  })

  it('surfaces a failed save', async () => {
    const { toast } = await import('sonner')
    mockUpdateRetention.mockRejectedValue(new Error('boom'))
    renderSection()

    fireEvent.change(await screen.findByLabelText('Audit log'), { target: { value: '120' } })
    fireEvent.click(screen.getByRole('button', { name: /save retention/i }))

    await waitFor(() => expect(toast.error).toHaveBeenCalledWith('Failed to update retention'))
  })
})

/**
 * agent-os-zlw0. UpdateLogRetention (backend/internal/handlers/settings.go:456)
 * answers with THREE distinct VALIDATION_ERROR 400s — bad body, a value below
 * MinRetentionDays, and no field supplied — and the zero-arity onError rendered
 * one sentence for all three.
 */
describe('HistoryRetentionSection — why the save failed', () => {
  // The healthy-read setup lives in the sibling describe's beforeEach, so it is
  // repeated here: without it getRetention resolves undefined and the component
  // renders its refusal state, which has no Save button to press.
  beforeEach(() => {
    vi.clearAllMocks()
    mockGetRetention.mockResolvedValue(SETTINGS)
  })

  it('names what the server rejected', async () => {
    const { toast } = await import('sonner')
    mockUpdateRetention.mockRejectedValue({
      error: 'Bad Request',
      code: 'VALIDATION_ERROR',
      message: 'Retention days must be at least 7',
      status: 400,
    })
    renderSection()

    fireEvent.change(await screen.findByLabelText('Audit log'), { target: { value: '120' } })
    fireEvent.click(screen.getByRole('button', { name: /save retention/i }))

    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith('Failed to update retention', {
        description: 'Retention days must be at least 7',
      }),
    )
  })

  // MUST-PASS side, same instrument: axios's own "Network Error" is not a
  // backend sentence and must not be presented as one (lib/api.ts:131).
  it('shows the generic sentence alone when the failure carries no server cause', async () => {
    const { toast } = await import('sonner')
    mockUpdateRetention.mockRejectedValue({
      error: 'Unknown error',
      code: 'ERR_NETWORK',
      message: 'Network Error',
    })
    renderSection()

    fireEvent.change(await screen.findByLabelText('Audit log'), { target: { value: '120' } })
    fireEvent.click(screen.getByRole('button', { name: /save retention/i }))

    await waitFor(() => expect(toast.error).toHaveBeenCalled())
    const call = vi.mocked(toast.error).mock.calls.find((c) => c[0] === 'Failed to update retention')
    expect(call).toBeDefined()
    expect(call?.[1]).toBeUndefined()
  })
})

/**
 * agent-os-wczm. `isError || !data` is true for a REFETCH failure as well as a
 * first-load failure, so one 500 on a focus refetch blanked a populated
 * write-back form. r1kc forbids FABRICATING a value; a value the server really
 * sent is not fabricated, and `!data` keeps every refusal r1kc asked for.
 *
 * The arm resolves first, edits, then rejects a refetch — a first-fetch-rejects
 * fixture leaves `data` undefined and is blind to the difference.
 */
describe('HistoryRetentionSection — a failed REFETCH must not discard the form', () => {
  it('keeps the populated form and the unsaved edit when a REFETCH fails', async () => {
    mockGetRetention.mockResolvedValue(SETTINGS)
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    })
    render(
      <QueryClientProvider client={queryClient}>
        <HistoryRetentionSection />
      </QueryClientProvider>,
    )

    const auditLog = await screen.findByLabelText('Audit log')
    fireEvent.change(auditLog, { target: { value: '120' } })
    expect(auditLog).toHaveValue(120)

    mockGetRetention.mockRejectedValue(new Error('boom'))
    await queryClient.refetchQueries({ queryKey: ['settings', 'retention'] })

    await waitFor(() =>
      expect(queryClient.getQueryState(['settings', 'retention'])?.status).toBe('error'),
    )
    expect(screen.getByLabelText('Audit log')).toHaveValue(120)
    expect(screen.getByLabelText('Update history')).toHaveValue(45)
    expect(
      screen.queryByText(/The configured retention could not be read/),
    ).not.toBeInTheDocument()
    // The WRITE-BACK variant of RefreshFailedNotice: `beforeSave` adds the save
    // warning, and this arm pins the whole sentence rather than the shared
    // prefix — dropping the beforeSave branch would leave "…the server sent."
    // and fail here. The read-only variant is pinned in BackupHistoryTab.test.tsx.
    expect(
      screen.getByText(/Could not refresh the retention settings\. The values shown are the last ones the server sent, so check them before saving\./),
    ).toBeInTheDocument()
  })
})
