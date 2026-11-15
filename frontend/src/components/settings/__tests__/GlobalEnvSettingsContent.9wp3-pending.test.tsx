/**
 * agent-os-9wp3, the global env page: a newer list held behind the "changed"
 * notice was served unlocked. The lock drops it, so Reload cannot load it into
 * a locked page.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { GlobalEnvSettingsContent } from '../GlobalEnvSettingsContent'
import { useEnvUnlockStore } from '@/stores/envUnlockStore'
import { useAuthStore } from '@/stores/authStore'
import { queryKeys } from '@/lib/query-keys'

const mockGetGlobalEnv = vi.fn()

vi.mock('@/lib/api', () => ({
  settingsApi: {
    getGlobalEnv: (...args: unknown[]) => mockGetGlobalEnv(...args),
    updateGlobalEnv: vi.fn(),
  },
  authApi: { verifyPassword: vi.fn() },
}))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), warning: vi.fn(), info: vi.fn() },
}))

const value = (n: number) => screen.getAllByLabelText(`Global env value ${n}`)[0] as HTMLInputElement
const reloadButton = () => screen.queryByRole('button', { name: 'Reload' })

beforeEach(() => {
  vi.clearAllMocks()
  act(() => useEnvUnlockStore.getState().lock())
  useAuthStore.setState({ authDisabled: false })
})
afterEach(() => {
  act(() => useEnvUnlockStore.getState().lock())
})

describe('global env: a held list across a lock (agent-os-9wp3)', () => {
  it('the lock drops the held list; the kept edit stays', async () => {
    mockGetGlobalEnv.mockResolvedValue({ vars: [{ key: 'PORT', value: '8080' }] })
    act(() => useEnvUnlockStore.getState().unlock('tok'))
    const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
    render(
      <QueryClientProvider client={client}>
        <GlobalEnvSettingsContent />
      </QueryClientProvider>,
    )
    await waitFor(() => expect(value(1).value).toBe('8080'))
    fireEvent.change(value(1), { target: { value: '9090' } })

    mockGetGlobalEnv.mockResolvedValue({
      vars: [
        { key: 'PORT', value: '8080' },
        { key: 'NEW_TOKEN', value: 'newsecret', sensitive: true },
      ],
    })
    await act(() => client.invalidateQueries({ queryKey: queryKeys.settings.globalEnv() }))
    await waitFor(() => expect(reloadButton()).not.toBeNull()) // positive side: held

    mockGetGlobalEnv.mockRejectedValue(new Error('offline'))
    act(() => useEnvUnlockStore.getState().lock())
    await act(() => client.invalidateQueries({ queryKey: queryKeys.settings.globalEnv() }))

    expect(reloadButton()).toBeNull()
    expect(value(1).value).toBe('9090')
  })
})
