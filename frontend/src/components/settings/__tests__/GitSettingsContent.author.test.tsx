import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { GitSettingsContent } from '../GitSettingsContent'
import { toast } from 'sonner'

/**
 * The commit author fields (agent-os-k81p.3): loaded from GET /settings/git,
 * sent on Save, and the server's 400 reason shown when it refuses one.
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
  mockGetGit.mockResolvedValue({
    sshKey: '', httpsUser: '', hasHttpsToken: false, httpsTokenUnreadable: false,
    authorName: 'Stored Name', authorEmail: 'stored@example.invalid',
  })
  mockUpdateGit.mockResolvedValue({})
})

describe('GitSettingsContent: commit author', () => {
  it('shows the stored author and sends the edited one', async () => {
    const user = userEvent.setup()
    renderPanel()
    const name = await screen.findByLabelText('Author name')
    expect(name).toHaveValue('Stored Name')
    expect(screen.getByLabelText('Author email')).toHaveValue('stored@example.invalid')

    await user.clear(name)
    await user.type(name, 'New Name')
    await user.click(screen.getByRole('button', { name: 'Save Git Settings' }))
    await waitFor(() =>
      expect(mockUpdateGit).toHaveBeenCalledWith({ authorName: 'New Name', authorEmail: 'stored@example.invalid' }),
    )
  })

  it('shows the server\'s validation message when it refuses the author', async () => {
    const user = userEvent.setup()
    mockUpdateGit.mockRejectedValue({ code: 'VALIDATION_ERROR', message: 'The author email must contain exactly one @', status: 400 })
    renderPanel()
    const email = await screen.findByLabelText('Author email')
    await user.clear(email)
    await user.type(email, 'not-an-email')
    await user.click(screen.getByRole('button', { name: 'Save Git Settings' }))
    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith('Failed to save git settings', {
        description: 'The author email must contain exactly one @',
      }),
    )
    expect(mockUpdateGit).toHaveBeenCalledWith(expect.objectContaining({ authorEmail: 'not-an-email' }))
  })
})
