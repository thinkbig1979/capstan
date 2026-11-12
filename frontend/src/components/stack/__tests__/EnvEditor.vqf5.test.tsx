/**
 * agent-os-vqf5: a lock change (the 5-minute unlock expiring, or Lock) must not
 * replace what the user is typing in the stack env editor. The edits stay on
 * screen, Save stays refused while locked, and after the unlock a secret row
 * holds the real value, never the redacted blank. Notices are asserted by role.
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

const lockedPayload = {
  hasEnvFile: true,
  filename: '.env',
  locked: true,
  entries: [
    { key: 'PORT', value: '8080', sensitive: false, comment: false, line: 1 },
    { key: 'API_KEY', value: '', sensitive: true, comment: false, line: 2 },
  ],
}

const unlockedPayload = {
  hasEnvFile: true,
  filename: '.env',
  entries: [
    { key: 'PORT', value: '8080', sensitive: false, comment: false, line: 1 },
    { key: 'API_KEY', value: 'real-secret', sensitive: true, comment: false, line: 2 },
  ],
  raw: 'PORT=8080\nAPI_KEY=real-secret\n',
}

let server: typeof lockedPayload | typeof unlockedPayload
let queryClient: ReturnType<typeof renderWithProviders>['queryClient']

// Desktop and mobile renderings both carry the label; the first is enough.
const input = (n: number) =>
  screen.getAllByLabelText(`Environment variable value ${n}`)[0] as HTMLInputElement

function renderEditor() {
  ;({ queryClient } = renderWithProviders(<EnvEditor stackId="s1" />))
}

async function serve(next: typeof server) {
  server = next
  const calls = mockGetEnv.mock.calls.length
  await act(async () => {
    await queryClient.invalidateQueries({ queryKey: queryKeys.stack.env('s1') })
  })
  await waitFor(() => expect(mockGetEnv.mock.calls.length).toBeGreaterThan(calls))
}

/** The unlock window ends: the store locks, then the cache sync refetches. */
async function lockNow() {
  act(() => useEnvUnlockStore.getState().lock())
  await serve(lockedPayload)
}

beforeEach(() => {
  vi.clearAllMocks()
  act(() => useEnvUnlockStore.getState().lock())
  useAuthStore.setState({ authDisabled: false })
  server = unlockedPayload
  mockGetEnv.mockImplementation(() => Promise.resolve(server))
  mockUpdateEnv.mockResolvedValue({ outcome: 'success', reason: 'Saved' })
})

afterEach(() => {
  act(() => useEnvUnlockStore.getState().lock())
})

describe('EnvEditor across a lock change (agent-os-vqf5)', () => {
  it('vqf5: unlock expiry keeps the unsaved edit, shows a locked-again alert, and Save stays refused', async () => {
    renderEditor()
    await waitFor(() => expect(input(1).value).toBe('8080'))
    fireEvent.change(input(1), { target: { value: '9090' } })

    await lockNow()

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(/Unlock/)
    expect(input(1).value).toBe('9090')
    expect(screen.getAllByRole('button', { name: /^Save$/ })[0]).toBeDisabled()
    expect(mockUpdateEnv).not.toHaveBeenCalled()
  })

  it('vqf5: the alert sits above the table and persists across a view switch and a second locked refetch', async () => {
    renderEditor()
    await waitFor(() => expect(input(1).value).toBe('8080'))
    fireEvent.change(input(1), { target: { value: '9090' } })
    await lockNow()
    const alert = await screen.findByRole('alert')

    // Position: before the table, outside the tab panels Radix unmounts.
    expect(alert.compareDocumentPosition(input(1)) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()

    // Persistence: a view switch and another locked payload leave it up.
    await userEvent.setup().click(screen.getByRole('tab', { name: 'Table View' }))
    await serve({ ...lockedPayload, entries: [...lockedPayload.entries] })
    expect(screen.getAllByRole('alert')).toHaveLength(1)
    expect(input(1).value).toBe('9090')
  })

  it('vqf5: edit, lock, unlock: the edit is kept and the secret row holds the real value, never blank', async () => {
    renderEditor()
    await waitFor(() => expect(input(1).value).toBe('8080'))
    fireEvent.change(input(1), { target: { value: '9090' } })
    await lockNow()
    await screen.findByRole('alert')

    await serve(unlockedPayload)

    await waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument())
    expect(input(1).value).toBe('9090')
    expect(input(2).value).toBe('real-secret')

    fireEvent.click(screen.getAllByRole('button', { name: /^Save$/ })[0])
    await waitFor(() => expect(mockUpdateEnv).toHaveBeenCalledTimes(1))
    const body = mockUpdateEnv.mock.calls[0][1] as { entries: Array<{ key: string; value: string }> }
    expect(body.entries.map((e) => [e.key, e.value])).toEqual([
      ['PORT', '9090'],
      ['API_KEY', 'real-secret'],
    ])
  })

  it('vqf5: edit while locked from the first load, then unlock: the blank secret becomes the real value and a save never writes the blank', async () => {
    server = lockedPayload
    renderEditor()
    await waitFor(() => expect(input(1).value).toBe('8080'))
    expect(input(2).value).toBe('')
    fireEvent.change(input(1), { target: { value: '9090' } })
    expect(await screen.findByRole('alert')).toBeInTheDocument()

    await serve(unlockedPayload)

    await waitFor(() => expect(input(2).value).toBe('real-secret'))
    expect(input(1).value).toBe('9090')
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()

    fireEvent.click(screen.getAllByRole('button', { name: /^Save$/ })[0])
    await waitFor(() => expect(mockUpdateEnv).toHaveBeenCalledTimes(1))
    const body = mockUpdateEnv.mock.calls[0][1] as { entries: Array<{ key: string; value: string }> }
    expect(body.entries.find((e) => e.key === 'API_KEY')?.value).toBe('real-secret')
  })

  it('vqf5: a clean editor still takes the locked payload, with no alert', async () => {
    renderEditor()
    await waitFor(() => expect(input(1).value).toBe('8080'))

    await lockNow()

    await waitFor(() => expect(input(2).value).toBe(''))
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })
})
