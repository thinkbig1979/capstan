/**
 * agent-os-oeqf: a reveal carried across a refetch (hydrate keeps it by key)
 * lands on a NEW row id that was never masked, so once the key is renamed to a
 * harmless one, nothing tells the lock's remask that the row was a secret.
 * Sequences from the review of PR #738 (probes B and G).
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

const PEM = '-----BEGIN PRIVATE KEY-----'

const unlocked = {
  hasEnvFile: true,
  filename: '.env',
  entries: [
    { key: 'PORT', value: '8080', sensitive: false, comment: false, line: 1 },
    { key: 'TLS', value: PEM, sensitive: true, comment: false, line: 2 },
    { key: 'DB_PASSWORD', value: 'hunter2', sensitive: true, comment: false, line: 3 },
  ],
  raw: `PORT=8080\nTLS=${PEM}\nDB_PASSWORD=hunter2\n`,
}

const val = (n: number) => screen.getAllByLabelText(`Environment variable value ${n}`)[0] as HTMLInputElement
const key = (n: number) => screen.getAllByLabelText(`Environment variable key ${n}`)[0] as HTMLInputElement
const eye = (n: number) => screen.getAllByRole('button', { name: `Toggle visibility for entry ${n}` })[0]

async function loadUnlocked() {
  act(() => useEnvUnlockStore.getState().unlock('tok'))
  const r = renderWithProviders(<EnvEditor stackId="s1" />)
  await waitFor(() => expect(val(2).value).toBe(PEM))
  return r
}

function setup() {
  beforeEach(() => {
    vi.clearAllMocks()
    act(() => useEnvUnlockStore.getState().lock())
    useAuthStore.setState({ authDisabled: false })
    mockGetEnv.mockResolvedValue(unlocked)
  })
  afterEach(() => {
    act(() => useEnvUnlockStore.getState().lock())
  })
}

/** Unlocked, reveal DB_PASSWORD, then a changed file hydrates the clean editor. */
async function revealThenRefetch() {
  const { queryClient } = await loadUnlocked()
  fireEvent.click(eye(3))
  expect(val(3).type).toBe('text')
  mockGetEnv.mockResolvedValue({
    ...unlocked,
    entries: [...unlocked.entries, { key: 'NEW', value: 'x', sensitive: false, comment: false, line: 4 }],
  })
  await act(() => queryClient.invalidateQueries())
  await waitFor(() => expect(screen.getAllByLabelText('Environment variable key 4')[0]).toBeInTheDocument())
  expect(val(3).type).toBe('text') // the reveal is carried
}

describe('EnvEditor: a reveal carried across a refetch (agent-os-oeqf)', () => {
  setup()

  it('B: renamed to a harmless key, the row is masked at the lock', async () => {
    await revealThenRefetch()
    fireEvent.change(key(3), { target: { value: 'DB_PASS' } })

    act(() => useEnvUnlockStore.getState().lock())

    expect(val(3).value).toBe('hunter2')
    expect(val(3).type).toBe('password')
  })

  it('B control: without the refetch the same steps stay masked', async () => {
    await loadUnlocked()
    fireEvent.click(eye(3))
    fireEvent.change(key(3), { target: { value: 'DB_PASS' } })

    act(() => useEnvUnlockStore.getState().lock())

    expect(val(3).type).toBe('password')
  })

  it.each([
    ['after the first lock', 1],
    ['after a second lock', 2],
  ])('G: renamed and deleted, Undo of the delete %s shows it masked', async (_name, locks) => {
    await revealThenRefetch()
    fireEvent.change(key(3), { target: { value: 'DB_PASS' } })
    fireEvent.click(screen.getAllByRole('button', { name: 'Delete entry 3' })[0])

    act(() => useEnvUnlockStore.getState().lock())
    if (locks === 2) {
      act(() => useEnvUnlockStore.getState().unlock('tok'))
      act(() => useEnvUnlockStore.getState().lock())
    }

    fireEvent.click(screen.getByTitle('Undo (Ctrl+Z)'))
    await waitFor(() => expect(key(3).value).toBe('DB_PASS'))
    expect(val(3).value).toBe('hunter2')
    expect(val(3).type).toBe('password')
  })
})
