import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { GlobalEnvSettingsContent } from '../GlobalEnvSettingsContent'

const mockGetGlobalEnv = vi.fn()
const mockUpdateGlobalEnv = vi.fn()

vi.mock('@/lib/api', () => ({
  settingsApi: {
    getGlobalEnv: (...args: unknown[]) => mockGetGlobalEnv(...args),
    updateGlobalEnv: (...args: unknown[]) => mockUpdateGlobalEnv(...args),
  },
}))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn() },
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

  it('masks sensitive values by key pattern and toggles visibility', async () => {
    mockGetGlobalEnv.mockResolvedValue({
      vars: [{ key: 'DATABASE_PASSWORD', value: 'topsecret' }],
    })
    renderWithClient()

    const valueInputs = (await screen.findAllByDisplayValue('topsecret')) as HTMLInputElement[]
    expect(valueInputs[0].type).toBe('password')

    const toggles = screen.getAllByRole('button', { name: /show value/i })
    fireEvent.click(toggles[0])

    await waitFor(() => {
      const refreshed = screen.getAllByDisplayValue('topsecret') as HTMLInputElement[]
      expect(refreshed[0].type).toBe('text')
    })
  })
})
