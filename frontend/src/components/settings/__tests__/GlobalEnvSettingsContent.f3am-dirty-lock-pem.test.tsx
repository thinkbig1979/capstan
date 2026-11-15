/**
 * agent-os-f3am, the global env twin of agent-os-y6e8: a commented-out PEM
 * opener's body lines parse as rows keyed by their own text. The server blanks
 * those keys only in the locked payload; a lock with unsaved edits keeps the
 * unlocked rows, so the body text stayed in a key input and matched search.
 * Payloads are globalEnvResponseVars' output for
 *   PORT=8080 / #SSH_KEY="-----BEGIN PRIVATE KEY----- / MIIEbodylineAAAA /
 *   MIIEbodyline== / -----END PRIVATE KEY-----" / TZ=UTC
 * (OBSERVED from a scratch Go probe on 7ecc0c6).
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
    { key: 'MIIEbodylineAAAA', value: '', sensitive: true, readOnly: true },
    { key: 'MIIEbodyline', value: '=', sensitive: true },
    { key: 'TZ', value: 'UTC' },
  ],
}
const lockedPayload: Payload = {
  locked: true,
  vars: [
    { key: 'PORT', value: '8080' },
    { key: '', value: '', sensitive: true, readOnly: true },
    { key: '', value: '', sensitive: true },
    { key: 'TZ', value: 'UTC' },
  ],
}
const SECRETS = ['MIIEbodylineAAAA', 'MIIEbodyline']

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

const value = (n: number) => screen.getAllByLabelText(`Global env value ${n}`)[0] as HTMLInputElement
const keyInputs = () => screen.queryAllByLabelText(/^Global env key \d+$/) as HTMLInputElement[]
const keyValues = () => keyInputs().map((el) => el.value)

async function serve(next: Payload) {
  server = next
  const calls = mockGetGlobalEnv.mock.calls.length
  await act(async () => {
    await client.invalidateQueries({ queryKey: queryKeys.settings.globalEnv() })
  })
  await waitFor(() => expect(mockGetGlobalEnv.mock.calls.length).toBeGreaterThan(calls))
}

async function lockWith(locked: Payload, dirty: boolean) {
  act(() => useEnvUnlockStore.getState().unlock('tok'))
  client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  render(
    <QueryClientProvider client={client}>
      <GlobalEnvSettingsContent />
    </QueryClientProvider>,
  )
  await waitFor(() => expect(value(1).value).toBe('8080'))
  expect(keyValues()).toContain('MIIEbodylineAAAA') // positive side: shown while unlocked
  if (dirty) fireEvent.change(value(1), { target: { value: '9090' } })
  act(() => useEnvUnlockStore.getState().lock())
  await serve(locked)
  expect(await screen.findByText('Locked')).toBeInTheDocument()
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

describe('global env: a dirty lock with a commented-out PEM (agent-os-f3am)', () => {
  it.each([
    ['dirty', true],
    ['clean (control)', false],
  ])('%s lock: no body text in a key input, and search does not match it', async (_name, dirty) => {
    await lockWith(lockedPayload, dirty)
    if (dirty) expect(value(1).value).toBe('9090') // the edit is kept

    for (const secret of SECRETS) {
      expect(keyValues().filter((k) => k.includes(secret))).toEqual([])
    }
    const search = screen.getByPlaceholderText('Filter by key or value…')
    fireEvent.change(search, { target: { value: 'MIIEbody' } })
    expect(keyInputs()).toHaveLength(0)
    fireEvent.change(search, { target: { value: 'TZ' } })
    expect(keyInputs().length).toBeGreaterThan(0)
  })

  it('a hidden key cannot be typed over; after unlock and reveal it shows and is editable again', async () => {
    await lockWith(lockedPayload, true)
    const key3 = () => screen.getAllByLabelText('Global env key 3') as HTMLInputElement[]
    expect(key3().map((el) => [el.value, el.disabled])).toEqual([
      ['', true],
      ['', true],
    ])

    act(() => useEnvUnlockStore.getState().unlock('tok'))
    await serve(unlockedPayload)
    await waitFor(() => expect(screen.queryByText('Locked')).toBeNull())
    fireEvent.click(screen.getAllByRole('button', { name: 'Show value 3' })[0])
    expect(key3().map((el) => [el.value, el.disabled])).toEqual([
      ['MIIEbodyline', false],
      ['MIIEbodyline', false],
    ])
  })

  it('the file changed during the lock: every row hides its key', async () => {
    await lockWith({ ...lockedPayload, vars: [...lockedPayload.vars, { key: 'NEW', value: 'x' }] }, true)
    expect(keyValues().filter((k) => k !== '')).toEqual([])
  })

  it.each([
    ['dirty', true],
    ['clean', false],
  ])('%s lock whose refetch fails: no body text in a key input or search', async (_name, dirty) => {
    act(() => useEnvUnlockStore.getState().unlock('tok'))
    client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
    render(
      <QueryClientProvider client={client}>
        <GlobalEnvSettingsContent />
      </QueryClientProvider>,
    )
    await waitFor(() => expect(value(1).value).toBe('8080'))
    expect(keyValues()).toContain('MIIEbodylineAAAA')
    if (dirty) fireEvent.change(value(1), { target: { value: '9090' } })
    mockGetGlobalEnv.mockImplementation(() => Promise.reject(new Error('offline')))
    act(() => useEnvUnlockStore.getState().lock())
    await act(async () => {
      await client.invalidateQueries({ queryKey: queryKeys.settings.globalEnv() })
    })
    await waitFor(() => expect(mockGetGlobalEnv).toHaveBeenCalledTimes(2))

    for (const secret of SECRETS) {
      expect(keyValues().filter((k) => k.includes(secret))).toEqual([])
    }
    expect(keyValues()).toContain('TZ') // a harmless key still shows
    const search = screen.getByPlaceholderText('Filter by key or value…')
    fireEvent.change(search, { target: { value: 'MIIEbody' } })
    expect(keyInputs()).toHaveLength(0)
  })
})
