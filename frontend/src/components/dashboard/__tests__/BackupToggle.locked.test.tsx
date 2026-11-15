/**
 * agent-os-tp3g: the server refuses a backup policy write while env values are
 * locked, so the toggle and its stop-policy select are disabled while locked,
 * with a tooltip that says to unlock, and enabled once unlocked.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { BackupToggle } from '../BackupToggle'
import { useAuthStore } from '@/stores/authStore'
import { useEnvUnlockStore } from '@/stores/envUnlockStore'

window.HTMLElement.prototype.scrollIntoView = vi.fn()

const mockMutate = vi.fn()

vi.mock('@/hooks/useBackup', () => ({
  useToggleBackup: () => ({ mutate: mockMutate, isPending: false }),
  useBackupPolicies: () => ({
    data: {
      policies: [
        {
          id: 'policy-1',
          targetType: 'stack',
          targetId: 'stacks~myapp',
          enabled: true,
          stopPolicy: 'stop',
          createdAt: '',
          updatedAt: '',
        },
      ],
    },
  }),
  useBackupStatus: () => ({
    data: { resticAvailable: true, rcloneAvailable: true, repoState: 'ok', repoStateMessage: '', lastRun: null },
  }),
}))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), info: vi.fn(), warning: vi.fn() },
}))

describe('BackupToggle while env values are locked (agent-os-tp3g)', () => {
  beforeEach(() => {
    mockMutate.mockReset()
    useAuthStore.setState({ authDisabled: false })
    useEnvUnlockStore.getState().lock()
  })

  it('disables the switch and the stop policy, and writes nothing', () => {
    render(<BackupToggle stackId="stacks~myapp" />)

    const sw = screen.getByTestId('backup-switch-stacks~myapp')
    expect(sw).toBeDisabled()
    expect(screen.getByTestId('backup-stop-policy-stacks~myapp')).toBeDisabled()
    expect(screen.getByTestId('backup-toggle-locked-stacks~myapp')).toBeInTheDocument()
    fireEvent.click(sw)
    expect(mockMutate).not.toHaveBeenCalled()
  })

  it('enables both once unlocked', () => {
    useEnvUnlockStore.getState().unlock('tok')
    render(<BackupToggle stackId="stacks~myapp" />)

    expect(screen.getByTestId('backup-switch-stacks~myapp')).toBeEnabled()
    expect(screen.getByTestId('backup-stop-policy-stacks~myapp')).toBeEnabled()
    expect(screen.queryByTestId('backup-toggle-locked-stacks~myapp')).toBeNull()
  })
})
