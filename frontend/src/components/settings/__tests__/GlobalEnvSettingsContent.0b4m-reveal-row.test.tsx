/**
 * agent-os-0b4m, the global env page: reveal state was keyed by row index, so
 * deleting a row above a revealed one moved the reveal onto its neighbour; and
 * a value the locked view blanked showed as an empty input, the same as a value
 * that really is empty.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { GlobalEnvSettingsContent } from '../GlobalEnvSettingsContent'
import { useEnvUnlockStore } from '@/stores/envUnlockStore'
import { useAuthStore } from '@/stores/authStore'

const mockGetGlobalEnv = vi.fn()

vi.mock('@/lib/api', () => ({
  settingsApi: {
    getGlobalEnv: (...args: unknown[]) => mockGetGlobalEnv(...args),
    updateGlobalEnv: vi.fn(),
  },
  authApi: { verifyPassword: vi.fn() },
}))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), warning: vi.fn(), info: vi.fn() },
}))

function renderPage() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  render(
    <QueryClientProvider client={client}>
      <GlobalEnvSettingsContent />
    </QueryClientProvider>,
  )
}

const values = (n: number) => screen.getAllByLabelText(`Global env value ${n}`) as HTMLInputElement[]

beforeEach(() => {
  vi.clearAllMocks()
  act(() => useEnvUnlockStore.getState().lock())
  useAuthStore.setState({ authDisabled: false })
})
afterEach(() => {
  act(() => useEnvUnlockStore.getState().lock())
})

describe('global env reveal state and hidden values (agent-os-0b4m)', () => {
  it('deleting a row above a revealed one keeps the reveal on its own row', async () => {
    mockGetGlobalEnv.mockResolvedValue({
      vars: [
        { key: 'X_TOKEN', value: 'xsecret', sensitive: true },
        { key: 'Y_TOKEN', value: 'ysecret', sensitive: true },
        { key: 'Z_TOKEN', value: 'zsecret', sensitive: true },
      ],
    })
    act(() => useEnvUnlockStore.getState().unlock('tok'))
    renderPage()
    await waitFor(() => expect(values(2)[0].value).toBe('ysecret'))
    fireEvent.click(screen.getAllByRole('button', { name: 'Show value 2' })[0])
    expect(values(2).map((el) => el.type)).toEqual(['text', 'text'])

    fireEvent.click(screen.getAllByRole('button', { name: 'Remove global env 1' })[0])

    expect(values(1).map((el) => [el.value, el.type])).toEqual([
      ['ysecret', 'text'],
      ['ysecret', 'text'],
    ])
    expect(values(2).map((el) => [el.value, el.type])).toEqual([
      ['zsecret', 'password'],
      ['zsecret', 'password'],
    ])
  })

  it('locked: a blanked secret says it is hidden; a value that is empty does not', async () => {
    mockGetGlobalEnv.mockResolvedValue({
      locked: true,
      vars: [
        { key: 'DATABASE_URL', value: '', sensitive: true },
        { key: 'EMPTY', value: '' },
      ],
    })
    renderPage()
    await waitFor(() => expect(values(1)).toHaveLength(2))
    expect(values(1).map((el) => el.placeholder)).toEqual(['Hidden, unlock to view', 'Hidden, unlock to view'])
    expect(values(2).map((el) => el.placeholder)).toEqual(['value', 'value'])
  })

  it('unlocked: an empty secret value shows the plain placeholder', async () => {
    mockGetGlobalEnv.mockResolvedValue({ vars: [{ key: 'API_KEY', value: '', sensitive: true }] })
    act(() => useEnvUnlockStore.getState().unlock('tok'))
    renderPage()
    await waitFor(() => expect(values(1)).toHaveLength(2))
    expect(values(1).map((el) => el.placeholder)).toEqual(['value', 'value'])
  })
})
