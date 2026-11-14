/**
 * agent-os-fgwm: a row the server flagged sensitive for its VALUE, under a key
 * that does not look secret (a PEM under TLS), must not be unmasked without the
 * unlock step. Two routes did it: renaming the key recomputed the mask from the
 * new key name only, and the "Visible" checkbox was enabled or disabled by the
 * key name only, so it revealed the row directly.
 *
 * Both arms load unlocked (real values on screen, masked), then lock: the values
 * stay in the editor and the remask covers them, which is the state where a
 * reveal must go through the unlock dialog.
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

const unlockedPayload = {
  hasEnvFile: true,
  filename: '.env',
  entries: [
    { key: 'PORT', value: '8080', sensitive: false, comment: false, line: 1 },
    { key: 'TLS', value: PEM, sensitive: true, comment: false, line: 2 },
    { key: 'API_KEY', value: 'real-secret', sensitive: true, comment: false, line: 3 },
  ],
  raw: `PORT=8080\nTLS=${PEM}\nAPI_KEY=real-secret\n`,
}

const valueInput = () => screen.getAllByLabelText('Environment variable value 2')[0] as HTMLInputElement
const keyInput = () => screen.getAllByLabelText('Environment variable key 2')[0] as HTMLInputElement
const visibleCheckbox = () =>
  screen.getAllByRole('checkbox', { name: 'Toggle visibility for entry 2' })[0] as HTMLInputElement

async function loadUnlockedThenLock() {
  act(() => useEnvUnlockStore.getState().unlock('tok'))
  renderWithProviders(<EnvEditor stackId="s1" />)
  await waitFor(() => expect(valueInput().value).toBe(PEM))
  expect(valueInput().type).toBe('password')
  act(() => useEnvUnlockStore.getState().lock())
}

describe('EnvEditor: a value-flagged row under a harmless key (agent-os-fgwm)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    act(() => useEnvUnlockStore.getState().lock())
    useAuthStore.setState({ authDisabled: false })
    mockGetEnv.mockResolvedValue(unlockedPayload)
  })

  afterEach(() => {
    act(() => useEnvUnlockStore.getState().lock())
  })

  it('stays masked when its key is renamed to another harmless key', async () => {
    await loadUnlockedThenLock()

    fireEvent.change(keyInput(), { target: { value: 'TLS2' } })

    expect(keyInput().value).toBe('TLS2')
    expect(valueInput().type).toBe('password')
  })

  it('cannot be revealed through the Visible checkbox while locked', async () => {
    await loadUnlockedThenLock()

    fireEvent.click(visibleCheckbox())

    expect(valueInput().type).toBe('password')
    // The reveal is offered through the unlock dialog instead.
    expect(await screen.findByText('Unlock environment variables')).toBeInTheDocument()
  })

  it('a masked key-named row stays masked when renamed to a harmless key', async () => {
    await loadUnlockedThenLock()
    const key3 = () => screen.getAllByLabelText('Environment variable key 3')[0] as HTMLInputElement
    const value3 = () => screen.getAllByLabelText('Environment variable value 3')[0] as HTMLInputElement
    expect(value3().type).toBe('password')

    fireEvent.change(key3(), { target: { value: 'FOO' } })

    expect(key3().value).toBe('FOO')
    expect(value3().type).toBe('password')
  })

  // The mobile layout renders its own checkbox; every other case drives [0],
  // the desktop one.
  it('cannot be revealed through the mobile Visible checkbox while locked', async () => {
    await loadUnlockedThenLock()
    const boxes = screen.getAllByRole('checkbox', { name: 'Toggle visibility for entry 2' })
    expect(boxes).toHaveLength(2)

    fireEvent.click(boxes[1])

    for (const el of screen.getAllByLabelText('Environment variable value 2')) {
      expect((el as HTMLInputElement).type).toBe('password')
    }
    expect(await screen.findByText('Unlock environment variables')).toBeInTheDocument()
  })
})
