/**
 * agent-os-3ajm: while env values are locked the server sends audit entries
 * without their details (detailsHidden) and refuses a search, which reads the
 * details. The panel says "Unlock to see details" instead of a bare "—", and
 * offers no search box; unlocked, both are as before.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { useAuthStore } from '@/stores/authStore'
import { useEnvUnlockStore } from '@/stores/envUnlockStore'

const mockGetAuditLog = vi.fn()

vi.mock('@/lib/api', () => ({
  settingsApi: {
    getAuditLog: (...args: unknown[]) => mockGetAuditLog(...args),
  },
}))

vi.mock('../HistoryRetentionSection', () => ({
  HistoryRetentionSection: () => <div data-testid="retention-section" />,
}))

import { AuditLogContent } from '../AuditLogContent'

const row = { id: 'log-1', userId: 'u', stackId: 's', action: 'git_commit', createdAt: '2026-03-01T10:00:00Z' }

function renderPanel() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  )
  return render(<AuditLogContent />, { wrapper })
}

describe('AuditLogContent while locked (agent-os-3ajm)', () => {
  beforeEach(() => {
    mockGetAuditLog.mockReset()
    useAuthStore.setState({ authDisabled: false })
    useEnvUnlockStore.getState().lock()
  })

  it('says the details need an unlock and offers no search', async () => {
    mockGetAuditLog.mockResolvedValue({ entries: [{ ...row, detail: '' }], total: 1, page: 1, pageSize: 50, detailsHidden: true })
    renderPanel()

    expect(await screen.findByText('Unlock to see details')).toBeInTheDocument()
    expect(screen.queryByPlaceholderText('Search detail or action')).toBeNull()
    for (const call of mockGetAuditLog.mock.calls) {
      expect(call[2]?.search ?? '').toBe('')
    }
  })

  it('a search typed unlocked is not sent once the session locks (review R2-2)', async () => {
    useEnvUnlockStore.getState().unlock('tok')
    mockGetAuditLog.mockResolvedValue({ entries: [{ ...row, detail: '' }], total: 1, page: 1, pageSize: 50 })
    renderPanel()

    fireEvent.change(await screen.findByPlaceholderText('Search detail or action'), { target: { value: 'guess' } })
    await waitFor(() => expect(mockGetAuditLog.mock.calls.at(-1)?.[2]?.search).toBe('guess'))

    act(() => useEnvUnlockStore.getState().lock())
    // The latest request, the one the locked view reads, carries no search.
    await waitFor(() => expect(mockGetAuditLog.mock.calls.at(-1)?.[2]?.search ?? '').toBe(''))
  })

  it('shows details and the search box once unlocked', async () => {
    useEnvUnlockStore.getState().unlock('tok')
    mockGetAuditLog.mockResolvedValue({ entries: [{ ...row, detail: '{"commit":"abc"}' }], total: 1, page: 1, pageSize: 50 })
    renderPanel()

    expect(await screen.findByText(/commit: abc/)).toBeInTheDocument()
    expect(screen.queryByText('Unlock to see details')).toBeNull()
    expect(screen.getByPlaceholderText('Search detail or action')).toBeInTheDocument()
  })
})
