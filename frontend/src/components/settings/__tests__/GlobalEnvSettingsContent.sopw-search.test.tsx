/**
 * agent-os-sopw (probe, the global env twin of agent-os-kxag): the page search
 * reads every row's value. With a dirty form kept across a lock the rows still
 * hold real values, so a filter could tell a locked user whether a hidden value
 * contains a substring. Setup copied from GlobalEnvSettingsContent.vqf5.test.tsx.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
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

afterEach(() => {
  act(() => useEnvUnlockStore.getState().lock())
})

const keyCount = () => screen.queryAllByLabelText(/^Global env key \d+$/).length

describe('global env search over hidden values (agent-os-sopw)', () => {
  it('locked with a dirty form: a substring of a hidden value does not match; keys still do', async () => {
    act(() => useEnvUnlockStore.getState().unlock('tok'))
    renderForm()
    await waitFor(() => expect(value(2).value).toBe('real-secret'))
    expect(value(2).type).toBe('password')
    fireEvent.change(value(1), { target: { value: 'typing' } }) // dirty, so the lock keeps the rows

    await lockNow()
    expect(await screen.findByRole('alert')).toHaveTextContent(/Unlock/)
    expect(value(2).value).toBe('real-secret')
    expect(value(2).type).toBe('password')

    const search = screen.getByPlaceholderText('Filter by key or value…')
    fireEvent.change(search, { target: { value: 'zzz' } })
    expect(keyCount()).toBe(0) // the filter hides rows

    fireEvent.change(search, { target: { value: 'real-sec' } })
    expect(keyCount()).toBe(0)

    fireEvent.change(search, { target: { value: 'API_K' } })
    expect(keyCount()).toBeGreaterThan(0)
  })

  it('unlocked: a revealed value is searchable, the same value hidden again is not', async () => {
    act(() => useEnvUnlockStore.getState().unlock('tok'))
    renderForm()
    await waitFor(() => expect(value(2).value).toBe('real-secret'))
    const search = screen.getByPlaceholderText('Filter by key or value…')

    fireEvent.click(screen.getAllByRole('button', { name: 'Show value 2' })[0])
    expect(value(2).type).toBe('text')
    fireEvent.change(search, { target: { value: 'real-sec' } })
    expect(keyCount()).toBeGreaterThan(0)

    fireEvent.click(screen.getAllByRole('button', { name: 'Hide value 2' })[0])
    expect(keyCount()).toBe(0)
  })
})
