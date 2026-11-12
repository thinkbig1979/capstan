/**
 * agent-os-vqf5, global env half: a lock change must not replace what the user
 * is typing, Save stays refused while locked, and after the unlock a secret row
 * holds the real value, never the redacted blank. Notices are asserted by role.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { GlobalEnvSettingsContent } from '../GlobalEnvSettingsContent'
import { useEnvUnlockStore } from '@/stores/envUnlockStore'
import { useAuthStore } from '@/stores/authStore'
import { queryKeys } from '@/lib/query-keys'

type Payload = { vars: Array<{ key: string; value: string }>; locked?: boolean }

const lockedPayload: Payload = {
  locked: true,
  vars: [
    { key: 'FOO', value: 'bar' },
    { key: 'API_KEY', value: '' },
  ],
}
const unlockedPayload: Payload = {
  vars: [
    { key: 'FOO', value: 'bar' },
    { key: 'API_KEY', value: 'real-secret' },
  ],
}

let server: Payload
let client: QueryClient
const mockGetGlobalEnv = vi.fn()
const mockUpdateGlobalEnv = vi.fn()

vi.mock('@/lib/api', () => ({
  settingsApi: {
    getGlobalEnv: (...args: unknown[]) => mockGetGlobalEnv(...args),
    updateGlobalEnv: (...args: unknown[]) => mockUpdateGlobalEnv(...args),
  },
  authApi: { verifyPassword: vi.fn() },
}))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), warning: vi.fn(), info: vi.fn() },
}))

function renderForm() {
  client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <GlobalEnvSettingsContent />
    </QueryClientProvider>,
  )
}

const value = (n: number) => screen.getAllByLabelText(`Global env value ${n}`)[0] as HTMLInputElement

async function serve(next: Payload) {
  server = next
  const calls = mockGetGlobalEnv.mock.calls.length
  await act(async () => {
    await client.invalidateQueries({ queryKey: queryKeys.settings.globalEnv() })
  })
  await waitFor(() => expect(mockGetGlobalEnv.mock.calls.length).toBeGreaterThan(calls))
}

async function lockNow() {
  act(() => useEnvUnlockStore.getState().lock())
  await serve(lockedPayload)
}

beforeEach(() => {
  vi.clearAllMocks()
  act(() => useEnvUnlockStore.getState().lock())
  useAuthStore.setState({ authDisabled: false })
  server = unlockedPayload
  mockGetGlobalEnv.mockImplementation(() => Promise.resolve(server))
  mockUpdateGlobalEnv.mockResolvedValue(undefined)
})

describe('global env form across a lock change (agent-os-vqf5)', () => {
  it('vqf5: unlock expiry keeps the unsaved edit, shows a locked-again alert, and Save stays refused', async () => {
    renderForm()
    await waitFor(() => expect(value(1).value).toBe('bar'))
    fireEvent.change(value(1), { target: { value: 'typing' } })

    await lockNow()

    expect(await screen.findByRole('alert')).toHaveTextContent(/Unlock/)
    expect(value(1).value).toBe('typing')
    expect(screen.getByText('Save').closest('button')).toBeDisabled()
    expect(mockUpdateGlobalEnv).not.toHaveBeenCalled()
  })

  it('vqf5: edit, lock, unlock: the edit is kept and the secret holds the real value, never blank', async () => {
    renderForm()
    await waitFor(() => expect(value(1).value).toBe('bar'))
    fireEvent.change(value(1), { target: { value: 'typing' } })
    await lockNow()
    await screen.findByRole('alert')

    await serve(unlockedPayload)

    await waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument())
    expect(value(1).value).toBe('typing')
    expect(value(2).value).toBe('real-secret')

    fireEvent.click(screen.getByText('Save'))
    await waitFor(() => expect(mockUpdateGlobalEnv).toHaveBeenCalledTimes(1))
    expect(mockUpdateGlobalEnv).toHaveBeenCalledWith([
      { key: 'FOO', value: 'typing' },
      { key: 'API_KEY', value: 'real-secret' },
    ])
  })

  it('vqf5: edit while locked from the first load, then unlock: a save never writes the blank', async () => {
    server = lockedPayload
    renderForm()
    await waitFor(() => expect(value(1).value).toBe('bar'))
    expect(value(2).value).toBe('')
    fireEvent.change(value(1), { target: { value: 'typing' } })
    expect(await screen.findByRole('alert')).toBeInTheDocument()

    await serve(unlockedPayload)

    await waitFor(() => expect(value(2).value).toBe('real-secret'))
    expect(value(1).value).toBe('typing')
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()

    fireEvent.click(screen.getByText('Save'))
    await waitFor(() => expect(mockUpdateGlobalEnv).toHaveBeenCalledTimes(1))
    expect(mockUpdateGlobalEnv).toHaveBeenCalledWith([
      { key: 'FOO', value: 'typing' },
      { key: 'API_KEY', value: 'real-secret' },
    ])
  })

  it('vqf5: a clean form still takes the locked payload, with no alert', async () => {
    renderForm()
    await waitFor(() => expect(value(1).value).toBe('bar'))

    await lockNow()

    await waitFor(() => expect(value(2).value).toBe(''))
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })
})
