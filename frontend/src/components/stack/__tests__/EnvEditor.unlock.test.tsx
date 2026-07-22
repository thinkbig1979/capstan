/**
 * Coverage for the unlock/relock transition in EnvEditor: when the env-unlock
 * session ends (manual "Lock" click or auto-expiry), previously revealed
 * sensitive-by-name entries must be re-masked, and non-sensitive entries must
 * be left alone. See EnvEditor.tsx's prevUnlockedUntil render-time comparison.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { screen, waitFor, act, fireEvent } from '@testing-library/react'
import { renderWithProviders } from '../../../test/utils'
import { useEnvUnlockStore, UNLOCK_DURATION_MS } from '@/stores/envUnlockStore'

// ─── Mocks ────────────────────────────────────────────────────────────────────

const mockGetEnv = vi.fn()

vi.mock('@/lib/api', () => ({
  stacksApi: {
    getEnv: (...args: unknown[]) => mockGetEnv(...args),
    updateEnv: vi.fn(),
    createEnv: vi.fn(),
    updateComposeAndEnv: vi.fn(),
  },
  authApi: {
    verifyPassword: vi.fn(),
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
  entries: [
    { key: 'API_KEY', value: 'secret-value', sensitive: true, comment: false, line: 1 },
    { key: 'PORT', value: '8080', sensitive: false, comment: false, line: 2 },
  ],
  raw: 'API_KEY=secret-value\nPORT=8080\n',
}

async function renderUnlockedEditor() {
  mockGetEnv.mockResolvedValue(envDataWithSensitive)
  renderWithProviders(<EnvEditor stackId="test-stack" />)

  await waitFor(() => {
    expect(screen.getAllByDisplayValue('secret-value').length).toBeGreaterThan(0)
  })
}

// The "Toggle visibility for entry N" aria-label is shared by two distinct
// elements per row: the eye/reveal button (only rendered while sensitive is
// true) and the "Visible" checkbox (always rendered). Scope to role=button so
// we exercise the actual reveal affordance rather than the checkbox.
function getToggleButtons() {
  return screen.queryAllByRole('button', { name: 'Toggle visibility for entry 1' })
}

function revealFirstSensitiveEntry() {
  fireEvent.click(getToggleButtons()[0])
}

function getSensitiveValueInputs() {
  return screen.getAllByDisplayValue('secret-value') as HTMLInputElement[]
}

function getPortValueInputs() {
  return screen.getAllByDisplayValue('8080') as HTMLInputElement[]
}

// ─── Suite ───────────────────────────────────────────────────────────────────

describe('EnvEditor unlock/relock transition', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    act(() => {
      useEnvUnlockStore.getState().lock()
    })
  })

  afterEach(() => {
    act(() => {
      useEnvUnlockStore.getState().lock()
    })
    vi.useRealTimers()
  })

  it('starts masked: sensitive entry renders as a password input', async () => {
    await renderUnlockedEditor()

    getSensitiveValueInputs().forEach((input) => {
      expect(input.type).toBe('password')
    })
  })

  it('reveal while unlocked, then manual relock (Lock button) re-masks the sensitive entry', async () => {
    await renderUnlockedEditor()

    act(() => {
      useEnvUnlockStore.getState().unlock()
    })

    // Reveal — since the session is unlocked, this flips sensitive:false directly
    // without going through the password dialog.
    revealFirstSensitiveEntry()

    getSensitiveValueInputs().forEach((input) => {
      expect(input.type).toBe('text')
    })
    // Revealed entries drop the eye toggle entirely (see EnvEditor's
    // sensitive-branch rendering), so the affordance should be gone too.
    expect(getToggleButtons()).toHaveLength(0)

    // Manually relock via the real "Lock" button rendered by EnvUnlockStatus,
    // exercising the full UI path rather than calling the store directly.
    const lockButton = screen.getByRole('button', { name: 'Lock environment variables now' })
    fireEvent.click(lockButton)

    getSensitiveValueInputs().forEach((input) => {
      expect(input.type).toBe('password')
    })
    // The toggle affordance reappears once re-masked.
    expect(getToggleButtons().length).toBeGreaterThan(0)
  })

  it('non-sensitive entries are unaffected by unlock, reveal, and relock', async () => {
    await renderUnlockedEditor()

    const initialPortInputs = getPortValueInputs()
    initialPortInputs.forEach((input) => expect(input.type).toBe('text'))

    act(() => {
      useEnvUnlockStore.getState().unlock()
    })
    revealFirstSensitiveEntry()

    await waitFor(() => {
      getSensitiveValueInputs().forEach((input) => expect(input.type).toBe('text'))
    })
    getPortValueInputs().forEach((input) => {
      expect(input.type).toBe('text')
      expect(input.value).toBe('8080')
    })

    act(() => {
      useEnvUnlockStore.getState().lock()
    })

    await waitFor(() => {
      getSensitiveValueInputs().forEach((input) => expect(input.type).toBe('password'))
    })
    getPortValueInputs().forEach((input) => {
      expect(input.type).toBe('text')
      expect(input.value).toBe('8080')
    })
  })

  it('auto-expiry of the unlock session re-masks the revealed entry without a manual lock', async () => {
    await renderUnlockedEditor()

    vi.useFakeTimers()
    act(() => {
      useEnvUnlockStore.getState().unlock()
    })
    revealFirstSensitiveEntry()

    getSensitiveValueInputs().forEach((input) => expect(input.type).toBe('text'))

    act(() => {
      vi.advanceTimersByTime(UNLOCK_DURATION_MS)
    })

    getSensitiveValueInputs().forEach((input) => expect(input.type).toBe('password'))
    expect(useEnvUnlockStore.getState().isUnlocked()).toBe(false)
  })
})
