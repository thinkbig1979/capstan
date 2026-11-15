/**
 * agent-os-9fsu F10, global page: the value input of a row whose key is hidden
 * stays editable while locked. On an unlocked baseline the typed text replaces
 * the real value at the next save after unlock, and the user never saw either.
 * Fixtures are the f3am ones (a commented-out PEM's body line that parsed as a
 * variable). Two lock windows hide the key: the locked payload's blank key (a
 * dirty lock), and the stale unlocked payload before the refetch lands, or when
 * it fails (staleUnlocked).
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { GlobalEnvSettingsContent } from '../GlobalEnvSettingsContent'
import { useEnvUnlockStore } from '@/stores/envUnlockStore'
import { useAuthStore } from '@/stores/authStore'
import { queryKeys } from '@/lib/query-keys'

type Var = { key: string; value: string; sensitive?: boolean; readOnly?: boolean }
type Payload = { vars: Var[]; locked?: boolean }

const unlockedPayload: Payload = {
  vars: [
    { key: 'PORT', value: '8080' },
    { key: 'MIIEbodyline', value: '=', sensitive: true },
    { key: 'TZ', value: 'UTC' },
  ],
}
const lockedPayload: Payload = {
  locked: true,
  vars: [
    { key: 'PORT', value: '8080' },
    { key: '', value: '', sensitive: true },
    { key: 'TZ', value: 'UTC' },
  ],
}

let server: Payload | Error
let client: QueryClient
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

const values = (n: number) => screen.getAllByLabelText(`Global env value ${n}`) as HTMLInputElement[]
const keys = (n: number) => screen.getAllByLabelText(`Global env key ${n}`) as HTMLInputElement[]

async function lockWith(next: Payload | Error, dirty: boolean) {
  act(() => useEnvUnlockStore.getState().unlock('tok'))
  client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  render(
    <QueryClientProvider client={client}>
      <GlobalEnvSettingsContent />
    </QueryClientProvider>,
  )
  await waitFor(() => expect(values(1)[0].value).toBe('8080'))
  // Positive side: shown and editable while unlocked.
  expect(keys(2)[0].value).toBe('MIIEbodyline')
  expect(values(2)[0].disabled).toBe(false)
  if (dirty) fireEvent.change(values(1)[0], { target: { value: '9090' } })
  act(() => useEnvUnlockStore.getState().lock())
  server = next
  const calls = mockGetGlobalEnv.mock.calls.length
  await act(async () => {
    await client.invalidateQueries({ queryKey: queryKeys.settings.globalEnv() })
  })
  await waitFor(() => expect(mockGetGlobalEnv.mock.calls.length).toBeGreaterThan(calls))
}

beforeEach(() => {
  vi.clearAllMocks()
  act(() => useEnvUnlockStore.getState().lock())
  useAuthStore.setState({ authDisabled: false })
  server = unlockedPayload
  mockGetGlobalEnv.mockImplementation(() => (server instanceof Error ? Promise.reject(server) : Promise.resolve(server)))
})

afterEach(() => {
  act(() => useEnvUnlockStore.getState().lock())
})

describe('global env: the value of a key-hidden row while locked (agent-os-9fsu F10)', () => {
  it('dirty lock, locked payload: the key and the value are both disabled, in the table and the cards', async () => {
    await lockWith(lockedPayload, true)
    await screen.findByText('Locked')

    expect(keys(2).map((el) => [el.value, el.disabled])).toEqual([['', true], ['', true]])
    expect(values(2).map((el) => el.disabled)).toEqual([true, true])
    // Positive side: a plain row stays editable.
    expect(values(1).map((el) => el.disabled)).toEqual([false, false])
    expect(values(3).map((el) => el.disabled)).toEqual([false, false])
  })

  it('lock, refetch failed: the stale payload hides the key, and the value is disabled too', async () => {
    await lockWith(new Error('refetch failed'), false)

    await waitFor(() => expect(keys(2).map((el) => el.value)).toEqual(['', '']))
    expect(values(2).map((el) => el.disabled)).toEqual([true, true])
    expect(values(1).map((el) => el.disabled)).toEqual([false, false])
  })
})
