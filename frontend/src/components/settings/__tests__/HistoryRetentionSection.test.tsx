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

  it('surfaces a failed save', async () => {
    const { toast } = await import('sonner')
    mockUpdateRetention.mockRejectedValue(new Error('boom'))
    renderSection()

    fireEvent.change(await screen.findByLabelText('Audit log'), { target: { value: '120' } })
    fireEvent.click(screen.getByRole('button', { name: /save retention/i }))

    await waitFor(() => expect(toast.error).toHaveBeenCalledWith('Failed to update retention'))
  })
})
