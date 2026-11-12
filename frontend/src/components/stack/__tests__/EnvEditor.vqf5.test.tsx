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

  it('vqf5: the alert sits above the table and outlives a locked payload that really changes', async () => {
    renderEditor()
    await waitFor(() => expect(input(1).value).toBe('8080'))
    fireEvent.change(input(1), { target: { value: '9090' } })
    await lockNow()
    const alert = await screen.findByRole('alert')

    // Position: before the table, outside the tab panels Radix unmounts.
    expect(alert.compareDocumentPosition(input(1)) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()

    // Persistence: another locked payload that differs (structural sharing
    // would hand back the same reference for an equal one and run nothing).
    // The changed-on-disk notice joins it; the locked-again alert stays.
    await serve({
      ...lockedPayload,
      entries: [{ ...lockedPayload.entries[0], value: '7070' }, lockedPayload.entries[1]],
    })
    await waitFor(() => expect(screen.getAllByRole('alert')).toHaveLength(2))
    expect(screen.getByRole('button', { name: 'Unlock' })).toBeInTheDocument()
    expect(input(1).value).toBe('9090')
  })

  it('vqf5: the alert and the kept edit are visible after a lock that arrives in the Raw view', async () => {
    renderEditor()
    await waitFor(() => expect(input(1).value).toBe('8080'))
    fireEvent.change(input(1), { target: { value: '9090' } })
    await userEvent.setup().click(screen.getByRole('tab', { name: 'Raw Editor' }))
    await screen.findByPlaceholderText('KEY=value')

    await lockNow()

    expect(await screen.findByRole('alert')).toHaveTextContent(/Unlock/)
    // The Raw tab goes with the lock; the table with the kept edit is what shows.
    expect(screen.queryByRole('tab', { name: 'Raw Editor' })).not.toBeInTheDocument()
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

const dupLocked = {
  hasEnvFile: true,
  filename: '.env',
  locked: true,
  entries: [
    { key: 'API_KEY', value: '', sensitive: true, comment: false, line: 1 },
    { key: 'PORT', value: '8080', sensitive: false, comment: false, line: 2 },
    { key: 'API_KEY', value: '', sensitive: true, comment: false, line: 3 },
  ],
}
const dupUnlocked = {
  hasEnvFile: true,
  filename: '.env',
  entries: [
    { key: 'API_KEY', value: 'one', sensitive: true, comment: false, line: 1 },
    { key: 'PORT', value: '8080', sensitive: false, comment: false, line: 2 },
    { key: 'API_KEY', value: 'two', sensitive: true, comment: false, line: 3 },
  ],
  raw: 'API_KEY=one\nPORT=8080\nAPI_KEY=two\n',
}

const keyInput = (n: number) =>
  screen.getAllByLabelText(`Environment variable key ${n}`)[0] as HTMLInputElement

const savedBody = () => {
  const body = mockUpdateEnv.mock.calls[0][1] as { entries: Array<{ key: string; value: string }> }
  // _rowId is client-side identity and must not reach the wire.
  expect(body.entries.every((e) => !('_rowId' in e))).toBe(true)
  return body
}

async function saveAfterUnlock() {
  fireEvent.click(screen.getAllByRole('button', { name: /^Save$/ })[0])
  await waitFor(() => expect(mockUpdateEnv).toHaveBeenCalledTimes(1))
}

describe('EnvEditor undo/redo after an unlock (agent-os-vqf5, review MUST-1)', () => {
  const undo = () => userEvent.setup().keyboard('{Control>}z{/Control}')
  const redo = () => userEvent.setup().keyboard('{Control>}y{/Control}')

  it('vqf5: Undo after an unlock restores the real secret, never the blank', async () => {
    server = lockedPayload
    renderEditor()
    await waitFor(() => expect(input(1).value).toBe('8080'))
    fireEvent.change(input(1), { target: { value: '9090' } })
    await serve(unlockedPayload)
    await waitFor(() => expect(input(2).value).toBe('real-secret'))

    await undo()

    await waitFor(() => expect(input(1).value).toBe('8080'))
    expect(input(2).value).toBe('real-secret')
    await saveAfterUnlock()
    expect(savedBody().entries.find((e) => e.key === 'API_KEY')?.value).toBe('real-secret')
  })

  it('vqf5: Redo after an unlock restores the real secret, never the blank', async () => {
    server = lockedPayload
    renderEditor()
    await waitFor(() => expect(input(1).value).toBe('8080'))
    fireEvent.change(input(1), { target: { value: '9090' } })
    fireEvent.change(input(1), { target: { value: '9191' } })
    await serve(unlockedPayload)
    await waitFor(() => expect(input(2).value).toBe('real-secret'))

    await undo()
    await undo()
    await waitFor(() => expect(input(1).value).toBe('8080'))
    await redo()

    await waitFor(() => expect(input(1).value).toBe('9090'))
    expect(input(2).value).toBe('real-secret')
    await saveAfterUnlock()
    expect(savedBody().entries.find((e) => e.key === 'API_KEY')?.value).toBe('real-secret')
  })

  it('vqf5: after Undo past the unlock the Raw view holds the real file, not the empty locked raw', async () => {
    server = lockedPayload
    renderEditor()
    await waitFor(() => expect(input(1).value).toBe('8080'))
    fireEvent.change(input(1), { target: { value: '9090' } })
    await serve(unlockedPayload)
    await waitFor(() => expect(input(2).value).toBe('real-secret'))

    await undo()
    await waitFor(() => expect(input(1).value).toBe('8080'))
    await userEvent.setup().click(screen.getByRole('tab', { name: 'Raw Editor' }))

    expect(await screen.findByPlaceholderText('KEY=value')).toHaveValue(unlockedPayload.raw)
  })
})

describe('EnvEditor rows edited while locked follow their identity (agent-os-vqf5, review MUST-2)', () => {
  it('vqf5: renaming a redacted row while locked, then unlock: the secret follows the row', async () => {
    server = lockedPayload
    renderEditor()
    await waitFor(() => expect(input(1).value).toBe('8080'))
    fireEvent.change(keyInput(2), { target: { value: 'API_KEY_V2' } })

    await serve(unlockedPayload)

    await waitFor(() => expect(input(2).value).toBe('real-secret'))
    await saveAfterUnlock()
    expect(savedBody().entries.map((e) => [e.key, e.value])).toEqual([
      ['PORT', '8080'],
      ['API_KEY_V2', 'real-secret'],
    ])
  })

  it('vqf5: deleting the first of two same-key redacted rows while locked: the survivor gets its own secret', async () => {
    server = dupLocked
    renderEditor()
    await waitFor(() => expect(input(2).value).toBe('8080'))
    fireEvent.click(screen.getAllByLabelText('Delete entry 1')[0])

    await serve(dupUnlocked)

    await waitFor(() => expect(input(2).value).toBe('two'))
    await saveAfterUnlock()
    expect(savedBody().entries.map((e) => [e.key, e.value])).toEqual([
      ['PORT', '8080'],
      ['API_KEY', 'two'],
    ])
  })

  it('vqf5: a row renamed onto an existing redacted key does not take that secret from the original', async () => {
    server = lockedPayload
    renderEditor()
    await waitFor(() => expect(input(1).value).toBe('8080'))
    // PORT becomes a second API_KEY, above the original one.
    fireEvent.change(keyInput(1), { target: { value: 'API_KEY' } })

    await serve(unlockedPayload)

    await waitFor(() => expect(input(2).value).toBe('real-secret'))
    expect(input(1).value).toBe('8080')
    await saveAfterUnlock()
    expect(savedBody().entries.map((e) => [e.key, e.value])).toEqual([
      ['API_KEY', '8080'],
      ['API_KEY', 'real-secret'],
    ])
  })
})
