import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithProviders } from '../../../test/utils'

const mockGet = vi.fn()
const mockPut = vi.fn()

vi.mock('@/lib/api', () => ({
  apiClient: {
    get: (...args: unknown[]) => mockGet(...args),
    put: (...args: unknown[]) => mockPut(...args),
  },
}))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), warning: vi.fn() },
}))

import { EnvEditor } from '../EnvEditor'

describe('EnvEditor', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('shows loading state', () => {
    mockGet.mockReturnValue(new Promise(() => {}))
    renderWithProviders(<EnvEditor stackId="test-stack" />)
    expect(screen.getByText('Loading...')).toBeInTheDocument()
  })

  it('shows no env file message when env data is null', async () => {
    mockGet.mockResolvedValue({ data: null })
    renderWithProviders(<EnvEditor stackId="test-stack" />)

    await vi.waitFor(() => {
      expect(screen.getByText('No environment file found for this stack')).toBeInTheDocument()
    })
  })

  it('renders table view with env entries', async () => {
    mockGet.mockResolvedValue({
      data: {
        entries: [
          { key: 'PORT', value: '8080', sensitive: false, comment: false, line: 1 },
          { key: 'API_KEY', value: 'secret', sensitive: true, comment: false, line: 2 },
        ],
        raw: 'PORT=8080\nAPI_KEY=secret\n',
      },
    })
    renderWithProviders(<EnvEditor stackId="test-stack" />)

    await vi.waitFor(() => {
      const inputs = screen.getAllByLabelText(/Environment variable key/)
      expect(inputs.length).toBe(4)
    })
  })

  it('shows Add Entry button', async () => {
    mockGet.mockResolvedValue({
      data: {
        entries: [{ key: 'PORT', value: '8080', sensitive: false, comment: false, line: 1 }],
        raw: 'PORT=8080\n',
      },
    })
    renderWithProviders(<EnvEditor stackId="test-stack" />)

    await vi.waitFor(() => {
      expect(screen.getByText('Add Entry')).toBeInTheDocument()
    })
  })

  it('masks sensitive values with password input', async () => {
    mockGet.mockResolvedValue({
      data: {
        entries: [
          { key: 'API_KEY', value: 'secret', sensitive: true, comment: false, line: 1 },
        ],
        raw: 'API_KEY=secret\n',
      },
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
    mockGet.mockResolvedValue({
      data: {
        entries: [{ key: 'PORT', value: '8080', sensitive: false, comment: false, line: 1 }],
        raw: 'PORT=8080\n',
      },
    })
    renderWithProviders(<EnvEditor stackId="test-stack" />)

    await waitFor(() => {
      expect(screen.getByText('Table View')).toBeInTheDocument()
    })

    await user.click(screen.getByText('Raw Editor'))

    const textarea = await screen.findByPlaceholderText('KEY=value')
    expect(textarea).toBeInTheDocument()
    expect(textarea).toHaveValue('PORT=8080\n')
  })
})
