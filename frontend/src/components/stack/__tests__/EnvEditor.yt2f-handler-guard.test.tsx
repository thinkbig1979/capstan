/**
 * agent-os-yt2f review S3: while Save is refused, the save handlers refuse on
 * their own, not only through the disabled buttons. A disabled button never
 * fires its onClick, so these tests capture the handlers EnvEditor passes to the
 * table and raw views and call them directly.
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
const handlers: { onSaveTable?: () => void; onSaveRaw?: () => void } = {}

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

vi.mock('../env-editor/EnvTableView', async (importOriginal) => {
  const real = await importOriginal<typeof import('../env-editor/EnvTableView')>()
  return {
    ...real,
    EnvTableView: (props: Parameters<typeof real.EnvTableView>[0]) => {
      handlers.onSaveTable = props.onSaveTable
      return real.EnvTableView(props)
    },
  }
})

vi.mock('../env-editor/EnvRawView', async (importOriginal) => {
  const real = await importOriginal<typeof import('../env-editor/EnvRawView')>()
  return {
    ...real,
    EnvRawView: (props: Parameters<typeof real.EnvRawView>[0]) => {
      handlers.onSaveRaw = props.onSaveRaw
      return real.EnvRawView(props)
    },
  }
})

import { EnvEditor } from '../EnvEditor'

// Go output at e9e7c55 (redactEnvResponse over parseEnvFile).
const aaaUnlocked = {"hasEnvFile":true,"filename":".env","entries":[{"key":"PORT","value":"8080","line":1,"sensitive":false},{"key":"SSH_KEY","value":"\"-----BEGIN","line":2,"sensitive":true},{"key":"AAA","value":"bbb","line":3,"sensitive":false},{"key":"","value":"-----END\"","line":4,"sensitive":false,"comment":true},{"key":"TZ","value":"UTC","line":5,"sensitive":false}],"raw":"PORT=8080\nSSH_KEY=\"-----BEGIN\nAAA=bbb\n-----END\"\nTZ=UTC\n"}
const aaaLocked = {"hasEnvFile":true,"filename":".env","entries":[{"key":"PORT","value":"8080","line":1,"sensitive":false},{"key":"SSH_KEY","value":"","line":2,"sensitive":true},{"key":"","value":"","line":3,"sensitive":true},{"key":"","value":"","line":4,"sensitive":true,"comment":true},{"key":"TZ","value":"UTC","line":5,"sensitive":false}],"locked":true}
// The same file with NEW=1 appended while locked: one row more, so not aligned.
const aaaPlusRowUnlocked = {...aaaUnlocked, entries: [...aaaUnlocked.entries, {"key":"NEW","value":"1","line":6,"sensitive":false}], raw: aaaUnlocked.raw + 'NEW=1\n'}

type Payload = typeof aaaUnlocked | typeof aaaLocked
let server: Payload
let queryClient: ReturnType<typeof renderWithProviders>['queryClient']

const input = (n: number) =>
  screen.getAllByLabelText(`Environment variable value ${n}`)[0] as HTMLInputElement
const keyInput = (n: number) =>
  screen.getAllByLabelText(`Environment variable key ${n}`)[0] as HTMLInputElement

async function serve(next: Payload) {
  server = next
  const calls = mockGetEnv.mock.calls.length
  await act(async () => {
    await queryClient.invalidateQueries({ queryKey: queryKeys.stack.env('s1') })
  })
  await waitFor(() => expect(mockGetEnv.mock.calls.length).toBeGreaterThan(calls))
}

/** Calls a captured handler, then gives a mutation the tick it needs to reach the API. */
async function callThenWait(handler: (() => void) | undefined) {
  expect(handler).toBeDefined()
  await act(async () => {
    handler?.()
    await new Promise((resolve) => setTimeout(resolve, 20))
  })
}

beforeEach(() => {
  vi.clearAllMocks()
  delete handlers.onSaveTable
  delete handlers.onSaveRaw
  act(() => useEnvUnlockStore.getState().lock())
  useAuthStore.setState({ authDisabled: false })
  mockGetEnv.mockImplementation(() => Promise.resolve(server))
  mockUpdateEnv.mockResolvedValue({ outcome: 'success', reason: 'Saved' })
})

afterEach(() => {
  act(() => useEnvUnlockStore.getState().lock())
})

describe('EnvEditor save handlers refuse on their own (agent-os-yt2f review S3)', () => {
  it.each([
    ['the payloads do not line up', aaaPlusRowUnlocked, () => fireEvent.change(input(1), { target: { value: '9090' } })],
    ['a row hidden while locked was edited', aaaUnlocked, () => fireEvent.change(keyInput(3), { target: { value: 'FOO' } })],
  ] as const)('yt2f: %s: the table and raw save handlers write nothing', async (_name, unlocked, edit) => {
    server = aaaLocked
    ;({ queryClient } = renderWithProviders(<EnvEditor stackId="s1" />))
    await waitFor(() => expect(input(1).value).toBe('8080'))
    edit()
    await serve(unlocked as Payload)
    await screen.findByRole('alert')

    await callThenWait(handlers.onSaveTable)
    await userEvent.setup().click(screen.getByRole('tab', { name: 'Raw Editor' }))
    await callThenWait(handlers.onSaveRaw)

    expect(mockUpdateEnv).not.toHaveBeenCalled()
  })

  it('yt2f: control: with nothing refused the same captured handler does save', async () => {
    server = aaaLocked
    ;({ queryClient } = renderWithProviders(<EnvEditor stackId="s1" />))
    await waitFor(() => expect(input(1).value).toBe('8080'))
    fireEvent.change(input(1), { target: { value: '9090' } })
    await serve(aaaUnlocked)
    await waitFor(() => expect(screen.getAllByRole('button', { name: /^Save$/ })[0]).not.toBeDisabled())

    await callThenWait(handlers.onSaveTable)

    expect(mockUpdateEnv).toHaveBeenCalledTimes(1)
  })
})
