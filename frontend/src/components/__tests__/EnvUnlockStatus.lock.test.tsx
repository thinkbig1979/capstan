/**
 * agent-os-qxio: the Lock button must tell the server to revoke the unlock
 * token, not only drop the browser's copy, and must still lock this tab when
 * that call fails.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { useEnvUnlockStore } from '@/stores/envUnlockStore'

const mockLockEnv = vi.fn()

vi.mock('@/lib/api', () => ({
  authApi: {
    lockEnv: (...args: unknown[]) => mockLockEnv(...args),
  },
}))

const mockWarning = vi.fn()
vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), warning: (...a: unknown[]) => mockWarning(...a), info: vi.fn() },
}))

import { EnvUnlockStatus } from '../EnvUnlockStatus'

describe('EnvUnlockStatus Lock button (agent-os-qxio)', () => {
  beforeEach(() => {
    mockLockEnv.mockReset()
    mockWarning.mockReset()
    useEnvUnlockStore.getState().lock()
    useEnvUnlockStore.getState().unlock('tok-1')
  })

  it('revokes the token on the server, then clears it here', async () => {
    let resolve: () => void = () => {}
    mockLockEnv.mockReturnValue(new Promise<void>((r) => { resolve = r }))
    render(<EnvUnlockStatus />)

    fireEvent.click(screen.getByRole('button', { name: 'Lock environment variables now' }))

    expect(mockLockEnv).toHaveBeenCalledTimes(1)
    resolve()
    await waitFor(() => expect(useEnvUnlockStore.getState().token).toBeNull())
    expect(useEnvUnlockStore.getState().unlockedUntil).toBeNull()
    expect(mockWarning).not.toHaveBeenCalled()
  })

  it('still locks this tab and warns when the server call fails', async () => {
    mockLockEnv.mockRejectedValue(new Error('network down'))
    render(<EnvUnlockStatus />)

    fireEvent.click(screen.getByRole('button', { name: 'Lock environment variables now' }))

    await waitFor(() => expect(useEnvUnlockStore.getState().token).toBeNull())
    expect(useEnvUnlockStore.getState().unlockedUntil).toBeNull()
    expect(mockWarning).toHaveBeenCalledWith(
      'Locked here; the server could not be told, so the unlock may stay valid for up to 5 minutes',
    )
  })
})
