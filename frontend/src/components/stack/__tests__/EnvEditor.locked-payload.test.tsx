/**
 * Coverage for how EnvEditor handles a *redacted* payload — the state the
 * backend now returns to any session that has not re-entered its password
 * (agent-os-7o5s). The other unlock suites all feed it a full payload, so none
 * of them could see what the editor does when `locked: true` arrives with the
 * sensitive values blanked and no `raw` field at all.
 *
 * The risk being pinned down: saving from that state would write the blanks back
 * over the real secrets. The backend refuses the write, but the UI must not
 * offer it either.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { screen, waitFor, act } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithProviders } from '../../../test/utils'
import { useEnvUnlockStore } from '@/stores/envUnlockStore'
import { useAuthStore } from '@/stores/authStore'

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
  apiClient: { get: vi.fn(), put: vi.fn(), post: vi.fn() },
}))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), warning: vi.fn(), info: vi.fn() },
}))

import { EnvEditor } from '../EnvEditor'

/** What the backend sends without a valid unlock token: blanks, and no `raw`. */
const lockedPayload = {
  filename: '.env',
  locked: true,
  entries: [
    { key: 'API_KEY', value: '', sensitive: true, comment: false, line: 1 },
    { key: 'TZ', value: 'Europe/Amsterdam', sensitive: false, comment: false, line: 2 },
  ],
}

/** What it sends once the token is in play. */
const unlockedPayload = {
  filename: '.env',
  entries: [
    { key: 'API_KEY', value: 'secret-value', sensitive: true, comment: false, line: 1 },
    { key: 'TZ', value: 'Europe/Amsterdam', sensitive: false, comment: false, line: 2 },
  ],
  raw: 'API_KEY=secret-value\nTZ=Europe/Amsterdam\n',
}

function saveButtons() {
  return screen.getAllByRole('button', { name: /^Save$/ })
}

describe('EnvEditor with a redacted (locked) payload', () => {
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

  it('explains that values are hidden and offers to unlock', async () => {
    mockGetEnv.mockResolvedValue(lockedPayload)
    renderWithProviders(<EnvEditor stackId="test-stack" />)

    expect(
      await screen.findByText(/Values that look like secrets are hidden and editing is disabled/),
    ).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Unlock' })).toBeInTheDocument()
  })

  it('disables Save, so the blanks it was handed cannot be written back', async () => {
    mockGetEnv.mockResolvedValue(lockedPayload)
    renderWithProviders(<EnvEditor stackId="test-stack" />)

    await waitFor(() => expect(saveButtons().length).toBeGreaterThan(0))
    saveButtons().forEach((button) => expect(button).toBeDisabled())
  })

  it('hides the Raw Editor tab, which would otherwise show an empty file', async () => {
    mockGetEnv.mockResolvedValue(lockedPayload)
    renderWithProviders(<EnvEditor stackId="test-stack" />)

    await waitFor(() => expect(screen.getByRole('tab', { name: 'Table View' })).toBeInTheDocument())
    expect(screen.queryByRole('tab', { name: 'Raw Editor' })).not.toBeInTheDocument()
  })

  it('still shows the keys and the non-secret values', async () => {
    mockGetEnv.mockResolvedValue(lockedPayload)
    renderWithProviders(<EnvEditor stackId="test-stack" />)

    await waitFor(() =>
      expect(screen.getAllByDisplayValue('API_KEY').length).toBeGreaterThan(0),
    )
    expect(screen.getAllByDisplayValue('Europe/Amsterdam').length).toBeGreaterThan(0)
  })

  it('unlocking refetches and re-enables editing with the real values', async () => {
    const user = userEvent.setup()
    // First load is redacted; the refetch that unlocking triggers is not.
    mockGetEnv.mockResolvedValueOnce(lockedPayload).mockResolvedValue(unlockedPayload)
    mockVerifyPassword.mockResolvedValue({ ok: true, unlockToken: 'minted', expiresIn: 300 })

    renderWithProviders(<EnvEditor stackId="test-stack" />)

    await screen.findByRole('button', { name: 'Unlock' })
    await user.click(screen.getByRole('button', { name: 'Unlock' }))

    await screen.findByText('Unlock environment variables')
    await user.type(screen.getByLabelText('Password'), 'correct-password')
    await user.click(screen.getByRole('button', { name: 'Unlock' }))

    // The token the server minted is what later requests must carry.
    await waitFor(() => expect(useEnvUnlockStore.getState().token).toBe('minted'))

    // The refetched payload is complete, so the notice goes and the Raw tab returns.
    await waitFor(() => {
      expect(
        screen.queryByText(/Values that look like secrets are hidden/),
      ).not.toBeInTheDocument()
    })
    expect(screen.getByRole('tab', { name: 'Raw Editor' })).toBeInTheDocument()
    expect(screen.getAllByDisplayValue('secret-value').length).toBeGreaterThan(0)
  })

  it('drops the token when the window is locked again', async () => {
    mockGetEnv.mockResolvedValue(lockedPayload)
    renderWithProviders(<EnvEditor stackId="test-stack" />)
    await screen.findByRole('button', { name: 'Unlock' })

    act(() => {
      useEnvUnlockStore.getState().unlock('minted')
    })
    expect(useEnvUnlockStore.getState().token).toBe('minted')

    act(() => {
      useEnvUnlockStore.getState().lock()
    })
    expect(useEnvUnlockStore.getState().token).toBeNull()
  })
})
