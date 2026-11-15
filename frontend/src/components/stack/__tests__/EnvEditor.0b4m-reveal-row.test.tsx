/**
 * agent-os-0b4m: reveal state carried across a refetch by key name, and only
 * for a key whose NAME looks secret. DATABASE_URL is flagged by the server for
 * its URL credential, not by its name.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { screen, waitFor, fireEvent, act } from '@testing-library/react'
import { renderWithProviders } from '../../../test/utils'
import { useEnvUnlockStore } from '@/stores/envUnlockStore'
import { useAuthStore } from '@/stores/authStore'
import { queryKeys } from '@/lib/query-keys'

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

const URL_VALUE = 'postgres://app:s3cret@db/app'
const unlocked = {
  hasEnvFile: true,
  filename: '.env',
  entries: [
    { key: 'PORT', value: '8080', line: 1, sensitive: false },
    { key: 'DATABASE_URL', value: URL_VALUE, line: 2, sensitive: true },
  ],
  raw: `PORT=8080\nDATABASE_URL=${URL_VALUE}\n`,
}

const val = (n: number) => screen.getAllByLabelText(`Environment variable value ${n}`)[0] as HTMLInputElement

async function revealUrl() {
  mockGetEnv.mockResolvedValue(unlocked)
  act(() => useEnvUnlockStore.getState().unlock('tok'))
  const r = renderWithProviders(<EnvEditor stackId="s1" />)
  await waitFor(() => expect(val(1).value).toBe('8080'))
  expect(val(2).type).toBe('password')
  fireEvent.click(screen.getAllByLabelText('Toggle visibility for entry 2')[0])
  expect(val(2).type).toBe('text')
  return r
}

describe('EnvEditor reveal state (agent-os-0b4m)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    act(() => useEnvUnlockStore.getState().lock())
    useAuthStore.setState({ authDisabled: false })
  })
  afterEach(() => {
    act(() => useEnvUnlockStore.getState().lock())
  })

  it('a refetch while unlocked keeps a revealed server-flagged value revealed', async () => {
    const { queryClient } = await revealUrl()
    // A changed file, so the refetch is a new payload and hydrates.
    mockGetEnv.mockResolvedValue({ ...unlocked, entries: [{ ...unlocked.entries[0], value: '9090' }, unlocked.entries[1]] })
    await act(() => queryClient.invalidateQueries({ queryKey: queryKeys.stack.env('s1') }))
    await waitFor(() => expect(val(1).value).toBe('9090'))
    expect(val(2).type).toBe('text')
  })

  it('locked: a blanked secret says it is hidden; an empty plain value does not', async () => {
    mockGetEnv.mockResolvedValue({
      hasEnvFile: true,
      filename: '.env',
      locked: true,
      entries: [
        { key: 'DATABASE_URL', value: '', line: 1, sensitive: true },
        { key: 'EMPTY', value: '', line: 2, sensitive: false },
      ],
    })
    renderWithProviders(<EnvEditor stackId="s1" />)
    await waitFor(() => expect(screen.getAllByLabelText('Environment variable value 1').length).toBeGreaterThan(0))
    const placeholders = (n: number) =>
      (screen.getAllByLabelText(`Environment variable value ${n}`) as HTMLInputElement[]).map((el) => el.placeholder)
    expect(placeholders(1)).toEqual(['Hidden, unlock to view', 'Hidden, unlock to view'])
    expect(placeholders(2)).toEqual(['value', 'value'])
  })

  it('a reveal does not follow its key to another line: the row there is masked again', async () => {
    const { queryClient } = await revealUrl()
    // A line inserted above: DATABASE_URL is now on line 3, under the same key.
    mockGetEnv.mockResolvedValue({
      ...unlocked,
      entries: [
        unlocked.entries[0],
        { key: 'TZ', value: 'UTC', line: 2, sensitive: false },
        { ...unlocked.entries[1], line: 3 },
      ],
    })
    await act(() => queryClient.invalidateQueries({ queryKey: queryKeys.stack.env('s1') }))
    await waitFor(() => expect(val(2).value).toBe('UTC'))
    expect(val(3).value).toBe(URL_VALUE)
    expect(val(3).type).toBe('password')
  })

  it('the unlock expiring re-masks it, with no refetch', async () => {
    await revealUrl()
    act(() => useEnvUnlockStore.getState().lock())
    expect(val(2).type).toBe('password')
  })
})
