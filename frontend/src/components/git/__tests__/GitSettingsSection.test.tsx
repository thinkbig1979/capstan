import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import { renderWithProviders } from '../../../test/utils'

const mockList = vi.fn().mockResolvedValue([])
const mockCredentialStatus = vi.fn()

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), warning: vi.fn() },
}))

vi.mock('@/lib/api', () => ({
  directoriesApi: {
    list: (...args: unknown[]) => mockList(...args),
    updateCredentials: vi.fn(),
    credentialStatus: (...args: unknown[]) => mockCredentialStatus(...args),
  },
}))

import { GitSettingsSection } from '../GitSettingsSection'

// These tests cover the agent-os-8a5 UI: the credential-status probe result
// (unreadable / empty / ok / none, see directories.go's CredentialStatus)
// surfaced once the "Git Credentials" disclosure is expanded — it is fetched
// inside the same open-gated effect that loads the directory's saved
// settings, not unconditionally on mount (finding J).
describe('GitSettingsSection', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockList.mockResolvedValue([])
  })

  it('does not probe credential status while collapsed', () => {
    renderWithProviders(
      <GitSettingsSection directoryPath="/opt/stacks/app" open={false} onToggle={() => {}} />
    )
    expect(mockCredentialStatus).not.toHaveBeenCalled()
  })

  it('shows a warning when the stored credential cannot be decrypted', async () => {
    mockCredentialStatus.mockResolvedValue({ path: '/opt/stacks/app', status: 'unreadable' })
    renderWithProviders(
      <GitSettingsSection directoryPath="/opt/stacks/app" open={true} onToggle={() => {}} />
    )
    await waitFor(() => expect(mockCredentialStatus).toHaveBeenCalledWith('/opt/stacks/app'))
    expect(await screen.findByText(/can't be decrypted/i)).toBeInTheDocument()
  })

  it('shows a warning when https auth is selected but no token is saved', async () => {
    mockList.mockResolvedValue([
      { path: '/opt/stacks/app', name: 'app', isDefault: false, gitAuthType: 'https', hasHttpsToken: false },
    ])
    mockCredentialStatus.mockResolvedValue({ path: '/opt/stacks/app', status: 'empty' })
    renderWithProviders(
      <GitSettingsSection directoryPath="/opt/stacks/app" open={true} onToggle={() => {}} />
    )
    expect(await screen.findByText(/no token is saved/i)).toBeInTheDocument()
  })

  it('shows no warning when the credential is ok', async () => {
    mockCredentialStatus.mockResolvedValue({ path: '/opt/stacks/app', status: 'ok' })
    renderWithProviders(
      <GitSettingsSection directoryPath="/opt/stacks/app" open={true} onToggle={() => {}} />
    )
    await waitFor(() => expect(mockCredentialStatus).toHaveBeenCalledWith('/opt/stacks/app'))
    expect(screen.queryByText(/can't be decrypted/i)).not.toBeInTheDocument()
    expect(screen.queryByText(/no token is saved/i)).not.toBeInTheDocument()
  })

  it('shows no warning when there is no credential configured', async () => {
    mockCredentialStatus.mockResolvedValue({ path: '/opt/stacks/app', status: 'none' })
    renderWithProviders(
      <GitSettingsSection directoryPath="/opt/stacks/app" open={true} onToggle={() => {}} />
    )
    await waitFor(() => expect(mockCredentialStatus).toHaveBeenCalledWith('/opt/stacks/app'))
    expect(screen.queryByText(/can't be decrypted/i)).not.toBeInTheDocument()
    expect(screen.queryByText(/no token is saved/i)).not.toBeInTheDocument()
  })
})
