import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithProviders } from '../../../test/utils'

// ─── Mocks ────────────────────────────────────────────────────────────────────

const mockGetEnv = vi.fn()
const mockUpdateEnv = vi.fn()
const mockCreateEnv = vi.fn()

vi.mock('@/lib/api', () => ({
  stacksApi: {
    getEnv: (...args: unknown[]) => mockGetEnv(...args),
    updateEnv: (...args: unknown[]) => mockUpdateEnv(...args),
    createEnv: (...args: unknown[]) => mockCreateEnv(...args),
    updateComposeAndEnv: vi.fn(),
  },
  apiClient: {
    get: vi.fn(),
    put: vi.fn(),
    post: vi.fn(),
  },
}))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), warning: vi.fn(), info: vi.fn() },
}))

import { EnvEditor } from '../EnvEditor'
import { toast } from 'sonner'

// ─── Helpers ─────────────────────────────────────────────────────────────────

const baseEnvData = {
  hasEnvFile: true,
  filename: '.env',
  entries: [
    { key: 'PORT', value: '8080', sensitive: false, comment: false, line: 1 },
  ],
  raw: 'PORT=8080\n',
}

// ─── Suite ───────────────────────────────────────────────────────────────────

describe('EnvEditor', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  // ── Existing behaviour (regression) ──────────────────────────────────────

  it('shows loading state', () => {
    mockGetEnv.mockReturnValue(new Promise(() => {}))
    renderWithProviders(<EnvEditor stackId="test-stack" />)
    expect(screen.getByText('Loading...')).toBeInTheDocument()
  })

  // Three arms on one instrument, because the fix that removes the console
  // error is also the fix that could swallow a real fault. `hasEnvFile: false`
  // is a 200 (agent-os-bt5y) and must reach the no-file state; a REJECTION is
  // a fault — an unknown stack, or a configured env file that has vanished
  // from disk, which the backend still answers 404 — and must reach the error
  // state, not the no-file state; and the present payload must still render
  // the editor. An arm that only checked the first would pass just as well if
  // the component treated every failure as "no env file".
  it('shows the no env file message when GET answers 200 with hasEnvFile false', async () => {
    mockGetEnv.mockResolvedValue({ hasEnvFile: false })
    renderWithProviders(<EnvEditor stackId="test-stack" />)

    await vi.waitFor(() => {
      expect(screen.getByText('No environment file found for this stack')).toBeInTheDocument()
    })
    expect(screen.queryByText('Failed to load environment file')).not.toBeInTheDocument()
  })

  it('shows the error state, NOT the no-env-file state, when GET rejects', async () => {
    mockGetEnv.mockRejectedValue({ status: 404, message: 'Env file not found on disk' })
    renderWithProviders(<EnvEditor stackId="test-stack" />)

    await vi.waitFor(() => {
      expect(screen.getByText('Failed to load environment file')).toBeInTheDocument()
    })
    expect(
      screen.queryByText('No environment file found for this stack'),
    ).not.toBeInTheDocument()
  })

  // agent-os-rtn8: EnvErrorState took only `onRetry`, so it structurally could
  // not display a cause even when the caller had one. env.go's Get answers TWO
  // distinct 404s -- "Stack not found" (env.go:83) and "Env file not found on
  // disk" (env.go:133) -- and the comment at EnvEditor.tsx:64-70 says both
  // deliberately land in isError. They arrived as one indistinguishable
  // sentence. (The cause only reaches the component because agent-os-mc4i
  // stopped classifyError's 404 arm replacing it; before that this test could
  // not have passed no matter what the component did.)
  describe('the error state shows the backend cause (agent-os-rtn8)', () => {
    it('renders the cause alongside the fixed sentence, not instead of it', async () => {
      mockGetEnv.mockRejectedValue({ status: 404, message: 'Env file not found on disk' })
      renderWithProviders(<EnvEditor stackId="test-stack" />)

      await vi.waitFor(() => {
        expect(screen.getByText('Failed to load environment file')).toBeInTheDocument()
      })
      expect(screen.getByText('Env file not found on disk')).toBeInTheDocument()
    })

    // The whole point of the bead: the two 404s must not read the same. An
    // assertion on one of them alone passes if the component hardcodes it.
    it('tells the two 404 states apart', async () => {
      mockGetEnv.mockRejectedValue({ status: 404, message: 'Stack not found' })
      const { unmount } = renderWithProviders(<EnvEditor stackId="test-stack" />)
      await vi.waitFor(() => {
        expect(screen.getByText('Stack not found')).toBeInTheDocument()
      })
      expect(screen.queryByText('Env file not found on disk')).not.toBeInTheDocument()
      unmount()
    })

    // TWO-SIDED (agent-os-zlw0 criterion 2): a failure carrying no cause must
    // still show the fixed sentence and must NOT invent one. Green before the
    // prop existed and must stay green after it.
    it('shows only the fixed sentence when the failure carries no cause', async () => {
      mockGetEnv.mockRejectedValue({ status: 500 })
      renderWithProviders(<EnvEditor stackId="test-stack" />)

      await vi.waitFor(() => {
        expect(screen.getByText('Failed to load environment file')).toBeInTheDocument()
      })
      expect(screen.queryByText('Env file not found on disk')).not.toBeInTheDocument()
      expect(screen.queryByText('Stack not found')).not.toBeInTheDocument()
    })
  })

  it('renders table view with env entries', async () => {
    mockGetEnv.mockResolvedValue({
      hasEnvFile: true,
      filename: '.env',
      entries: [
        { key: 'PORT', value: '8080', sensitive: false, comment: false, line: 1 },
        { key: 'API_KEY', value: 'secret', sensitive: true, comment: false, line: 2 },
      ],
      raw: 'PORT=8080\nAPI_KEY=secret\n',
    })
    renderWithProviders(<EnvEditor stackId="test-stack" />)

    await vi.waitFor(() => {
      const inputs = screen.getAllByLabelText(/Environment variable key/)
      expect(inputs.length).toBe(4)
    })
  })

  it('shows Add Entry button', async () => {
    mockGetEnv.mockResolvedValue(baseEnvData)
    renderWithProviders(<EnvEditor stackId="test-stack" />)

    await vi.waitFor(() => {
      expect(screen.getByText('Add Entry')).toBeInTheDocument()
    })
  })

  it('masks sensitive values with password input', async () => {
    mockGetEnv.mockResolvedValue({
      hasEnvFile: true,
      filename: '.env',
      entries: [
        { key: 'API_KEY', value: 'secret', sensitive: true, comment: false, line: 1 },
      ],
      raw: 'API_KEY=secret\n',
    })
    renderWithProviders(<EnvEditor stackId="test-stack" />)

    await vi.waitFor(() => {
      const passwordInputs = screen.getAllByDisplayValue('secret')
      expect(passwordInputs.length).toBe(2)
      passwordInputs.forEach((input) => {
        expect(input).toHaveAttribute('type', 'password')
      })
    })
  })

  it('toggles to raw view', async () => {
    const user = userEvent.setup()
    mockGetEnv.mockResolvedValue(baseEnvData)
    renderWithProviders(<EnvEditor stackId="test-stack" />)

    await waitFor(() => {
      expect(screen.getByText('Table View')).toBeInTheDocument()
    })

    await user.click(screen.getByText('Raw Editor'))

    const textarea = await screen.findByPlaceholderText('KEY=value')
    expect(textarea).toBeInTheDocument()
    expect(textarea).toHaveValue('PORT=8080\n')
  })

  // ── B4: Env save outcome handling (finding #15) ───────────────────────────

  it('env save failed outcome → toast.error with reason, no "Saved" toast', async () => {
    mockGetEnv.mockResolvedValue(baseEnvData)
    // Backend returns an ActionResult with outcome 'failed'
    mockUpdateEnv.mockResolvedValue({
      outcome: 'failed',
      reason: 'Entry has empty key',
    })

    const user = userEvent.setup()
    renderWithProviders(<EnvEditor stackId="test-stack" />)

    await waitFor(() => expect(screen.getByText('Save')).toBeInTheDocument())

    // Make a change so Save is enabled
    const keyInput = screen.getAllByLabelText(/Environment variable key/)[0]
    await user.clear(keyInput)
    await user.type(keyInput, 'NEW_KEY')

    await user.click(screen.getByText('Save'))

    await waitFor(() => {
      expect(toast.error).toHaveBeenCalledWith('Entry has empty key')
      expect(toast.success).not.toHaveBeenCalled()
    })
  })

  it('env save partial outcome → toast.warning, no false "Saved"', async () => {
    mockGetEnv.mockResolvedValue(baseEnvData)
    mockUpdateEnv.mockResolvedValue({
      outcome: 'partial',
      reason: 'Some entries could not be validated',
    })

    const user = userEvent.setup()
    renderWithProviders(<EnvEditor stackId="test-stack" />)

    await waitFor(() => expect(screen.getByText('Save')).toBeInTheDocument())

    const keyInput = screen.getAllByLabelText(/Environment variable key/)[0]
    await user.clear(keyInput)
    await user.type(keyInput, 'CHANGED_KEY')

    await user.click(screen.getByText('Save'))

    await waitFor(() => {
      expect(toast.warning).toHaveBeenCalledWith('Some entries could not be validated')
      expect(toast.success).not.toHaveBeenCalled()
    })
  })

  it('env save success outcome → toast.success with title', async () => {
    mockGetEnv.mockResolvedValue(baseEnvData)
    mockUpdateEnv.mockResolvedValue({
      outcome: 'success',
      reason: 'Env saved',
    })

    const user = userEvent.setup()
    renderWithProviders(<EnvEditor stackId="test-stack" />)

    await waitFor(() => expect(screen.getByText('Save')).toBeInTheDocument())

    const keyInput = screen.getAllByLabelText(/Environment variable key/)[0]
    await user.clear(keyInput)
    await user.type(keyInput, 'OTHER_KEY')

    await user.click(screen.getByText('Save'))

    await waitFor(() => {
      expect(toast.success).toHaveBeenCalledWith('Environment variables saved')
      expect(toast.error).not.toHaveBeenCalled()
    })
  })

  it('env save legacy {saved:true} → maps to success, fires toast.success', async () => {
    mockGetEnv.mockResolvedValue(baseEnvData)
    // Legacy backend shape
    mockUpdateEnv.mockResolvedValue({ saved: true, filename: '.env' })

    const user = userEvent.setup()
    renderWithProviders(<EnvEditor stackId="test-stack" />)

    await waitFor(() => expect(screen.getByText('Save')).toBeInTheDocument())

    const keyInput = screen.getAllByLabelText(/Environment variable key/)[0]
    await user.clear(keyInput)
    await user.type(keyInput, 'LEGACY_KEY')

    await user.click(screen.getByText('Save'))

    await waitFor(() => {
      expect(toast.success).toHaveBeenCalledWith('Environment variables saved')
    })
  })

  // ── B4: Create Environment File button (finding #16) ─────────────────────

  it('Create Environment File button calls createEnv and reveals editor on success', async () => {
    // GET returns 404 → no env file
    mockGetEnv.mockResolvedValue({ hasEnvFile: false })
    mockCreateEnv.mockResolvedValue({ outcome: 'success', reason: 'Created' })

    const user = userEvent.setup()
    renderWithProviders(<EnvEditor stackId="test-stack" />)

    await waitFor(() => {
      expect(screen.getByText('Create Environment File')).toBeInTheDocument()
    })

    await user.click(screen.getByText('Create Environment File'))

    await waitFor(() => {
      // createEnv was called
      expect(mockCreateEnv).toHaveBeenCalledWith('test-stack')
      // Editor is now revealed (Add Entry button appears)
      expect(screen.getByText('Add Entry')).toBeInTheDocument()
    })
  })

  it('Create Environment File button shows error when createEnv fails', async () => {
    mockGetEnv.mockResolvedValue({ hasEnvFile: false })
    mockCreateEnv.mockResolvedValue({ outcome: 'failed', reason: 'No permission' })

    const user = userEvent.setup()
    renderWithProviders(<EnvEditor stackId="test-stack" />)

    await waitFor(() => {
      expect(screen.getByText('Create Environment File')).toBeInTheDocument()
    })

    await user.click(screen.getByText('Create Environment File'))

    await waitFor(() => {
      expect(toast.error).toHaveBeenCalledWith('No permission')
    })
  })
})
