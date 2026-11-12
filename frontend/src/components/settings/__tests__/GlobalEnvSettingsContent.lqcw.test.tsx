/**
 * agent-os-lqcw: the global env form must not replace what the user is typing
 * when its query refetches (window focus, another tab's save, its own save).
 * The notice is asserted by role; its copy is not pinned.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { GlobalEnvSettingsContent } from '../GlobalEnvSettingsContent'
import { useEnvUnlockStore } from '@/stores/envUnlockStore'
import { useAuthStore } from '@/stores/authStore'
import { queryKeys } from '@/lib/query-keys'

type Vars = Array<{ key: string; value: string }>

let server: { vars: Vars; locked?: boolean } = { vars: [{ key: 'FOO', value: 'bar' }] }
const mockGetGlobalEnv = vi.fn()
const mockUpdateGlobalEnv = vi.fn()

vi.mock('@/lib/api', () => ({
  settingsApi: {
    getGlobalEnv: (...args: unknown[]) => mockGetGlobalEnv(...args),
    updateGlobalEnv: (...args: unknown[]) => mockUpdateGlobalEnv(...args),
  },
  authApi: { verifyPassword: vi.fn() },
}))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), warning: vi.fn(), info: vi.fn() },
}))

let client: QueryClient

function renderForm() {
  client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <GlobalEnvSettingsContent />
    </QueryClientProvider>,
  )
}

// Desktop and mobile layouts both render each row; the first is enough.
const valueInput = () => screen.getAllByLabelText('Global env value 1')[0] as HTMLInputElement
const edit = (value: string) => fireEvent.change(valueInput(), { target: { value } })

async function loaded(value = 'bar') {
  await waitFor(() => expect(valueInput().value).toBe(value))
}

async function refetchWith(next: typeof server) {
  server = next
  const calls = mockGetGlobalEnv.mock.calls.length
  await client.invalidateQueries({ queryKey: queryKeys.settings.globalEnv() })
  await waitFor(() => expect(mockGetGlobalEnv.mock.calls.length).toBeGreaterThan(calls))
}

beforeEach(() => {
  vi.clearAllMocks()
  useEnvUnlockStore.getState().lock()
  useAuthStore.setState({ authDisabled: false })
  server = { vars: [{ key: 'FOO', value: 'bar' }] }
  mockGetGlobalEnv.mockImplementation(() => Promise.resolve(server))
  mockUpdateGlobalEnv.mockImplementation((vars: Vars) => {
    server = { vars }
    return Promise.resolve(undefined)
  })
})

describe('global env form refetch (agent-os-lqcw)', () => {
  it('lqcw: a clean form takes the refetched values with no notice', async () => {
    renderForm()
    await loaded()

    await refetchWith({ vars: [{ key: 'FOO', value: 'changed' }] })

    await waitFor(() => expect(valueInput().value).toBe('changed'))
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('lqcw: edits survive a refetch that returns changed vars, and a notice shows', async () => {
    renderForm()
    await loaded()
    edit('typing')

    await refetchWith({ vars: [{ key: 'FOO', value: 'changed' }] })

    const notice = await screen.findByRole('alert')
    expect(within(notice).getByRole('button', { name: 'Reload' })).toBeInTheDocument()
    expect(valueInput().value).toBe('typing')
    expect(screen.getByText('Save')).not.toBeDisabled()
  })

  it('lqcw: Reload discards the edits, loads the changed vars and clears the notice', async () => {
    renderForm()
    await loaded()
    edit('typing')
    await refetchWith({ vars: [{ key: 'FOO', value: 'changed' }] })
    const notice = await screen.findByRole('alert')

    fireEvent.click(within(notice).getByRole('button', { name: 'Reload' }))

    await waitFor(() => expect(valueInput().value).toBe('changed'))
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(screen.queryByText('Unsaved changes')).not.toBeInTheDocument()
  })

  it('lqcw: no notice when the refetch brings what the form already shows', async () => {
    renderForm()
    await loaded()
    edit('same')

    await refetchWith({ vars: [{ key: 'FOO', value: 'same' }] })

    await waitFor(() => expect(screen.queryByText('Unsaved changes')).not.toBeInTheDocument())
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(valueInput().value).toBe('same')
  })

  it('lqcw: a save with no newer typing ends clean and shows no notice', async () => {
    renderForm()
    await loaded()
    edit('saved')

    fireEvent.click(screen.getByText('Save'))

    await waitFor(() => expect(mockUpdateGlobalEnv).toHaveBeenCalledWith([{ key: 'FOO', value: 'saved' }]))
    await waitFor(() => expect(screen.queryByText('Unsaved changes')).not.toBeInTheDocument())
    expect(valueInput().value).toBe('saved')
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('lqcw: typing while a save is in flight survives the save\'s own refetch, with no notice', async () => {
    let finishSave: () => void = () => {}
    mockUpdateGlobalEnv.mockImplementation((vars: Vars) => {
      return new Promise<void>((resolve) => {
        finishSave = () => {
          server = { vars }
          resolve()
        }
      })
    })
    renderForm()
    await loaded()
    edit('first')
    fireEvent.click(screen.getByText('Save'))
    await waitFor(() => expect(mockUpdateGlobalEnv).toHaveBeenCalled())

    edit('second')
    finishSave()

    await waitFor(() => expect(mockGetGlobalEnv.mock.calls.length).toBeGreaterThan(1))
    await waitFor(() => expect(screen.getByText('Save')).not.toBeDisabled())
    expect(valueInput().value).toBe('second')
    expect(screen.getByText('Unsaved changes')).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })
})
