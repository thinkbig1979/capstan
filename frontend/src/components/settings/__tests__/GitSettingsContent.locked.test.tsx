import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { GitSettingsContent } from '../GitSettingsContent'
import { useEnvUnlockStore } from '@/stores/envUnlockStore'
import { useAuthStore } from '@/stores/authStore'

/**
 * agent-os-mu4z: while env values are locked the server refuses a change to
 * the SSH key, HTTPS user or token, even one that resends the stored value.
 * So the three are disabled with the unlock notice beside them, the author
 * stays editable, and a save sends only what was edited.
 */

const mockGetGit = vi.fn()
const mockUpdateGit = vi.fn()

vi.mock('@/lib/api', () => ({
  settingsApi: {
    getGit: (...args: unknown[]) => mockGetGit(...args),
    updateGit: (...args: unknown[]) => mockUpdateGit(...args),
  },
}))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), info: vi.fn(), warning: vi.fn() },
}))

function renderPanel() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  )
  return render(<GitSettingsContent />, { wrapper })
}

beforeEach(() => {
  vi.clearAllMocks()
  useAuthStore.setState({ authDisabled: false })
  useEnvUnlockStore.getState().lock()
  mockGetGit.mockResolvedValue({
    sshKey: '/keys/stored', httpsUser: 'stored-user', hasHttpsToken: true, httpsTokenUnreadable: false,
    authorName: 'Stored Name', authorEmail: 'stored@example.invalid',
  })
  mockUpdateGit.mockResolvedValue({})
})

describe('GitSettingsContent while env values are locked', () => {
  it('disables the SSH key, HTTPS user and token, keeps the author editable, and offers an unlock', async () => {
    renderPanel()
    expect(await screen.findByLabelText('SSH Private Key Path')).toBeDisabled()
    expect(screen.getByLabelText('Username')).toBeDisabled()
    expect(screen.getByLabelText(/Personal Access Token/)).toBeDisabled()
    expect(screen.getByLabelText('Author name')).toBeEnabled()
    expect(screen.getByLabelText('Author email')).toBeEnabled()
    expect(screen.getByRole('status')).toHaveTextContent('Env values are locked')
    expect(screen.getByRole('button', { name: 'Unlock' })).toBeInTheDocument()
  })

  it('an author-only save sends only the author, never the stored credentials', async () => {
    const user = userEvent.setup()
    renderPanel()
    const name = await screen.findByLabelText('Author name')
    await user.clear(name)
    await user.type(name, 'New Name')
    await user.click(screen.getByRole('button', { name: 'Save Git Settings' }))
    await waitFor(() => expect(mockUpdateGit).toHaveBeenCalledTimes(1))
    expect(mockUpdateGit).toHaveBeenCalledWith({ authorName: 'New Name' })
  })

  it('unlocked, the credential fields are editable and no notice shows', async () => {
    useEnvUnlockStore.getState().unlock('t')
    renderPanel()
    expect(await screen.findByLabelText('SSH Private Key Path')).toBeEnabled()
    expect(screen.getByLabelText('Username')).toBeEnabled()
    expect(screen.getByLabelText(/Personal Access Token/)).toBeEnabled()
    expect(screen.queryByText(/Env values are locked/)).toBeNull()
  })
})
