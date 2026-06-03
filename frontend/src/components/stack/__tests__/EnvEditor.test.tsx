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

  it('shows no env file message when GET returns 404', async () => {
    mockGetEnv.mockRejectedValue({ status: 404 })
    renderWithProviders(<EnvEditor stackId="test-stack" />)

    await vi.waitFor(() => {
      expect(screen.getByText('No environment file found for this stack')).toBeInTheDocument()
    })
  })

  it('renders table view with env entries', async () => {
    mockGetEnv.mockResolvedValue({
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
    mockGetEnv.mockRejectedValue({ status: 404 })
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
    mockGetEnv.mockRejectedValue({ status: 404 })
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
