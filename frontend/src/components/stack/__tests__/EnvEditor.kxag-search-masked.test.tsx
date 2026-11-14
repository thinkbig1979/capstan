/**
 * agent-os-kxag: the search box matched on masked values, so with a dirty form
 * kept across a lock (rows still hold real values) a filter told whether a
 * secret contains a substring. Sequence from the review of PR #738 (probe D).
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { screen, waitFor, fireEvent, act } from '@testing-library/react'
import { renderWithProviders } from '../../../test/utils'
import { useEnvUnlockStore } from '@/stores/envUnlockStore'
import { useAuthStore } from '@/stores/authStore'
import { invalidateEnvUnlockQueries } from '@/lib/env-unlock-queries'
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

const lockedPayload = {
  hasEnvFile: true,
  filename: '.env',
  locked: true,
  entries: [
    { key: 'PORT', value: '8080', sensitive: false, comment: false, line: 1 },
    { key: 'TLS', value: '', sensitive: true, comment: false, line: 2 },
    { key: 'DB_PASSWORD', value: '', sensitive: true, comment: false, line: 3 },
  ],
}

const val = (n: number) => screen.getAllByLabelText(`Environment variable value ${n}`)[0] as HTMLInputElement
const key = (n: number) => screen.getAllByLabelText(`Environment variable key ${n}`)[0] as HTMLInputElement

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

describe('EnvEditor: search over masked values (agent-os-kxag)', () => {
  setup()

  it('locked with a dirty form: a substring of a masked value does not match; keys still do', async () => {
    const { queryClient } = await loadUnlocked()
    fireEvent.change(val(1), { target: { value: '9090' } }) // dirty, so the lock keeps the rows
    mockGetEnv.mockResolvedValue(lockedPayload)
    act(() => useEnvUnlockStore.getState().lock())
    await act(() => invalidateEnvUnlockQueries(queryClient))
    await waitFor(() => expect(mockGetEnv).toHaveBeenCalledTimes(2))
    expect(val(3).value).toBe('hunter2')
    expect(val(3).type).toBe('password')

    const search = screen.getByPlaceholderText('Filter env vars…')
    fireEvent.change(search, { target: { value: 'zzz' } })
    expect(screen.queryAllByLabelText('Environment variable key 3')).toHaveLength(0) // the filter hides rows

    fireEvent.change(search, { target: { value: 'hunt' } })
    expect(screen.queryAllByLabelText(/Environment variable key/)).toHaveLength(0)

    fireEvent.change(search, { target: { value: 'DB_PASS' } })
    expect(key(3).value).toBe('DB_PASSWORD')
    // a value that is shown is still searchable
    fireEvent.change(search, { target: { value: '9090' } })
    expect(key(1).value).toBe('PORT')
  })
})
