import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { GitSettingsContent } from '../GitSettingsContent'
import { toast } from 'sonner'

/**
 * SettingsPage.test.tsx mocks this panel out with an empty div, so the page
 * test proves only that the right empty div appeared. This file renders the
 * real component (agent-os-m1mu).
 *
 * The API layer is mocked, not the hooks — so useGitSettings and
 * useUpdateGitSettings, their query keys and their invalidation all run for
 * real, the same way BackupSettingsContent.test.tsx does it.
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

function createWrapper() {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false, staleTime: 0 },
      mutations: { retry: false },
    },
  })
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  )
}

const renderPanel = () => render(<GitSettingsContent />, { wrapper: createWrapper() })

beforeEach(() => {
  vi.clearAllMocks()
  mockGetGit.mockResolvedValue({ sshKey: '', httpsUser: '', hasHttpsToken: false })
  mockUpdateGit.mockResolvedValue({})
})

describe('GitSettingsContent', () => {
  it('shows a spinner until the settings load', () => {
    mockGetGit.mockReturnValue(new Promise(() => {}))
    const { container } = renderPanel()

    expect(container.querySelector('form')).toBeNull()
  })

  it('populates the fields from the server settings', async () => {
    mockGetGit.mockResolvedValue({
      sshKey: '/keys/id_ed25519',
      httpsUser: 'deploy-bot',
      hasHttpsToken: false,
    })
    renderPanel()

    expect(await screen.findByLabelText('SSH Private Key Path')).toHaveValue('/keys/id_ed25519')
    expect(screen.getByLabelText('Username')).toHaveValue('deploy-bot')
  })

  it('falls back to empty strings when the server returns nothing', async () => {
    mockGetGit.mockResolvedValue({})
    renderPanel()

    expect(await screen.findByLabelText('SSH Private Key Path')).toHaveValue('')
    expect(screen.getByLabelText('Username')).toHaveValue('')
  })

  it('notes when a token is already stored, and says so in the placeholder', async () => {
    mockGetGit.mockResolvedValue({ sshKey: '', httpsUser: '', hasHttpsToken: true })
    renderPanel()

    expect(await screen.findByText('(currently set)')).toBeInTheDocument()
    expect(screen.getByLabelText(/Personal Access Token/)).toHaveAttribute(
      'placeholder',
      'Leave blank to keep current token',
    )
  })

  it('offers to create a token when none is stored', async () => {
    renderPanel()

    expect(await screen.findByLabelText(/Personal Access Token/)).toHaveAttribute(
      'placeholder',
      'ghp_xxxx or glpat-xxxx',
    )
    expect(screen.queryByText('(currently set)')).not.toBeInTheDocument()
  })

  it('keeps the token masked until the reveal button is pressed', async () => {
    renderPanel()

    const token = await screen.findByLabelText(/Personal Access Token/)
    expect(token).toHaveAttribute('type', 'password')

    fireEvent.click(screen.getByRole('button', { name: 'Reveal access token' }))
    expect(token).toHaveAttribute('type', 'text')

    fireEvent.click(screen.getByRole('button', { name: 'Hide access token' }))
    expect(token).toHaveAttribute('type', 'password')
  })

  it('submits only the fields that have a value', async () => {
    renderPanel()

    const sshKey = await screen.findByLabelText('SSH Private Key Path')
    fireEvent.change(sshKey, { target: { value: '/keys/id_rsa' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save Git Settings' }))

    await waitFor(() => expect(mockUpdateGit).toHaveBeenCalledTimes(1))
    // httpsUser and httpsToken are empty, so they are omitted entirely rather
    // than sent as '' — sending '' would clear a stored token.
    expect(mockUpdateGit).toHaveBeenCalledWith({ sshKey: '/keys/id_rsa' })
  })

  it('submits all three fields when all are filled', async () => {
    renderPanel()

    fireEvent.change(await screen.findByLabelText('SSH Private Key Path'), {
      target: { value: '/keys/id_rsa' },
    })
    fireEvent.change(screen.getByLabelText('Username'), { target: { value: 'git' } })
    fireEvent.change(screen.getByLabelText(/Personal Access Token/), {
      target: { value: 'ghp_secret' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Save Git Settings' }))

    await waitFor(() =>
      expect(mockUpdateGit).toHaveBeenCalledWith({
        sshKey: '/keys/id_rsa',
        httpsUser: 'git',
        httpsToken: 'ghp_secret',
      }),
    )
  })

  it('clears the token field and confirms on success', async () => {
    renderPanel()

    const token = await screen.findByLabelText(/Personal Access Token/)
    fireEvent.change(token, { target: { value: 'ghp_secret' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save Git Settings' }))

    await waitFor(() => expect(toast.success).toHaveBeenCalledWith('Git settings saved'))
    // The token must not linger in the DOM after it has been stored.
    expect(token).toHaveValue('')
  })

  it('reports a failed save and keeps the token so it is not lost', async () => {
    mockUpdateGit.mockRejectedValue(new Error('boom'))
    renderPanel()

    const token = await screen.findByLabelText(/Personal Access Token/)
    fireEvent.change(token, { target: { value: 'ghp_secret' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save Git Settings' }))

    await waitFor(() => expect(toast.error).toHaveBeenCalledWith('Failed to save git settings'))
    expect(token).toHaveValue('ghp_secret')
    expect(toast.success).not.toHaveBeenCalled()
  })

  it('submits on Enter in a field, not just on the button', async () => {
    const { container } = renderPanel()

    fireEvent.change(await screen.findByLabelText('Username'), { target: { value: 'git' } })
    fireEvent.submit(container.querySelector('form')!)

    await waitFor(() => expect(mockUpdateGit).toHaveBeenCalledWith({ httpsUser: 'git' }))
  })
})
