import { describe, it, expect, vi, beforeEach } from 'vitest'
import { fireEvent, screen, waitFor } from '@testing-library/react'
import { toast } from 'sonner'
import { renderWithProviders } from '../../../test/utils'

const mockList = vi.fn().mockResolvedValue([])
const mockCredentialStatus = vi.fn()
const mockUpdateCredentials = vi.fn()

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), warning: vi.fn() },
}))

vi.mock('@/lib/api', () => ({
  directoriesApi: {
    list: (...args: unknown[]) => mockList(...args),
    updateCredentials: (...args: unknown[]) => mockUpdateCredentials(...args),
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
    mockCredentialStatus.mockResolvedValue({ path: '/opt/stacks/app', status: 'ok' })
    mockUpdateCredentials.mockResolvedValue({})
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

  // The save-mutation error path (agent-os-82lk). PUT /directories/credentials
  // answers with four wire codes; two of them tell the operator something the
  // UI's own sentence cannot, and the onError used to be zero-arity and throw
  // all four away. agent-os-p7r records a real production 404 that sat behind
  // this exact sentence for months.
  describe('save failures', () => {
    async function submitSave() {
      renderWithProviders(
        <GitSettingsSection directoryPath="/opt/stacks/app" open={true} onToggle={() => {}} />
      )
      const save = await screen.findByRole('button', { name: /save credentials/i })
      fireEvent.click(save)
      await waitFor(() => expect(mockUpdateCredentials).toHaveBeenCalled())
    }

    it('names the missing directory on a 404 NOT_FOUND', async () => {
      mockUpdateCredentials.mockRejectedValue({
        status: 404,
        code: 'NOT_FOUND',
        message: 'Directory not found',
      })

      await submitSave()

      await waitFor(() =>
        expect(toast.error).toHaveBeenCalledWith('Failed to save credentials', {
          description: 'Directory not found',
        })
      )
    })

    it('names the missing encryption key, and its recovery, on a 422 ENCRYPTION_KEY_MISSING', async () => {
      // The WIRE value, not the Go identifier: the constant is
      // models.ErrEncryptionUnavailable and its value is this string. Keying on
      // the identifier would silently match nothing.
      const message =
        'Cannot store this value: no encryption key is configured. Set STORAGE_KEY (or JWT_SECRET) in the environment and restart Capstan, then try again.'
      mockUpdateCredentials.mockRejectedValue({
        status: 422,
        code: 'ENCRYPTION_KEY_MISSING',
        message,
      })

      await submitSave()

      await waitFor(() =>
        expect(toast.error).toHaveBeenCalledWith('Failed to save credentials', { description: message })
      )
    })

    // The negative arm, and it is a REGRESSION GUARD rather than a fail-first
    // one: before this change the onError rendered the bare sentence for every
    // error, so this assertion was already green. It is here because the
    // allow-list is a SECURITY bound on this endpoint — it takes a git token —
    // and this pins that a code outside the list surfaces no server text at
    // all. VALIDATION_ERROR is genuinely producible here: UpdateCredentials
    // mints it on a bad authType (directories.go, inside UpdateCredentials).
    it('keeps the generic sentence for a code outside the allow-list', async () => {
      mockUpdateCredentials.mockRejectedValue({
        status: 400,
        code: 'VALIDATION_ERROR',
        message: "authType must be 'ssh', 'https', 'inherit', or empty",
      })

      await submitSave()

      await waitFor(() => expect(toast.error).toHaveBeenCalled())
      // Asserted on the RENDERED arguments rather than with
      // toHaveBeenCalledWith(..., undefined), which would key on the callback's
      // ARITY: before this change onError called toast.error with one argument,
      // so that form went red at the base commit for a reason that has nothing
      // to do with what the operator sees. This one is green before and after,
      // which is what a regression guard should be.
      const [title, options] = vi.mocked(toast.error).mock.calls[0] as [
        string,
        { description?: string } | undefined,
      ]
      expect(title).toBe('Failed to save credentials')
      expect(options?.description).toBeUndefined()
    })
  })
})
