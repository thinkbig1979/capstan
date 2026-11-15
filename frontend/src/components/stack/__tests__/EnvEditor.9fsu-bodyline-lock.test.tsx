/**
 * agent-os-9fsu: a private-key body line that parses as KEY= (base64 padding)
 * is flagged `sensitive` + `bodyLine` by the server in the UNLOCKED payload, so
 * the editor hides its key from the first render. Before, only the locked
 * payload flagged it (agent-os-y6e8): between a lock and its refetch, and for
 * good when that refetch fails, the key showed and search matched it, on a clean
 * lock as well as a dirty one (F9). Fixtures are hand-built in the style of
 * EnvEditor.y6e8-dirty-lock-pem.test.tsx.
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

const BODY = 'QkZHSlastLineXyZ'
// What the parser makes of a PEM written as one opener, base64 body lines and a
// closer: the lines after the opener are rows of their own, flagged by the server.
const unlocked = {
  hasEnvFile: true,
  filename: '.env',
  entries: [
    { key: 'PORT', value: '8080', line: 1, sensitive: false },
    { key: '', value: '#OLD_KEY="-----BEGIN RSA PRIVATE KEY-----', line: 2, sensitive: true, comment: true },
    { key: 'MIIEbodylineAAAA', value: 'MIIEbodylineAAAA', line: 3, sensitive: true, comment: true, bodyLine: true },
    { key: BODY, value: '=', line: 4, sensitive: true, bodyLine: true },
    { key: '', value: '-----END RSA PRIVATE KEY-----"', line: 5, sensitive: true, comment: true, bodyLine: true },
    { key: 'TZ', value: 'UTC', line: 6, sensitive: false },
  ],
  raw: `PORT=8080\n#OLD_KEY="-----BEGIN RSA PRIVATE KEY-----\nMIIEbodylineAAAA\n${BODY}==\n-----END RSA PRIVATE KEY-----"\nTZ=UTC\n`,
}

const val = (n: number) => screen.getAllByLabelText(`Environment variable value ${n}`)[0] as HTMLInputElement
const keyValues = () =>
  screen.getAllByLabelText(/^Environment variable key \d+$/).map((el) => (el as HTMLInputElement).value)

async function load() {
  mockGetEnv.mockResolvedValue(unlocked)
  act(() => useEnvUnlockStore.getState().unlock('tok'))
  const r = renderWithProviders(<EnvEditor stackId="s1" />)
  await waitFor(() => expect(val(1).value).toBe('8080'))
  return r
}

/** A lock whose refetch fails: the unlocked payload stays on screen for good. */
async function lockWithFailedRefetch(queryClient: Parameters<typeof invalidateEnvUnlockQueries>[0]) {
  mockGetEnv.mockRejectedValue(new Error('refetch failed'))
  act(() => useEnvUnlockStore.getState().lock())
  await act(() => invalidateEnvUnlockQueries(queryClient))
  await waitFor(() => expect(mockGetEnv).toHaveBeenCalledTimes(2))
}

describe('EnvEditor: a flagged body row that parses as KEY= (agent-os-9fsu)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    act(() => useEnvUnlockStore.getState().lock())
    useAuthStore.setState({ authDisabled: false })
  })
  afterEach(() => {
    act(() => useEnvUnlockStore.getState().lock())
  })

  it('its key is hidden from the first unlocked render, and the real keys are shown', async () => {
    await load()

    const keys = keyValues()
    expect(keys.filter((k) => k.includes(BODY))).toEqual([])
    expect(keys).toContain('PORT') // positive side: a plain row shows its key
    expect(keys).toContain('TZ')
  })

  it('clean lock, refetch failed: the key stays hidden and search does not match it', async () => {
    const { queryClient } = await load()
    await lockWithFailedRefetch(queryClient)

    expect(keyValues().filter((k) => k.includes(BODY))).toEqual([])
    const search = screen.getByPlaceholderText('Filter env vars…')
    fireEvent.change(search, { target: { value: 'QkZHSlast' } })
    expect(screen.queryAllByLabelText(/^Environment variable key \d+$/)).toHaveLength(0)
    fireEvent.change(search, { target: { value: 'UTC' } })
    expect(screen.queryAllByLabelText(/^Environment variable key \d+$/).length).toBeGreaterThan(0)
  })

  it('dirty lock, refetch failed: the key stays hidden, and so does the value', async () => {
    const { queryClient } = await load()
    fireEvent.change(val(1), { target: { value: '9090' } })
    await lockWithFailedRefetch(queryClient)

    expect(val(1).value).toBe('9090') // the edit is kept
    expect(keyValues().filter((k) => k.includes(BODY))).toEqual([])
    expect(val(4).type).toBe('password')
  })

  it('F10: the value of the hidden-key row cannot be typed over, from the first render and while locked', async () => {
    const { queryClient } = await load()
    expect(val(4).disabled).toBe(true)
    await lockWithFailedRefetch(queryClient)

    expect(val(4).disabled).toBe(true)
    // Positive side: a plain row stays editable.
    expect(val(1).disabled).toBe(false)
  })

  it('N3 (pinned, fails closed): after an unlock the key stays hidden, and the value frozen, until the row is revealed', async () => {
    const { queryClient } = await load()
    fireEvent.change(val(1), { target: { value: '9090' } })
    await lockWithFailedRefetch(queryClient)

    mockGetEnv.mockResolvedValue(unlocked)
    act(() => useEnvUnlockStore.getState().unlock('tok'))
    await act(() => invalidateEnvUnlockQueries(queryClient))
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Unlock' })).toBeNull())
    expect(keyValues().filter((k) => k.includes(BODY))).toEqual([])
    expect(val(4).disabled).toBe(true)

    fireEvent.click(screen.getAllByRole('button', { name: 'Toggle visibility for entry 4' })[0])
    expect(keyValues()).toContain(BODY)
    expect(val(4).disabled).toBe(false)
  })
})
