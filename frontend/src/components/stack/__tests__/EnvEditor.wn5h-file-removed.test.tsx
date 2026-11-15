/**
 * agent-os-wn5h item 1: a stack's env file goes away on disk (a git pull
 * deletes .env and the rescan drops it, so the payload says hasEnvFile: false)
 * while the editor holds unsaved edits. The editor used to hydrate the no-file
 * payload: the edits vanished behind "No environment file", and Create reset
 * them. They must stay on screen with a notice, and Save must be refused (the
 * backend has no file to write: PUT answers 404), until Reload.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { screen, waitFor, fireEvent, act } from '@testing-library/react'
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

const FILE = {"hasEnvFile":true,"filename":".env","entries":[{"key":"PORT","value":"8080","line":1,"sensitive":false},{"key":"TZ","value":"UTC","line":2,"sensitive":false}],"raw":"PORT=8080\nTZ=UTC\n"}
const NO_FILE = { hasEnvFile: false }

type Payload = typeof FILE | typeof NO_FILE
let server: Payload
let queryClient: ReturnType<typeof renderWithProviders>['queryClient']

const input = (n: number) => screen.getAllByLabelText(`Environment variable value ${n}`)[0] as HTMLInputElement
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

beforeEach(() => {
  vi.clearAllMocks()
  act(() => useEnvUnlockStore.getState().unlock('tok'))
  useAuthStore.setState({ authDisabled: false })
  server = FILE
  mockGetEnv.mockImplementation(() => Promise.resolve(server))
  mockUpdateEnv.mockResolvedValue({ outcome: 'success', reason: 'Saved' })
})

afterEach(() => {
  act(() => useEnvUnlockStore.getState().lock())
})

describe('EnvEditor: the env file removed on disk under unsaved edits (agent-os-wn5h item 1)', () => {
  it('wn5h: the edits stay with a notice, Save is refused, Reload shows the no-file state', async () => {
    ;({ queryClient } = renderWithProviders(<EnvEditor stackId="s1" />))
    await waitFor(() => expect(input(1).value).toBe('8080'))
    fireEvent.change(input(1), { target: { value: '9090' } })

    await serve(NO_FILE)
    await afterATick()

    expect(input(1).value).toBe('9090')
    expect(screen.getByRole('alert')).toHaveTextContent(/removed/)
    expect(saveButton()).toBeDisabled()
    fireEvent.click(saveButton())
    await afterATick()
    expect(mockUpdateEnv).not.toHaveBeenCalled()

    fireEvent.click(screen.getByRole('button', { name: 'Reload' }))
    await waitFor(() => expect(screen.queryByLabelText('Environment variable value 1')).not.toBeInTheDocument())
  })

  it('wn5h: control: a clean editor shows the no-file state at once', async () => {
    ;({ queryClient } = renderWithProviders(<EnvEditor stackId="s1" />))
    await waitFor(() => expect(input(1).value).toBe('8080'))

    await serve(NO_FILE)

    await waitFor(() => expect(screen.queryByLabelText('Environment variable value 1')).not.toBeInTheDocument())
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })
})
