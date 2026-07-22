import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react'
import { MemoryRouter, Routes, Route } from 'react-router-dom'
import type { User } from '@/types'

// Page-level test: pins SettingsPage's own contract (section routing, search
// filtering, and the page-owned password-change validation/flow) without
// exercising each settings section's internals — those have their own
// dedicated coverage, and are stubbed out here.
const changePassword = vi.fn()
const logout = vi.fn()

const mockUseAuth: {
  user: User | undefined
  logout: () => void
  authDisabled: boolean
} = {
  user: { id: 'u1', username: 'alice', createdAt: '2024-01-01T00:00:00Z' } as User,
  logout,
  authDisabled: false,
}

vi.mock('@/hooks/useAuth', () => ({
  useAuth: () => mockUseAuth,
}))

vi.mock('@/lib/api', () => ({
  authApi: {
    changePassword: (...args: unknown[]) => changePassword(...args),
  },
}))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), warning: vi.fn(), info: vi.fn() },
}))

vi.mock('@/components/settings/BackupSettingsContent', () => ({
  BackupSettingsContent: () => <div data-testid="section-backup" />,
}))
vi.mock('@/components/settings/UpdateScheduleContent', () => ({
  UpdateScheduleContent: () => <div data-testid="section-update-schedule" />,
}))
vi.mock('@/components/settings/GitSettingsContent', () => ({
  GitSettingsContent: () => <div data-testid="section-git" />,
}))
vi.mock('@/components/settings/DirectoriesSettingsContent', () => ({
  DirectoriesSettingsContent: () => <div data-testid="section-directories" />,
}))
vi.mock('@/components/settings/AuditLogContent', () => ({
  AuditLogContent: () => <div data-testid="section-audit-log" />,
}))
vi.mock('@/components/settings/GlobalEnvSettingsContent', () => ({
  GlobalEnvSettingsContent: () => <div data-testid="section-global-env" />,
}))

import { SettingsPage } from '../SettingsPage'

function renderPage(route = '/settings') {
  return render(
    <MemoryRouter initialEntries={[route]}>
      <Routes>
        <Route path="/settings" element={<SettingsPage />} />
        <Route path="/settings/:section" element={<SettingsPage />} />
      </Routes>
    </MemoryRouter>,
  )
}

