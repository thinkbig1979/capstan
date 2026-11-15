/**
 * agent-os-ocnh: a row edited blind while locked keeps Save refused after the
 * unlock (agent-os-yt2f) until it holds its real content again. An edit the user
 * makes to that row once its value is in view is an informed one, and clears
 * the refusal for that row. An edit made while the value is still masked does
 * not (fail closed), and neither does an Undo back to the blind edit.
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
type Payload = typeof aaaUnlocked | typeof aaaLocked
let server: Payload
let queryClient: ReturnType<typeof renderWithProviders>['queryClient']

const input = (n: number) => screen.getAllByLabelText(`Environment variable value ${n}`)[0] as HTMLInputElement
const keyInput = (n: number) => screen.getAllByLabelText(`Environment variable key ${n}`)[0] as HTMLInputElement
const reveal = (n: number) => fireEvent.click(screen.getAllByLabelText(`Toggle visibility for entry ${n}`)[0])
const saveButton = () => screen.getAllByRole('button', { name: /^Save$/ })[0] as HTMLButtonElement

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

async function expectSaveRefused() {
  expect(await screen.findByRole('alert')).toHaveTextContent(/can't be saved/)
  expect(saveButton()).toBeDisabled()
  fireEvent.click(saveButton())
  await afterATick()
  expect(mockUpdateEnv).not.toHaveBeenCalled()
}

/** Locked from the first load, a key typed into the hidden body row, then unlocked: refused. */
async function blindEditThenUnlock() {
  server = aaaLocked
  ;({ queryClient } = renderWithProviders(<EnvEditor stackId="s1" />))
  await waitFor(() => expect(input(1).value).toBe('8080'))
  fireEvent.change(keyInput(3), { target: { value: 'FOO' } })
  await serve(aaaUnlocked)
  await expectSaveRefused()
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

describe('EnvEditor: an informed edit lifts the blind-edit refusal for its row (agent-os-ocnh)', () => {
  it('ocnh: the row edited again with its value in view saves as typed', async () => {
    await blindEditThenUnlock()
    reveal(3)
    await waitFor(() => expect(input(3).type).toBe('text'))
    fireEvent.change(input(3), { target: { value: 'chosen' } })

    await waitFor(() => expect(saveButton()).not.toBeDisabled())
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    fireEvent.click(saveButton())
    await waitFor(() => expect(mockUpdateEnv).toHaveBeenCalledTimes(1))
    const row3 = (mockUpdateEnv.mock.calls[0][1] as { entries: { key: string; value: string }[] }).entries[2]
    expect([row3.key, row3.value]).toEqual(['FOO', 'chosen'])
  })

  it('ocnh: an edit while the value is still masked keeps Save refused', async () => {
    await blindEditThenUnlock()
    expect(input(3).type).toBe('password')
    fireEvent.change(input(3), { target: { value: 'chosen' } })

    await expectSaveRefused()
  })

  it('ocnh: a rename while the value is still masked keeps Save refused', async () => {
    await blindEditThenUnlock()
    fireEvent.change(keyInput(3), { target: { value: 'BAR' } })

    await expectSaveRefused()
  })

  it('ocnh: Undo of the informed edit back to the blind one refuses Save again', async () => {
    await blindEditThenUnlock()
    reveal(3)
    await waitFor(() => expect(input(3).type).toBe('text'))
    fireEvent.change(input(3), { target: { value: 'chosen' } })
    await waitFor(() => expect(saveButton()).not.toBeDisabled())

    await userEvent.setup().keyboard('{Control>}z{/Control}')
    await waitFor(() => expect(input(3).value).toBe(''))
    await expectSaveRefused()
  })

  // Review F3: the lock window. The lock (here an expiry) re-masks every row
  // that was ever masked in the same render that turns lockedNow on
  // (useEnvUnlockRemask), so no edit can reach a blind row while it shows its
  // value and the session is locked. This pins that: typing after the lock is
  // a masked edit, and the next unlock still refuses.
  it('ocnh: a value typed after the session locks is not informed, even if the row was revealed', async () => {
    await blindEditThenUnlock()
    reveal(3)
    await waitFor(() => expect(input(3).type).toBe('text'))

    act(() => useEnvUnlockStore.getState().lock())
    expect(input(3).type).toBe('password')
    fireEvent.change(input(3), { target: { value: 'chosen' } })
    act(() => useEnvUnlockStore.getState().unlock('tok'))

    await expectSaveRefused()
  })
})
