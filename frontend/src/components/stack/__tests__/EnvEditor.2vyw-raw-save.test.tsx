/**
 * agent-os-2vyw: a row edited blind while locked refuses the TABLE save after
 * the unlock (agent-os-yt2f). The Raw save never reads table rows, so it cannot
 * carry that edit: it must not be refused for it. The refusal for an unlock
 * that could not be lined up stays on both saves (the raw text was not refilled
 * then), and the table save stays refused after a raw save.
 *
 * The payloads are the aaa fixtures of EnvEditor.yt2f-unlock.test.tsx
 * (redactEnvResponse over parseEnvFile).
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { screen, waitFor, fireEvent, act } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithProviders } from '../../../test/utils'
import { useEnvUnlockStore } from '@/stores/envUnlockStore'
import { useAuthStore } from '@/stores/authStore'
import { queryKeys } from '@/lib/query-keys'

const mockGetEnv = vi.fn()
const mockUpdateEnv = vi.fn()

vi.mock('@/lib/api', () => ({
  stacksApi: {
    getEnv: (...args: unknown[]) => mockGetEnv(...args),
    updateEnv: (...args: unknown[]) => mockUpdateEnv(...args),
    createEnv: vi.fn(),
    updateComposeAndEnv: vi.fn(),
  },
  authApi: { verifyPassword: vi.fn() },
  apiClient: { get: vi.fn(), put: vi.fn(), post: vi.fn() },
}))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), warning: vi.fn(), info: vi.fn() },
}))

import { EnvEditor } from '../EnvEditor'

function storeFollows<T extends object>(payload: T): T {
  const locked = 'locked' in payload && payload.locked === true
  const store = useEnvUnlockStore.getState()
  if (locked && store.unlockedUntil !== null) store.lock()
  else if (!locked && store.unlockedUntil === null) store.unlock('tok')
  return payload
}

// PORT=8080 / SSH_KEY="-----BEGIN / AAA=bbb / -----END" / TZ=UTC
const RAW = 'PORT=8080\nSSH_KEY="-----BEGIN\nAAA=bbb\n-----END"\nTZ=UTC\n'
const aaaUnlocked = {"hasEnvFile":true,"filename":".env","entries":[{"key":"PORT","value":"8080","line":1,"sensitive":false},{"key":"SSH_KEY","value":"\"-----BEGIN","line":2,"sensitive":true},{"key":"AAA","value":"bbb","line":3,"sensitive":false},{"key":"","value":"-----END\"","line":4,"sensitive":false,"comment":true},{"key":"TZ","value":"UTC","line":5,"sensitive":false}],"raw":RAW}
const aaaLocked = {"hasEnvFile":true,"filename":".env","entries":[{"key":"PORT","value":"8080","line":1,"sensitive":false},{"key":"SSH_KEY","value":"","line":2,"sensitive":true},{"key":"","value":"","line":3,"sensitive":true},{"key":"","value":"","line":4,"sensitive":true,"comment":true},{"key":"TZ","value":"UTC","line":5,"sensitive":false}],"locked":true}
// The same file with a row appended while locked: it cannot be lined up.
const aaaPlusRowUnlocked = {
  ...aaaUnlocked,
  entries: [...aaaUnlocked.entries, { key: 'NEW', value: '1', line: 6, sensitive: false }],
  raw: RAW + 'NEW=1\n',
}

type Payload = typeof aaaUnlocked | typeof aaaLocked | typeof aaaPlusRowUnlocked
let server: Payload
let queryClient: ReturnType<typeof renderWithProviders>['queryClient']

const input = (n: number) => screen.getAllByLabelText(`Environment variable value ${n}`)[0] as HTMLInputElement
const saves = () => screen.getAllByRole('button', { name: /^Save$/ }) as HTMLButtonElement[]
const tableSave = () => saves()[0]
const rawSave = () => saves().at(-1) as HTMLButtonElement
const rawText = () => screen.getByPlaceholderText('KEY=value') as HTMLTextAreaElement

async function serve(next: Payload) {
  server = next
  const calls = mockGetEnv.mock.calls.length
  await act(async () => {
    await queryClient.invalidateQueries({ queryKey: queryKeys.stack.env('s1') })
  })
  await waitFor(() => expect(mockGetEnv.mock.calls.length).toBeGreaterThan(calls))
}

async function afterATick() {
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 20))
  })
}

/** Locked from the first load, a value typed into the hidden body row, then unlocked. */
async function blindEditThenUnlock(unlocked: Payload, rawShown: string) {
  server = aaaLocked
  ;({ queryClient } = renderWithProviders(<EnvEditor stackId="s1" />))
  await waitFor(() => expect(input(1).value).toBe('8080'))
  fireEvent.change(input(3), { target: { value: 'typed' } })
  await serve(unlocked)
  expect(await screen.findByRole('alert')).toHaveTextContent(/can't be saved/)
  await userEvent.setup().click(screen.getByRole('tab', { name: 'Raw Editor' }))
  await waitFor(() => expect(rawText().value).toBe(rawShown))
}

beforeEach(() => {
  vi.clearAllMocks()
  act(() => useEnvUnlockStore.getState().lock())
  useAuthStore.setState({ authDisabled: false })
  mockGetEnv.mockImplementation(() => Promise.resolve(storeFollows(server)))
  mockUpdateEnv.mockResolvedValue({ outcome: 'success', reason: 'Saved' })
})

afterEach(() => {
  act(() => useEnvUnlockStore.getState().lock())
})

describe('EnvEditor: the Raw save is not refused for a blind table edit (agent-os-2vyw)', () => {
  it('2vyw: with a blind-edit refusal on the table, the Raw save writes the raw text', async () => {
    await blindEditThenUnlock(aaaUnlocked, RAW)
    fireEvent.change(rawText(), { target: { value: RAW + 'EXTRA=1\n' } })

    expect(rawSave()).not.toBeDisabled()
    fireEvent.click(rawSave())
    await waitFor(() => expect(mockUpdateEnv).toHaveBeenCalledTimes(1))
    expect(mockUpdateEnv.mock.calls[0][1]).toEqual({ raw: RAW + 'EXTRA=1\n' })
  })

  it('2vyw: an unlock that could not be lined up still refuses the Raw save', async () => {
    // Nothing was refilled: the raw text is still the locked view's empty one,
    // and a save of it would empty the file.
    await blindEditThenUnlock(aaaPlusRowUnlocked, '')
    fireEvent.change(rawText(), { target: { value: 'X=1\n' } })

    expect(rawSave()).toBeDisabled()
    fireEvent.click(rawSave())
    await afterATick()
    expect(mockUpdateEnv).not.toHaveBeenCalled()
  })

  it('2vyw: a Raw save that lands while the user kept typing leaves the table refusal on', async () => {
    let resolveSave: (v: unknown) => void = () => {}
    mockUpdateEnv.mockImplementation(() => new Promise((resolve) => (resolveSave = resolve)))
    await blindEditThenUnlock(aaaUnlocked, RAW)
    const saved = RAW + 'EXTRA=1\n'
    fireEvent.change(rawText(), { target: { value: saved } })
    fireEvent.click(rawSave())
    await waitFor(() => expect(mockUpdateEnv).toHaveBeenCalledTimes(1))
    // Typing lands while the save is in flight, so the editor stays dirty and
    // the save's refetch is taken as its own.
    fireEvent.change(rawText(), { target: { value: saved + 'MORE=2\n' } })
    await act(async () => resolveSave({ outcome: 'success', reason: 'Saved' }))
    await serve({ ...aaaUnlocked, raw: saved })

    await userEvent.setup().click(screen.getByRole('tab', { name: 'Table View' }))
    expect(tableSave()).toBeDisabled()
    fireEvent.click(tableSave())
    await afterATick()
    expect(mockUpdateEnv).toHaveBeenCalledTimes(1)
  })
})
