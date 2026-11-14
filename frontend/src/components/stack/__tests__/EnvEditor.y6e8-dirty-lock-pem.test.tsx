/**
 * agent-os-y6e8: the server flags a multi-line secret's body rows and a
 * commented-out secret as sensitive only in the payload it redacts; unlocked,
 * they arrive sensitive:false. A lock with unsaved edits keeps the unlocked rows
 * (rebaseWhileDirty), so those rows stayed readable, in the value and in the
 * key field of a body row, and searchable. Sequence from the review of PR #739
 * (probe H); the save arm proves an unlock still writes the real values.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { screen, waitFor, fireEvent, act } from '@testing-library/react'
import { renderWithProviders } from '../../../test/utils'
import { useEnvUnlockStore } from '@/stores/envUnlockStore'
import { useAuthStore } from '@/stores/authStore'
import { invalidateEnvUnlockQueries } from '@/lib/env-unlock-queries'

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

// Hand-built in the style of EnvEditor.yt2f-unlock.test.tsx's fixtures, not
// parser output: the real parser reads a closed quoted value as one multi-line
// row. The rows give the editor the shapes it must mask.
const pemUnlocked = {
  hasEnvFile: true,
  filename: '.env',
  entries: [
    { key: 'PORT', value: '8080', line: 1, sensitive: false },
    { key: '', value: '#OLD_SECRET=commentedvalue', line: 2, sensitive: false, comment: true },
    { key: 'SSH_KEY', value: '"-----BEGIN', line: 3, sensitive: true },
    { key: 'bodylineAAAA', value: 'bodylineAAAA', line: 4, sensitive: false, comment: true },
    { key: '', value: '-----END"', line: 5, sensitive: false, comment: true },
    { key: 'TZ', value: 'UTC', line: 6, sensitive: false },
  ],
  raw: 'PORT=8080\n#OLD_SECRET=commentedvalue\nSSH_KEY="-----BEGIN\nbodylineAAAA\n-----END"\nTZ=UTC\n',
}
const pemLocked = {
  hasEnvFile: true,
  filename: '.env',
  locked: true,
  entries: [
    { key: 'PORT', value: '8080', line: 1, sensitive: false },
    { key: '', value: '#OLD_SECRET=', line: 2, sensitive: true, comment: true },
    { key: 'SSH_KEY', value: '', line: 3, sensitive: true },
    { key: '', value: '', line: 4, sensitive: true, comment: true },
    { key: '', value: '', line: 5, sensitive: true, comment: true },
    { key: 'TZ', value: 'UTC', line: 6, sensitive: false },
  ],
}

const val = (n: number) => screen.getAllByLabelText(`Environment variable value ${n}`)[0] as HTMLInputElement
const keyValues = () =>
  screen.getAllByLabelText(/^Environment variable key \d+$/).map((el) => (el as HTMLInputElement).value)
const SECRETS = ['bodylineAAAA', 'commentedvalue']

async function dirtyLock() {
  mockGetEnv.mockResolvedValue(pemUnlocked)
  act(() => useEnvUnlockStore.getState().unlock('tok'))
  const r = renderWithProviders(<EnvEditor stackId="s1" />)
  await waitFor(() => expect(val(1).value).toBe('8080'))
  // Positive side: shown while unlocked.
  expect(screen.getAllByText('commentedvalue', { exact: false }).length).toBeGreaterThan(0)
  expect(keyValues()).toContain('bodylineAAAA')
  fireEvent.change(val(1), { target: { value: '9090' } })
  mockGetEnv.mockResolvedValue(pemLocked)
  act(() => useEnvUnlockStore.getState().lock())
  await act(() => invalidateEnvUnlockQueries(r.queryClient))
  await waitFor(() => expect(mockGetEnv).toHaveBeenCalledTimes(2))
  await waitFor(() => expect(screen.getByRole('button', { name: 'Unlock' })).toBeInTheDocument())
  expect(val(1).value).toBe('9090') // the edit is kept
  return r
}

describe('EnvEditor: a dirty lock with a multi-line and a commented secret (agent-os-y6e8)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    act(() => useEnvUnlockStore.getState().lock())
    useAuthStore.setState({ authDisabled: false })
    mockUpdateEnv.mockResolvedValue({ outcome: 'success', reason: 'Saved' })
  })
  afterEach(() => {
    act(() => useEnvUnlockStore.getState().lock())
  })

  it('neither the body line nor the commented secret is shown, in a value or a key', async () => {
    await dirtyLock()

    for (const secret of SECRETS) {
      expect(screen.queryAllByText(secret, { exact: false })).toHaveLength(0)
      expect(keyValues().filter((k) => k.includes(secret))).toEqual([])
    }
  })

  it('search matches neither, and still finds a shown row', async () => {
    await dirtyLock()
    const search = screen.getByPlaceholderText('Filter env vars…')

    for (const q of ['bodyline', 'commentedv']) {
      fireEvent.change(search, { target: { value: q } })
      expect(screen.queryAllByLabelText(/^Environment variable key \d+$/)).toHaveLength(0)
    }
    fireEvent.change(search, { target: { value: 'UTC' } })
    expect(screen.queryAllByLabelText(/^Environment variable key \d+$/).length).toBeGreaterThan(0)
  })

  it('unlock and Save writes the real rows and the kept edit', async () => {
    const { queryClient } = await dirtyLock()

    mockGetEnv.mockResolvedValue(pemUnlocked)
    act(() => useEnvUnlockStore.getState().unlock('tok'))
    await act(() => invalidateEnvUnlockQueries(queryClient))
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Unlock' })).toBeNull())

    fireEvent.click(screen.getAllByRole('button', { name: /^Save$/ })[0])
    await waitFor(() => expect(mockUpdateEnv).toHaveBeenCalledTimes(1))
    const body = mockUpdateEnv.mock.calls[0][1] as { entries: Array<{ key: string; value: string }> }
    expect(body.entries.map((e) => [e.key, e.value])).toEqual([
      ['PORT', '9090'],
      ['', '#OLD_SECRET=commentedvalue'],
      ['SSH_KEY', '"-----BEGIN'],
      ['bodylineAAAA', 'bodylineAAAA'],
      ['', '-----END"'],
      ['TZ', 'UTC'],
    ])
  })
})

describe('EnvEditor: harmless comments across a lock (agent-os-y6e8)', () => {
  const unlockedNotes = {
    hasEnvFile: true,
    filename: '.env',
    entries: [
      { key: 'PORT', value: '8080', line: 1, sensitive: false },
      { key: '', value: '# database settings', line: 2, sensitive: false, comment: true },
    ],
    raw: 'PORT=8080\n# database settings\n',
  }
  const lockedNotes = { ...unlockedNotes, raw: undefined, locked: true }

  beforeEach(() => {
    vi.clearAllMocks()
    act(() => useEnvUnlockStore.getState().lock())
    useAuthStore.setState({ authDisabled: false })
  })
  afterEach(() => {
    act(() => useEnvUnlockStore.getState().lock())
  })

  async function loadUnlockedNotes() {
    mockGetEnv.mockResolvedValue(unlockedNotes)
    act(() => useEnvUnlockStore.getState().unlock('tok'))
    const r = renderWithProviders(<EnvEditor stackId="s1" />)
    await waitFor(() => expect(val(1).value).toBe('8080'))
    expect(screen.getAllByText('# database settings').length).toBeGreaterThan(0)
    return r
  }

  it('dirty lock: a harmless comment shows masked, and revealing it opens the unlock dialog', async () => {
    const { queryClient } = await loadUnlockedNotes()
    fireEvent.change(val(1), { target: { value: '9090' } })
    mockGetEnv.mockResolvedValue(lockedNotes)
    act(() => useEnvUnlockStore.getState().lock())
    await act(() => invalidateEnvUnlockQueries(queryClient))
    await waitFor(() => expect(mockGetEnv).toHaveBeenCalledTimes(2))

    expect(screen.queryAllByText('# database settings')).toHaveLength(0)
    fireEvent.click(screen.getAllByRole('button', { name: 'Toggle visibility for entry 2' })[0])
    expect(await screen.findByText('Unlock environment variables')).toBeInTheDocument()
    expect(screen.queryAllByText('# database settings')).toHaveLength(0)
  })

  it('clean lock: the locked payload hydrates and harmless comments show as the server sends them', async () => {
    const { queryClient } = await loadUnlockedNotes()
    mockGetEnv.mockResolvedValue(lockedNotes)
    act(() => useEnvUnlockStore.getState().lock())
    await act(() => invalidateEnvUnlockQueries(queryClient))
    await waitFor(() => expect(mockGetEnv).toHaveBeenCalledTimes(2))

    await waitFor(() => expect(screen.getAllByText('# database settings').length).toBeGreaterThan(0))
  })
})

describe('EnvEditor: a body line the parser reads as KEY= (agent-os-y6e8, review rv-envedit3 F1)', () => {
  // A base64 body line ending in "==" parses, unlocked, as a variable named by
  // the line with the value "="; only the locked payload blanks and flags it.
  const unlocked = {
    hasEnvFile: true,
    filename: '.env',
    entries: [
      { key: 'PORT', value: '8080', line: 1, sensitive: false },
      { key: '', value: '#OLD_KEY="-----BEGIN RSA PRIVATE KEY-----', line: 2, sensitive: false, comment: true },
      { key: 'MIIEbodylineAAAA', value: 'MIIEbodylineAAAA', line: 3, sensitive: false, comment: true },
      { key: 'QkZHSlastLineXyZ', value: '=', line: 4, sensitive: false },
      { key: '', value: '-----END RSA PRIVATE KEY-----"', line: 5, sensitive: false, comment: true },
      { key: 'TZ', value: 'UTC', line: 6, sensitive: false },
    ],
    raw: 'PORT=8080\n#OLD_KEY="-----BEGIN RSA PRIVATE KEY-----\nMIIEbodylineAAAA\nQkZHSlastLineXyZ==\n-----END RSA PRIVATE KEY-----"\nTZ=UTC\n',
  }
  const locked = {
    hasEnvFile: true,
    filename: '.env',
    locked: true,
    entries: [
      { key: 'PORT', value: '8080', line: 1, sensitive: false },
      { key: '', value: '#OLD_KEY=', line: 2, sensitive: true, comment: true },
      { key: '', value: '', line: 3, sensitive: true, comment: true },
      { key: '', value: '', line: 4, sensitive: true },
      { key: '', value: '', line: 5, sensitive: true, comment: true },
      { key: 'TZ', value: 'UTC', line: 6, sensitive: false },
    ],
  }

  beforeEach(() => {
    vi.clearAllMocks()
    act(() => useEnvUnlockStore.getState().lock())
    useAuthStore.setState({ authDisabled: false })
    mockUpdateEnv.mockResolvedValue({ outcome: 'success', reason: 'Saved' })
  })
  afterEach(() => {
    act(() => useEnvUnlockStore.getState().lock())
  })

  async function dirtyLockWith(lockedPayload: object) {
    mockGetEnv.mockResolvedValue(unlocked)
    act(() => useEnvUnlockStore.getState().unlock('tok'))
    const r = renderWithProviders(<EnvEditor stackId="s1" />)
    await waitFor(() => expect(val(1).value).toBe('8080'))
    fireEvent.change(val(1), { target: { value: '9090' } })
    mockGetEnv.mockResolvedValue(lockedPayload)
    act(() => useEnvUnlockStore.getState().lock())
    await act(() => invalidateEnvUnlockQueries(r.queryClient))
    await waitFor(() => expect(mockGetEnv).toHaveBeenCalledTimes(2))
    return r
  }

  it('the file changed on disk during the lock: every variable row hides its key and value', async () => {
    await dirtyLockWith({ ...locked, entries: [...locked.entries, { key: 'NEW', value: 'x', line: 7, sensitive: false }] })

    expect(keyValues().filter((k) => k !== '')).toEqual([])
    for (const n of [1, 4, 6]) expect(val(n).type).toBe('password')
  })

  it('a hidden key cannot be typed over; after unlock and reveal it is editable again', async () => {
    await dirtyLockWith(locked)
    const key4 = () => screen.getAllByLabelText('Environment variable key 4') as HTMLInputElement[]
    expect(key4().map((el) => el.disabled)).toEqual([true, true])

    act(() => useEnvUnlockStore.getState().unlock('tok'))
    fireEvent.click(screen.getAllByRole('button', { name: 'Toggle visibility for entry 4' })[0])
    expect(key4().map((el) => [el.value, el.disabled])).toEqual([
      ['QkZHSlastLineXyZ', false],
      ['QkZHSlastLineXyZ', false],
    ])
  })

  it('unlock: revealing the row shows its key again, and Save writes the real rows', async () => {
    const { queryClient } = await dirtyLockWith(locked)
    expect(keyValues().filter((k) => k.includes('QkZHSlast'))).toEqual([])

    mockGetEnv.mockResolvedValue(unlocked)
    act(() => useEnvUnlockStore.getState().unlock('tok'))
    await act(() => invalidateEnvUnlockQueries(queryClient))
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Unlock' })).toBeNull())
    fireEvent.click(screen.getAllByRole('button', { name: 'Toggle visibility for entry 4' })[0])
    expect(keyValues()).toContain('QkZHSlastLineXyZ')

    fireEvent.click(screen.getAllByRole('button', { name: /^Save$/ })[0])
    await waitFor(() => expect(mockUpdateEnv).toHaveBeenCalledTimes(1))
    const body = mockUpdateEnv.mock.calls[0][1] as { entries: Array<{ key: string; value: string }> }
    expect(body.entries.map((e) => [e.key, e.value])).toEqual([
      ['PORT', '9090'],
      ['', '#OLD_KEY="-----BEGIN RSA PRIVATE KEY-----'],
      ['MIIEbodylineAAAA', 'MIIEbodylineAAAA'],
      ['QkZHSlastLineXyZ', '='],
      ['', '-----END RSA PRIVATE KEY-----"'],
      ['TZ', 'UTC'],
    ])
  })

  it('dirty lock: the line is not shown as a key and search does not match it', async () => {
    mockGetEnv.mockResolvedValue(unlocked)
    act(() => useEnvUnlockStore.getState().unlock('tok'))
    const { queryClient } = renderWithProviders(<EnvEditor stackId="s1" />)
    await waitFor(() => expect(val(1).value).toBe('8080'))
    expect(keyValues()).toContain('QkZHSlastLineXyZ') // positive side: shown while unlocked
    fireEvent.change(val(1), { target: { value: '9090' } })
    mockGetEnv.mockResolvedValue(locked)
    act(() => useEnvUnlockStore.getState().lock())
    await act(() => invalidateEnvUnlockQueries(queryClient))
    await waitFor(() => expect(mockGetEnv).toHaveBeenCalledTimes(2))
    expect(val(1).value).toBe('9090')

    expect(keyValues().filter((k) => k.includes('QkZHSlast'))).toEqual([])
    fireEvent.change(screen.getByPlaceholderText('Filter env vars…'), { target: { value: 'QkZHSlast' } })
    expect(screen.queryAllByLabelText(/^Environment variable key \d+$/)).toHaveLength(0)
  })
})
