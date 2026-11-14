/**
 * agent-os-y4gh: a reveal made while unlocked is stored in the undo history
 * (the row's `sensitive: false`), and so is every edit after it. A lock with a
 * dirty form keeps the edits and re-masks the rows on screen, but Undo and Redo
 * restore the snapshots as they were, so they must not bring the plaintext back
 * while locked.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { screen, waitFor, fireEvent, act } from '@testing-library/react'
import { renderWithProviders } from '../../../test/utils'
import { useEnvUnlockStore } from '@/stores/envUnlockStore'
import { useAuthStore } from '@/stores/authStore'

const mockGetEnv = vi.fn()

vi.mock('@/lib/api', () => ({
  stacksApi: {
    getEnv: (...args: unknown[]) => mockGetEnv(...args),
    updateEnv: vi.fn(),
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

const unlockedPayload = {
  hasEnvFile: true,
  filename: '.env',
  entries: [
    { key: 'PORT', value: '8080', sensitive: false, comment: false, line: 1 },
    { key: 'API_KEY', value: 'real-secret', sensitive: true, comment: false, line: 2 },
    { key: 'TLS', value: 'pem-secret', sensitive: true, comment: false, line: 3 },
  ],
  raw: 'PORT=8080\nAPI_KEY=real-secret\nTLS=pem-secret\n',
}

const valueInput = (n: number) =>
  screen.getAllByLabelText(`Environment variable value ${n}`)[0] as HTMLInputElement
const eye = (n: number) => screen.getAllByRole('button', { name: `Toggle visibility for entry ${n}` })[0]

function expectSecretsMasked() {
  expect(valueInput(2).type).toBe('password')
  expect(valueInput(3).type).toBe('password')
}

describe('EnvEditor: undo history after a lock (agent-os-y4gh)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    act(() => useEnvUnlockStore.getState().lock())
    useAuthStore.setState({ authDisabled: false })
    mockGetEnv.mockResolvedValue(unlockedPayload)
  })

  afterEach(() => {
    act(() => useEnvUnlockStore.getState().lock())
  })

  it('unlock, reveal, edit, lock: Undo and Redo never show a secret while locked', async () => {
    act(() => useEnvUnlockStore.getState().unlock('tok'))
    renderWithProviders(<EnvEditor stackId="s1" />)
    await waitFor(() => expect(valueInput(2).value).toBe('real-secret'))
    expectSecretsMasked()

    // Reveal both secrets (a key-named row and a value-flagged one), then edit.
    fireEvent.click(eye(2))
    fireEvent.click(eye(3))
    expect(valueInput(2).type).toBe('text')
    expect(valueInput(3).type).toBe('text')
    fireEvent.change(valueInput(1), { target: { value: '9090' } })

    act(() => useEnvUnlockStore.getState().lock())
    expectSecretsMasked()

    const undo = screen.getByTitle('Undo (Ctrl+Z)')
    const redo = screen.getByTitle('Redo (Ctrl+Y)')

    fireEvent.click(undo)
    await waitFor(() => expect(valueInput(1).value).toBe('8080'))
    expectSecretsMasked()

    fireEvent.click(undo)
    expectSecretsMasked()

    fireEvent.click(redo)
    expectSecretsMasked()

    fireEvent.click(redo)
    await waitFor(() => expect(valueInput(1).value).toBe('9090'))
    expectSecretsMasked()
  })

  it('a revealed value-flagged row deleted before the lock comes back masked on Undo', async () => {
    act(() => useEnvUnlockStore.getState().unlock('tok'))
    renderWithProviders(<EnvEditor stackId="s1" />)
    await waitFor(() => expect(valueInput(3).value).toBe('pem-secret'))

    fireEvent.click(eye(3))
    expect(valueInput(3).type).toBe('text')
    fireEvent.click(screen.getAllByRole('button', { name: 'Delete entry 3' })[0])
    expect(screen.queryAllByLabelText('Environment variable value 3')).toHaveLength(0)
    // A new masked row makes the remask forget the ids no longer on screen, so
    // only the history still knows TLS was masked.
    fireEvent.click(screen.getByRole('button', { name: 'Add Entry' }))
    fireEvent.change(screen.getAllByLabelText('Environment variable key 3')[0], { target: { value: 'DB_PASSWORD' } })
    expect(valueInput(3).type).toBe('password')

    act(() => useEnvUnlockStore.getState().lock())

    const undo = screen.getByTitle('Undo (Ctrl+Z)')
    fireEvent.click(undo) // the rename
    fireEvent.click(undo) // the added row
    fireEvent.click(undo) // the delete
    await waitFor(() => expect(valueInput(3).value).toBe('pem-secret'))
    expect(valueInput(3).type).toBe('password')
  })

  // Review finding A: the history holds 50 snapshots, so after enough edits no
  // snapshot remembers TLS was masked, and the on-screen set must not forget it
  // either. 3 edits is the control: the history still covers it.
  it.each([3, 55])('a revealed row deleted after %i edits comes back masked on Undo after a lock', async (edits) => {
    act(() => useEnvUnlockStore.getState().unlock('tok'))
    renderWithProviders(<EnvEditor stackId="s1" />)
    await waitFor(() => expect(valueInput(3).value).toBe('pem-secret'))

    fireEvent.click(eye(3))
    for (let n = 0; n < edits; n++) fireEvent.change(valueInput(1), { target: { value: `80${n}` } })
    fireEvent.click(screen.getAllByRole('button', { name: 'Delete entry 3' })[0])
    fireEvent.click(screen.getByRole('button', { name: 'Add Entry' }))
    fireEvent.change(screen.getAllByLabelText('Environment variable key 3')[0], { target: { value: 'API_TOKEN' } })

    act(() => useEnvUnlockStore.getState().lock())

    const undo = screen.getByTitle('Undo (Ctrl+Z)')
    fireEvent.click(undo) // the rename
    fireEvent.click(undo) // the added row
    fireEvent.click(undo) // the delete
    await waitFor(() => expect(valueInput(3).value).toBe('pem-secret'))
    expect(valueInput(3).type).toBe('password')
  })
})