describe('SettingsPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockUseAuth.user = { id: 'u1', username: 'alice', createdAt: '2024-01-01T00:00:00Z' } as User
    mockUseAuth.authDisabled = false
  })

  describe('section routing', () => {
    it('renders the Account Security section by default', () => {
      renderPage('/settings')
      expect(screen.getByRole('heading', { name: 'Account Information' })).toBeInTheDocument()
      expect(screen.queryByTestId('section-backup')).not.toBeInTheDocument()
    })

    it('renders the section named by the :section route param', () => {
      renderPage('/settings/backup')
      expect(screen.getByTestId('section-backup')).toBeInTheDocument()
      expect(screen.queryByRole('heading', { name: 'Account Information' })).not.toBeInTheDocument()
    })

    it('falls back to Account Security for an unknown section id', () => {
      // SettingsPage.tsx:128 — activeId only accepts a known section id, else DEFAULT_SECTION.
      renderPage('/settings/does-not-exist')
      expect(screen.getByRole('heading', { name: 'Account Information' })).toBeInTheDocument()
    })

    it('filters the sidebar to matching sections when searching', () => {
      renderPage('/settings')
      fireEvent.change(screen.getByLabelText('Search settings'), { target: { value: 'backup' } })

      expect(screen.getByRole('link', { name: /Backup/ })).toBeInTheDocument()
      expect(screen.queryByRole('link', { name: /Appearance/ })).not.toBeInTheDocument()
    })

    it('shows a no-results message when the search matches nothing', () => {
      renderPage('/settings')
      fireEvent.change(screen.getByLabelText('Search settings'), { target: { value: 'zzz-nope' } })

      expect(screen.getByText('No settings match “zzz-nope”.')).toBeInTheDocument()
    })
  })

  describe('loading state (page-owned analog)', () => {
    // SettingsPage itself has no page-level isLoading branch — loading is owned
    // by each section's own data hook (e.g. BackupSettingsContent's
    // useBackupSettings), which this suite intentionally stubs out. The closest
    // page-owned "loading" behavior is that the Account section renders safely
    // with an empty username input while `user` from useAuth is still
    // undefined (SettingsPage.tsx:206), instead of crashing.
    it('renders the username field empty when user is not yet loaded', () => {
      mockUseAuth.user = undefined
      renderPage('/settings')

      const usernameInput = screen.getByLabelText('Username') as HTMLInputElement
      expect(usernameInput.value).toBe('')
      expect(usernameInput).toBeDisabled()
    })

    it('shows the authDisabled notice instead of account fields when auth is disabled', () => {
      mockUseAuth.authDisabled = true
      renderPage('/settings')

      expect(screen.getByText('Authentication is disabled')).toBeInTheDocument()
      expect(screen.queryByLabelText('Username')).not.toBeInTheDocument()
    })
  })

  describe('error path: password change', () => {
    it('rejects a too-short new password without opening the confirm dialog', async () => {
      renderPage('/settings')

      fireEvent.change(screen.getByLabelText('Current Password'), { target: { value: 'oldpass1' } })
      fireEvent.change(screen.getByLabelText('New Password'), { target: { value: 'short' } })
      fireEvent.change(screen.getByLabelText('Confirm Password'), { target: { value: 'short' } })
      fireEvent.click(screen.getByRole('button', { name: 'Change Password' }))

      const { toast } = await import('sonner')
      expect(toast.error).toHaveBeenCalledWith('Password must be at least 8 characters')
      expect(screen.queryByText('Confirm Password Change')).not.toBeInTheDocument()
      expect(changePassword).not.toHaveBeenCalled()
    })

    it('rejects a mismatched confirmation without opening the confirm dialog', async () => {
      renderPage('/settings')

      fireEvent.change(screen.getByLabelText('Current Password'), { target: { value: 'oldpass1' } })
      fireEvent.change(screen.getByLabelText('New Password'), { target: { value: 'newpassword' } })
      fireEvent.change(screen.getByLabelText('Confirm Password'), { target: { value: 'different1' } })
      fireEvent.click(screen.getByRole('button', { name: 'Change Password' }))

      const { toast } = await import('sonner')
      expect(toast.error).toHaveBeenCalledWith('Passwords do not match')
      expect(screen.queryByText('Confirm Password Change')).not.toBeInTheDocument()
      expect(changePassword).not.toHaveBeenCalled()
    })

    it('calls the API and logs out on a confirmed successful password change', async () => {
      changePassword.mockResolvedValue(undefined)
      renderPage('/settings')

      fireEvent.change(screen.getByLabelText('Current Password'), { target: { value: 'oldpass1' } })
      fireEvent.change(screen.getByLabelText('New Password'), { target: { value: 'newpassword' } })
      fireEvent.change(screen.getByLabelText('Confirm Password'), { target: { value: 'newpassword' } })
      fireEvent.click(screen.getByRole('button', { name: 'Change Password' }))

      await waitFor(() => expect(screen.getByText('Confirm Password Change')).toBeInTheDocument())
      // act() flushes the async handler's promise chain (including the `finally`
      // that runs after the awaited API call) so its state update isn't left
      // dangling outside of React's test-managed batch.
      await act(async () => {
        fireEvent.click(screen.getByRole('button', { name: 'Confirm' }))
      })

      expect(changePassword).toHaveBeenCalledWith('oldpass1', 'newpassword')
      expect(logout).toHaveBeenCalled()
    })

    it('closes the dialog without logging out or clearing fields when the API rejects', async () => {
      // SettingsPage.tsx:175-178 — the catch branch toasts and closes the
      // dialog, but (unlike the success branch at 169-174) does NOT call
      // logout() and does NOT clear currentPassword/newPassword/confirmPassword.
      changePassword.mockRejectedValue({ response: { data: { error: 'Current password is wrong' } } })
      renderPage('/settings')

      fireEvent.change(screen.getByLabelText('Current Password'), { target: { value: 'wrongpass' } })
      fireEvent.change(screen.getByLabelText('New Password'), { target: { value: 'newpassword' } })
      fireEvent.change(screen.getByLabelText('Confirm Password'), { target: { value: 'newpassword' } })
      fireEvent.click(screen.getByRole('button', { name: 'Change Password' }))

      await waitFor(() => expect(screen.getByText('Confirm Password Change')).toBeInTheDocument())
      await act(async () => {
        fireEvent.click(screen.getByRole('button', { name: 'Confirm' }))
      })

      const { toast } = await import('sonner')
      expect(toast.error).toHaveBeenCalledWith('Current password is wrong')
      expect(screen.queryByText('Confirm Password Change')).not.toBeInTheDocument()
      expect(logout).not.toHaveBeenCalled()
      expect((screen.getByLabelText('Current Password') as HTMLInputElement).value).toBe('wrongpass')
      expect((screen.getByLabelText('New Password') as HTMLInputElement).value).toBe('newpassword')
      expect((screen.getByLabelText('Confirm Password') as HTMLInputElement).value).toBe('newpassword')
    })
  })
})
