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

  // agent-os-pjos. GetGitSettings answers hasHttpsToken:true with
  // httpsTokenUnreadable:true when the stored token exists but cannot be read.
  // "(currently set)" alone told the operator a usable token was in place.
  it('discloses a stored token that could not be read, instead of calling it set', async () => {
    mockGetGit.mockResolvedValue({
      sshKey: '',
      httpsUser: '',
      hasHttpsToken: true,
      httpsTokenUnreadable: true,
    })
    renderPanel()

    expect(
      await screen.findByText('A token is stored but could not be read. Enter it again to replace it.'),
    ).toBeInTheDocument()
    expect(screen.queryByText('(currently set)')).not.toBeInTheDocument()
    expect(screen.getByLabelText(/Personal Access Token/)).toHaveAttribute(
      'placeholder',
      'Enter the token again to replace it',
    )
  })

  it('does not show the unreadable-token notice for a readable stored token', async () => {
    mockGetGit.mockResolvedValue({
      sshKey: '',
      httpsUser: '',
      hasHttpsToken: true,
      httpsTokenUnreadable: false,
    })
    renderPanel()

    expect(await screen.findByText('(currently set)')).toBeInTheDocument()
    expect(screen.queryByText(/could not be read/)).not.toBeInTheDocument()
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

/**
 * agent-os-zlw0. UpdateGitSettings (backend/internal/handlers/settings.go:914)
 * answers with two VALIDATION_ERROR 400s — bad body, and an SSH field holding
 * pasted key material rather than a path — plus ENCRYPTION_KEY_MISSING, a 422
 * from respondIfEncryptionUnavailable (respond.go:229) reached from its own
 * token write at settings.go:968. That last one carries the recovery ("Set
 * STORAGE_KEY ... and restart Capstan"), and the zero-arity onError threw it away.
 */
describe('GitSettingsContent — why the save failed', () => {
  it('names what the server rejected', async () => {
    mockUpdateGit.mockRejectedValue({
      error: 'Bad Request',
      code: 'VALIDATION_ERROR',
      message: 'git SSH key must be a path to a key file, not the key contents',
      status: 400,
    })
    renderPanel()

    fireEvent.change(await screen.findByLabelText('SSH Private Key Path'), {
      target: { value: '-----BEGIN OPENSSH PRIVATE KEY-----' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Save Git Settings' }))

    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith('Failed to save git settings', {
        description: 'git SSH key must be a path to a key file, not the key contents',
      }),
    )
  })

  it('passes on the missing-encryption-key recovery', async () => {
    mockUpdateGit.mockRejectedValue({
      error: 'Unprocessable Entity',
      code: 'ENCRYPTION_KEY_MISSING',
      message:
        'Cannot store this value: no encryption key is configured. Set STORAGE_KEY (or JWT_SECRET) in the environment and restart Capstan, then try again.',
      status: 422,
    })
    renderPanel()

    fireEvent.change(await screen.findByLabelText(/Personal Access Token/), {
      target: { value: 'ghp_secret' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Save Git Settings' }))

    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith('Failed to save git settings', {
        description: expect.stringContaining('Set STORAGE_KEY'),
      }),
    )
  })

  // MUST-PASS side, same instrument.
  it('shows the generic sentence alone when the failure carries no server cause', async () => {
    mockUpdateGit.mockRejectedValue({
      error: 'Unknown error',
      code: 'ERR_NETWORK',
      message: 'Network Error',
    })
    renderPanel()

    fireEvent.change(await screen.findByLabelText('Username'), { target: { value: 'git' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save Git Settings' }))

    await waitFor(() => expect(toast.error).toHaveBeenCalled())
    const call = vi.mocked(toast.error).mock.calls.find((c) => c[0] === 'Failed to save git settings')
    expect(call).toBeDefined()
    expect(call?.[1]).toBeUndefined()
  })
})

describe('GitSettingsContent — a failed refresh is disclosed before Save (agent-os-vs6c)', () => {
  const serverFault = { status: 500, code: 'INTERNAL_ERROR', message: 'read failed' }

  function renderWithClient() {
    const queryClient = new QueryClient({
      defaultOptions: {
        queries: { retry: false, staleTime: 0 },
        mutations: { retry: false },
      },
    })
    render(
      <QueryClientProvider client={queryClient}>
        <GitSettingsContent />
      </QueryClientProvider>,
    )
    return queryClient
  }

  const notice = /Could not refresh the git settings\. The values shown are the last ones the server sent, so check them before saving\./

  /**
   * Resolve FIRST, then reject (the ptiq shape): a first-fetch-rejects fixture
   * never puts populated credentials on screen for the refetch to strand.
   */
  it('keeps the populated credentials AND says the refresh failed', async () => {
    mockGetGit.mockResolvedValue({ sshKey: '/keys/id_ed25519', httpsUser: 'deploy-bot', hasHttpsToken: true })
    const queryClient = renderWithClient()

    expect(await screen.findByLabelText('SSH Private Key Path')).toHaveValue('/keys/id_ed25519')

    mockGetGit.mockRejectedValue(serverFault)
    await queryClient.refetchQueries()
    await waitFor(() =>
      expect(
        queryClient.getQueryCache().getAll().some((q) => q.state.status === 'error'),
      ).toBe(true),
    )

    // Retention first, so the red below means RETAINED AND UNDISCLOSED rather
    // than "the form went away".
    expect(screen.getByLabelText('SSH Private Key Path')).toHaveValue('/keys/id_ed25519')
    expect(screen.getByLabelText('Username')).toHaveValue('deploy-bot')

    expect(screen.getByText(notice)).toBeInTheDocument()

    // Placement: directly above the control that submits the stale values.
    const alert = screen.getByRole('alert')
    expect(alert.nextElementSibling).toContainElement(
      screen.getByRole('button', { name: 'Save Git Settings' }),
    )

    // Persistence: an edit does not dismiss it, because the untouched field
    // is still written back from the stale data.
    fireEvent.change(screen.getByLabelText('Username'), { target: { value: 'other-bot' } })
    expect(screen.getByText(notice)).toBeInTheDocument()
  })

  it('shows no notice while the settings are fresh', async () => {
    mockGetGit.mockResolvedValue({ sshKey: '/keys/id_ed25519', httpsUser: 'deploy-bot', hasHttpsToken: true })
    renderWithClient()

    expect(await screen.findByLabelText('SSH Private Key Path')).toHaveValue('/keys/id_ed25519')
    expect(screen.queryByText(notice)).not.toBeInTheDocument()
  })

  it('does not claim "the last ones the server sent" when the first load failed', async () => {
    mockGetGit.mockRejectedValue(serverFault)
    const queryClient = renderWithClient()

    await waitFor(() =>
      expect(
        queryClient.getQueryCache().getAll().some((q) => q.state.status === 'error'),
      ).toBe(true),
    )
    expect(screen.queryByText(notice)).not.toBeInTheDocument()
  })
})
