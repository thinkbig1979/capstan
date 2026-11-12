/**
 * agent-os-vqf5: a dirty editor unlocked through the eye button. EnvUnlockDialog
 * awaits the refetch and then calls onUnlocked, a closure from the render that
 * submitted, which toggles the row on the pre-unlock (blank) entries. The result
 * must not depend on whether the refetch or that stale toggle reaches React
 * first, so the same arms run under three notifyManager schedulers.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { screen, waitFor, fireEvent, act } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { notifyManager } from '@tanstack/react-query'
import { renderWithProviders } from '../../../test/utils'
import { useEnvUnlockStore } from '@/stores/envUnlockStore'
import { useAuthStore } from '@/stores/authStore'

const mockGetEnv = vi.fn()
const mockUpdateEnv = vi.fn()
const mockVerifyPassword = vi.fn()

vi.mock('@/lib/api', () => ({
  stacksApi: {
    getEnv: (...args: unknown[]) => mockGetEnv(...args),
    updateEnv: (...args: unknown[]) => mockUpdateEnv(...args),
    createEnv: vi.fn(),
    updateComposeAndEnv: vi.fn(),
  },
  authApi: { verifyPassword: (...args: unknown[]) => mockVerifyPassword(...args) },
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

const input = (n: number) =>
  screen.getAllByLabelText(`Environment variable value ${n}`)[0] as HTMLInputElement

const SCHEDULERS: Record<string, (cb: () => void) => void> = {
  'default (setTimeout 0)': (cb) => void setTimeout(cb, 0),
  'microtask': (cb) => queueMicrotask(cb),
  'synchronous': (cb) => cb(),
}

async function unlockThroughTheEyeButton() {
  const user = userEvent.setup()
  await waitFor(() => expect(input(1).value).toBe('8080'))
  fireEvent.change(input(1), { target: { value: '9090' } })
  await user.click(screen.getAllByRole('button', { name: 'Toggle visibility for entry 2' })[0])
  await screen.findByText('Unlock environment variables')
  await user.type(screen.getByLabelText('Password'), 'pw')
  await user.click(screen.getByRole('button', { name: 'Unlock' }))
  await waitFor(() => expect(input(2).value).toBe('real-secret'))
}

const savedBody = () => mockUpdateEnv.mock.calls[0][1] as { entries: Array<{ key: string; value: string }> }

describe.each(Object.entries(SCHEDULERS))('dirty editor unlocked through the eye button, scheduler: %s (agent-os-vqf5)', (_name, scheduler) => {
  beforeEach(() => {
    vi.clearAllMocks()
    act(() => useEnvUnlockStore.getState().lock())
    useAuthStore.setState({ authDisabled: false })
    notifyManager.setScheduler(scheduler)
    server = lockedPayload
    mockGetEnv.mockImplementation(() => Promise.resolve(server))
    mockVerifyPassword.mockImplementation(() => {
      server = unlockedPayload
      return Promise.resolve({ ok: true, unlockToken: 'tok', expiresIn: 300 })
    })
    mockUpdateEnv.mockResolvedValue({ outcome: 'success', reason: 'Saved' })
  })

  afterEach(() => {
    notifyManager.setScheduler((cb) => void setTimeout(cb, 0))
    act(() => useEnvUnlockStore.getState().lock())
  })

  it('vqf5: the kept edit stays, the secret is real, and a save never writes the blank', async () => {
    renderWithProviders(<EnvEditor stackId="s1" />)

    await unlockThroughTheEyeButton()

    expect(input(1).value).toBe('9090')
    fireEvent.click(screen.getAllByRole('button', { name: /^Save$/ })[0])
    await waitFor(() => expect(mockUpdateEnv).toHaveBeenCalledTimes(1))
    expect(savedBody().entries.map((e) => [e.key, e.value])).toEqual([
      ['PORT', '9090'],
      ['API_KEY', 'real-secret'],
    ])
  })

  it('vqf5: Undo after that unlock does not bring the blank back either', async () => {
    renderWithProviders(<EnvEditor stackId="s1" />)
    await unlockThroughTheEyeButton()

    await userEvent.setup().keyboard('{Control>}z{/Control}')

    // The reveal is not an undo step, so Undo takes back the PORT edit.
    await waitFor(() => expect(input(1).value).toBe('8080'))
    expect(input(2).value).toBe('real-secret')
    fireEvent.click(screen.getAllByRole('button', { name: /^Save$/ })[0])
    await waitFor(() => expect(mockUpdateEnv).toHaveBeenCalledTimes(1))
    expect(savedBody().entries.find((e) => e.key === 'API_KEY')?.value).toBe('real-secret')
  })
})
