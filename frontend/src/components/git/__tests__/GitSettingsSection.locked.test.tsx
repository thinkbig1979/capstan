import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen } from '@testing-library/react'
import { renderWithProviders } from '../../../test/utils'
import { useEnvUnlockStore } from '@/stores/envUnlockStore'
import { useAuthStore } from '@/stores/authStore'

const mockList = vi.fn()
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

// agent-os-mu4z: while env values are locked the server refuses every change
// to a folder's git credentials, so the method, key, username, token and
// Save are disabled with the unlock notice. The push switch has its own
// route and stays usable.
describe('GitSettingsSection while env values are locked', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    useAuthStore.setState({ authDisabled: false })
    useEnvUnlockStore.getState().lock()
    mockList.mockResolvedValue([
      { path: '/opt/stacks/app', name: 'app', isDefault: false, gitAuthType: 'https', gitHttpsUser: 'owner', hasHttpsToken: true, gitPushEnabled: false },
    ])
    mockCredentialStatus.mockResolvedValue({ path: '/opt/stacks/app', status: 'ok' })
    mockUpdateCredentials.mockResolvedValue({})
  })

  it('disables the credential controls and Save, and offers an unlock', async () => {
    renderWithProviders(<GitSettingsSection directoryPath="/opt/stacks/app" open={true} onToggle={() => {}} />)
    expect(await screen.findByDisplayValue('owner')).toBeDisabled()
    expect(screen.getByPlaceholderText('Leave blank to keep current')).toBeDisabled()
    expect(screen.getByRole('combobox', { name: 'Authentication method' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Save Credentials' })).toBeDisabled()
    expect(screen.getByRole('status')).toHaveTextContent("Env values are locked. Changing this folder's git credentials needs an unlock")
    expect(screen.getByRole('button', { name: 'Unlock' })).toBeInTheDocument()
    expect(screen.getByRole('switch')).toBeEnabled()
  })

  it('unlocked, the credential controls are usable and no notice shows', async () => {
    useEnvUnlockStore.getState().unlock('t')
    renderWithProviders(<GitSettingsSection directoryPath="/opt/stacks/app" open={true} onToggle={() => {}} />)
    expect(await screen.findByDisplayValue('owner')).toBeEnabled()
    expect(screen.getByRole('button', { name: 'Save Credentials' })).toBeEnabled()
    expect(screen.queryByText(/Env values are locked/)).toBeNull()
  })
})
