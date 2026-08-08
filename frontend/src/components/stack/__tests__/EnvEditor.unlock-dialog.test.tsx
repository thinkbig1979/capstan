/**
 * Coverage for EnvEditor's wiring of the unlock *dialog* itself — as opposed
 * to EnvEditor.unlock.test.tsx, which covers the store-driven
 * unlock/relock/re-mask transition and treats the dialog as opaque. This file
 * exercises: opening the dialog on reveal when locked, Cancel leaving the
 * entry masked, a successful password submission revealing the pending
 * entry, and the authDisabled bypass that skips the dialog entirely.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { screen, waitFor, act } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithProviders } from '../../../test/utils'
import { useEnvUnlockStore } from '@/stores/envUnlockStore'
import { useAuthStore } from '@/stores/authStore'

// ─── Mocks ────────────────────────────────────────────────────────────────────

const mockGetEnv = vi.fn()
const mockVerifyPassword = vi.fn()

vi.mock('@/lib/api', () => ({
  stacksApi: {
    getEnv: (...args: unknown[]) => mockGetEnv(...args),
    updateEnv: vi.fn(),
    createEnv: vi.fn(),
    updateComposeAndEnv: vi.fn(),
  },
  authApi: {
    verifyPassword: (...args: unknown[]) => mockVerifyPassword(...args),
  },
  apiClient: {
    get: vi.fn(),
    put: vi.fn(),
    post: vi.fn(),
  },
}))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), warning: vi.fn(), info: vi.fn() },
}))

import { EnvEditor } from '../EnvEditor'

// ─── Helpers ─────────────────────────────────────────────────────────────────

const envDataWithSensitive = {
  filename: '.env',
  entries: [{ key: 'API_KEY', value: 'secret-value', sensitive: true, comment: false, line: 1 }],
  raw: 'API_KEY=secret-value\n',
}

// The toggle-visibility button renders once for desktop and once for the
// mobile card; either instance drives the same handler.
function getRevealButton() {
  return screen.getAllByRole('button', { name: 'Toggle visibility for entry 1' })[0]
}

describe('EnvEditor unlock dialog wiring', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    act(() => {
      useEnvUnlockStore.getState().lock()
    })
    useAuthStore.setState({ authDisabled: false })
  })

  afterEach(() => {
    act(() => {
      useEnvUnlockStore.getState().lock()
    })
    useAuthStore.setState({ authDisabled: false })
  })

  it('reveal on a locked, auth-enabled entry opens the unlock dialog instead of revealing it', async () => {
    const user = userEvent.setup()
    mockGetEnv.mockResolvedValue(envDataWithSensitive)
    renderWithProviders(<EnvEditor stackId="test-stack" />)

    await waitFor(() => {
      expect(screen.getAllByDisplayValue('secret-value').length).toBeGreaterThan(0)
    })

    await user.click(getRevealButton())

    expect(await screen.findByText('Unlock environment variables')).toBeInTheDocument()
    // Still masked — the dialog hasn't been completed yet.
    ;(screen.getAllByDisplayValue('secret-value') as HTMLInputElement[]).forEach((input) =>
      expect(input.type).toBe('password'),
    )
  })

  it('Cancel closes the dialog without revealing or unlocking the session', async () => {
    const user = userEvent.setup()
    mockGetEnv.mockResolvedValue(envDataWithSensitive)
    renderWithProviders(<EnvEditor stackId="test-stack" />)

    await waitFor(() => {
      expect(screen.getAllByDisplayValue('secret-value').length).toBeGreaterThan(0)
    })
    await user.click(getRevealButton())
    await screen.findByText('Unlock environment variables')

    await user.click(screen.getByRole('button', { name: 'Cancel' }))

    await waitFor(() => {
      expect(screen.queryByText('Unlock environment variables')).not.toBeInTheDocument()
    })
    ;(screen.getAllByDisplayValue('secret-value') as HTMLInputElement[]).forEach((input) =>
      expect(input.type).toBe('password'),
    )
    expect(useEnvUnlockStore.getState().isUnlocked()).toBe(false)
  })

  it('a correct password unlocks the session and reveals the pending entry', async () => {
    const user = userEvent.setup()
    mockGetEnv.mockResolvedValue(envDataWithSensitive)
    mockVerifyPassword.mockResolvedValue({ ok: true, unlockToken: 'test-unlock-token', expiresIn: 300 })
    renderWithProviders(<EnvEditor stackId="test-stack" />)

    await waitFor(() => {
      expect(screen.getAllByDisplayValue('secret-value').length).toBeGreaterThan(0)
    })
    await user.click(getRevealButton())
    await screen.findByText('Unlock environment variables')

    await user.type(screen.getByLabelText('Password'), 'correct-password')
    await user.click(screen.getByRole('button', { name: 'Unlock' }))

    await waitFor(() => {
      ;(screen.getAllByDisplayValue('secret-value') as HTMLInputElement[]).forEach((input) =>
        expect(input.type).toBe('text'),
      )
    })
    expect(mockVerifyPassword).toHaveBeenCalledWith('correct-password')
    expect(useEnvUnlockStore.getState().isUnlocked()).toBe(true)
  })

  it('with auth disabled, reveal bypasses the dialog entirely', async () => {
    const user = userEvent.setup()
    useAuthStore.setState({ authDisabled: true })
    mockGetEnv.mockResolvedValue(envDataWithSensitive)
    renderWithProviders(<EnvEditor stackId="test-stack" />)

    await waitFor(() => {
      expect(screen.getAllByDisplayValue('secret-value').length).toBeGreaterThan(0)
    })

    await user.click(getRevealButton())

    await waitFor(() => {
      ;(screen.getAllByDisplayValue('secret-value') as HTMLInputElement[]).forEach((input) =>
        expect(input.type).toBe('text'),
      )
    })
    expect(screen.queryByText('Unlock environment variables')).not.toBeInTheDocument()
  })
})
