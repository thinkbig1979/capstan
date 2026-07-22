import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { GlobalEnvSettingsContent } from '../GlobalEnvSettingsContent'
import { useEnvUnlockStore } from '@/stores/envUnlockStore'
import { useAuthStore } from '@/stores/authStore'

const mockGetGlobalEnv = vi.fn()
const mockUpdateGlobalEnv = vi.fn()
const mockVerifyPassword = vi.fn()

vi.mock('@/lib/api', () => ({
  settingsApi: {
    getGlobalEnv: (...args: unknown[]) => mockGetGlobalEnv(...args),
    updateGlobalEnv: (...args: unknown[]) => mockUpdateGlobalEnv(...args),
  },
  authApi: {
    verifyPassword: (...args: unknown[]) => mockVerifyPassword(...args),
  },
}))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), warning: vi.fn(), info: vi.fn() },
}))

function renderWithClient() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <GlobalEnvSettingsContent />
    </QueryClientProvider>
  )
}

describe('GlobalEnvSettingsContent', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    useEnvUnlockStore.getState().lock()
    useAuthStore.setState({ authDisabled: false })
  })

  it('renders existing variables from the API', async () => {
    mockGetGlobalEnv.mockResolvedValue({
      vars: [
        { key: 'NODE_ENV', value: 'production' },
        { key: 'API_KEY', value: 'secret-123' },
      ],
    })
    renderWithClient()

    await waitFor(() => {
      // Component renders both desktop and mobile layouts; each row appears twice.
      expect(screen.getAllByDisplayValue('NODE_ENV')).toHaveLength(2)
      expect(screen.getAllByDisplayValue('production')).toHaveLength(2)
      expect(screen.getAllByDisplayValue('API_KEY')).toHaveLength(2)
    })
  })

  it('saves edited variables and shows the unsaved-changes badge', async () => {
    mockGetGlobalEnv.mockResolvedValue({ vars: [{ key: 'FOO', value: 'bar' }] })
    mockUpdateGlobalEnv.mockResolvedValue(undefined)

    renderWithClient()

    const valueInputs = await screen.findAllByDisplayValue('bar')
    fireEvent.change(valueInputs[0], { target: { value: 'baz' } })

    expect(screen.getByText('Unsaved changes')).toBeInTheDocument()

    fireEvent.click(screen.getByText('Save'))

    await waitFor(() => {
      expect(mockUpdateGlobalEnv).toHaveBeenCalledWith([
        { key: 'FOO', value: 'baz' },
      ])
    })
  })

  it('adds a new variable row when Add Variable clicked', async () => {
    mockGetGlobalEnv.mockResolvedValue({ vars: [] })
    renderWithClient()

    await waitFor(() => {
      // Empty-state message appears in both layouts.
      expect(screen.getAllByText(/No global variables yet/i).length).toBeGreaterThan(0)
    })

    fireEvent.click(screen.getByText('Add Variable'))

    const keyInputs = await screen.findAllByPlaceholderText('KEY')
    expect(keyInputs.length).toBeGreaterThan(0)
  })

  it('masks sensitive values by key pattern', async () => {
    mockGetGlobalEnv.mockResolvedValue({
      vars: [{ key: 'DATABASE_PASSWORD', value: 'topsecret' }],
    })
    renderWithClient()

    const valueInputs = (await screen.findAllByDisplayValue('topsecret')) as HTMLInputElement[]
    expect(valueInputs[0].type).toBe('password')
  })

  it('prompts for password before revealing a sensitive value when locked', async () => {
    mockGetGlobalEnv.mockResolvedValue({
      vars: [{ key: 'DATABASE_PASSWORD', value: 'topsecret' }],
    })
    renderWithClient()

    await screen.findAllByDisplayValue('topsecret')

    const toggles = screen.getAllByRole('button', { name: /show value/i })
    fireEvent.click(toggles[0])

    expect(await screen.findByText('Unlock environment variables')).toBeInTheDocument()
  })

  it('reveals the value after a successful unlock', async () => {
    mockGetGlobalEnv.mockResolvedValue({
      vars: [{ key: 'DATABASE_PASSWORD', value: 'topsecret' }],
    })
    mockVerifyPassword.mockResolvedValue({ ok: true })

    renderWithClient()

    await screen.findAllByDisplayValue('topsecret')

    const toggles = screen.getAllByRole('button', { name: /show value/i })
    fireEvent.click(toggles[0])

    const passwordInput = await screen.findByLabelText('Password')
    fireEvent.change(passwordInput, { target: { value: 'mypassword' } })
    fireEvent.click(screen.getByRole('button', { name: 'Unlock' }))

    await waitFor(() => {
      const refreshed = screen.getAllByDisplayValue('topsecret') as HTMLInputElement[]
      expect(refreshed[0].type).toBe('text')
    })
    expect(mockVerifyPassword).toHaveBeenCalledWith('mypassword')
  })

  it('deletes a variable row', async () => {
    mockGetGlobalEnv.mockResolvedValue({
      vars: [
        { key: 'FOO', value: 'bar' },
        { key: 'BAZ', value: 'qux' },
      ],
    })
    renderWithClient()

    await screen.findAllByDisplayValue('bar')

    const removeButtons = screen.getAllByRole('button', { name: 'Remove global env 1' })
    fireEvent.click(removeButtons[0])

    await waitFor(() => {
      expect(screen.queryAllByDisplayValue('bar')).toHaveLength(0)
    })
    expect(screen.getAllByDisplayValue('qux').length).toBeGreaterThan(0)
  })

  it('filters variables by search query', async () => {
    mockGetGlobalEnv.mockResolvedValue({
      vars: [
        { key: 'FOO', value: 'bar' },
        { key: 'BAZ', value: 'qux' },
      ],
    })
    renderWithClient()

    await screen.findAllByDisplayValue('bar')

    fireEvent.change(screen.getByPlaceholderText(/filter by key or value/i), {
      target: { value: 'baz' },
    })

    await waitFor(() => {
      expect(screen.queryAllByDisplayValue('bar')).toHaveLength(0)
      expect(screen.getAllByDisplayValue('qux').length).toBeGreaterThan(0)
    })
  })

  it('shows a message when no variables match the filter', async () => {
    mockGetGlobalEnv.mockResolvedValue({ vars: [{ key: 'FOO', value: 'bar' }] })
    renderWithClient()

    await screen.findAllByDisplayValue('bar')

    fireEvent.change(screen.getByPlaceholderText(/filter by key or value/i), {
      target: { value: 'nomatch' },
    })

    await waitFor(() => {
      expect(screen.getAllByText(/No variables match/i).length).toBeGreaterThan(0)
    })
  })

  it('shows an error message when the query fails', async () => {
    mockGetGlobalEnv.mockRejectedValue(new Error('Network error'))
    renderWithClient()

    await waitFor(
      () => {
        expect(
          screen.getByText('Failed to load global environment variables.')
        ).toBeInTheDocument()
      },
      { timeout: 3000 },
    )
  })

  it('hides a revealed value again when toggled', async () => {
    mockGetGlobalEnv.mockResolvedValue({
      vars: [{ key: 'DATABASE_PASSWORD', value: 'topsecret' }],
    })
    useEnvUnlockStore.getState().unlock()

    renderWithClient()

    await screen.findAllByDisplayValue('topsecret')

    const showToggles = screen.getAllByRole('button', { name: /show value/i })
    fireEvent.click(showToggles[0])

    const hideToggles = await screen.findAllByRole('button', { name: /hide value/i })
    fireEvent.click(hideToggles[0])

    await waitFor(() => {
      const inputs = screen.getAllByDisplayValue('topsecret') as HTMLInputElement[]
      expect(inputs[0].type).toBe('password')
    })
  })

  it('re-masks a revealed value when the unlock session expires', async () => {
    mockGetGlobalEnv.mockResolvedValue({
      vars: [{ key: 'DATABASE_PASSWORD', value: 'topsecret' }],
    })
    useEnvUnlockStore.getState().unlock()

    renderWithClient()

    await screen.findAllByDisplayValue('topsecret')

    const showToggles = screen.getAllByRole('button', { name: /show value/i })
    fireEvent.click(showToggles[0])

    await waitFor(() => {
      const inputs = screen.getAllByDisplayValue('topsecret') as HTMLInputElement[]
      expect(inputs[0].type).toBe('text')
    })

    act(() => {
      useEnvUnlockStore.getState().lock()
    })

    await waitFor(() => {
      const inputs = screen.getAllByDisplayValue('topsecret') as HTMLInputElement[]
      expect(inputs[0].type).toBe('password')
    })
  })

  it('reveals sensitive values immediately without a dialog when auth is disabled', async () => {
    useAuthStore.setState({ authDisabled: true })
    mockGetGlobalEnv.mockResolvedValue({
      vars: [{ key: 'DATABASE_PASSWORD', value: 'topsecret' }],
    })
    renderWithClient()

    await screen.findAllByDisplayValue('topsecret')

    const toggles = screen.getAllByRole('button', { name: /show value/i })
    fireEvent.click(toggles[0])

    await waitFor(() => {
      const inputs = screen.getAllByDisplayValue('topsecret') as HTMLInputElement[]
      expect(inputs[0].type).toBe('text')
    })
    expect(screen.queryByText('Unlock environment variables')).not.toBeInTheDocument()
  })

  it('trims keys and drops empty-key rows on save', async () => {
    mockGetGlobalEnv.mockResolvedValue({ vars: [{ key: 'FOO', value: 'bar' }] })
    mockUpdateGlobalEnv.mockResolvedValue(undefined)
    renderWithClient()

    await screen.findAllByDisplayValue('bar')

    fireEvent.click(screen.getByText('Add Variable'))
    const keyInputs = await screen.findAllByPlaceholderText('KEY')
    fireEvent.change(keyInputs[keyInputs.length - 1], { target: { value: '  SPACED  ' } })

    fireEvent.click(screen.getByText('Save'))

    await waitFor(() => {
      expect(mockUpdateGlobalEnv).toHaveBeenCalledWith([
        { key: 'FOO', value: 'bar' },
        { key: 'SPACED', value: '' },
      ])
    })
  })
})
