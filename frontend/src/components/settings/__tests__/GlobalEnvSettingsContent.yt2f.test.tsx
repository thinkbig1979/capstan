/**
 * agent-os-yt2f, global env half: the locked view also blanks a value whose key
 * is not secret-looking, such as a DATABASE_URL carrying a password. A form that
 * kept its edits across the lock (agent-os-vqf5) must save the real value after
 * the unlock, never that blank, and must refuse to save when it cannot line the
 * two payloads up.
 *
 * The payloads are exactly what the Go handler produces (redactGlobalEnvVars
 * over parseGlobalEnvText, generated at e9e7c55), not hand-written.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { GlobalEnvSettingsContent } from '../GlobalEnvSettingsContent'
import { useEnvUnlockStore } from '@/stores/envUnlockStore'
import { useAuthStore } from '@/stores/authStore'
import { queryKeys } from '@/lib/query-keys'

type Payload = { vars: Array<{ key: string; value: string }>; locked?: boolean }

// PORT=8080 / DATABASE_URL=postgres://u:pw@h/db / API_TOKEN=tok / TZ=UTC
const unlockedPayload: Payload = {"locked":false,"vars":[{"key":"PORT","value":"8080"},{"key":"DATABASE_URL","value":"postgres://u:pw@h/db"},{"key":"API_TOKEN","value":"tok"},{"key":"TZ","value":"UTC"}]}
const lockedPayload: Payload = {"locked":true,"vars":[{"key":"PORT","value":"8080"},{"key":"DATABASE_URL","value":""},{"key":"API_TOKEN","value":""},{"key":"TZ","value":"UTC"}]}
// PORT=8080 / SSH_KEY="-----BEGIN / AAA=bbb / -----END" / TZ=UTC: the body line
// that looks like an assignment comes back locked with its key blanked too.
const pemUnlocked: Payload = {"locked":false,"vars":[{"key":"PORT","value":"8080"},{"key":"SSH_KEY","value":"\"-----BEGIN"},{"key":"AAA","value":"bbb"},{"key":"TZ","value":"UTC"}]}
const pemLocked: Payload = {"locked":true,"vars":[{"key":"PORT","value":"8080"},{"key":"SSH_KEY","value":""},{"key":"","value":""},{"key":"TZ","value":"UTC"}]}
// The same file with NEW=1 appended while the form was locked.
const plusRowUnlocked: Payload = {"locked":false,"vars":[{"key":"PORT","value":"8080"},{"key":"DATABASE_URL","value":"postgres://u:pw@h/db"},{"key":"API_TOKEN","value":"tok"},{"key":"TZ","value":"UTC"},{"key":"NEW","value":"1"}]}

let server: Payload
let client: QueryClient
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

function renderForm() {
  client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <GlobalEnvSettingsContent />
    </QueryClientProvider>,
  )
}

const value = (n: number) => screen.getAllByLabelText(`Global env value ${n}`)[0] as HTMLInputElement
const keyInput = (n: number) => screen.getAllByLabelText(`Global env key ${n}`)[0] as HTMLInputElement
const saveButton = () => screen.getByText('Save').closest('button') as HTMLButtonElement

async function serve(next: Payload) {
  server = next
  const calls = mockGetGlobalEnv.mock.calls.length
  await act(async () => {
    await client.invalidateQueries({ queryKey: queryKeys.settings.globalEnv() })
  })
  await waitFor(() => expect(mockGetGlobalEnv.mock.calls.length).toBeGreaterThan(calls))
}

async function save() {
  fireEvent.click(saveButton())
  await waitFor(() => expect(mockUpdateGlobalEnv).toHaveBeenCalledTimes(1))
}

/** Locked from the first load, then `edit` runs, then the unlocked payload arrives. */
async function editLockedThenUnlock(edit: () => void, unlocked: Payload = unlockedPayload) {
  server = lockedPayload
  renderForm()
  await waitFor(() => expect(value(1).value).toBe('8080'))
  edit()
  await serve(unlocked)
}

beforeEach(() => {
  vi.clearAllMocks()
  act(() => useEnvUnlockStore.getState().lock())
  useAuthStore.setState({ authDisabled: false })
  server = unlockedPayload
  mockGetGlobalEnv.mockImplementation(() => Promise.resolve(server))
  mockUpdateGlobalEnv.mockResolvedValue(undefined)
})

describe('global env form saves the real values after an unlock (agent-os-yt2f)', () => {
  it('yt2f: a DATABASE_URL blanked for its URL credential comes back, not the blank', async () => {
    await editLockedThenUnlock(() => fireEvent.change(value(1), { target: { value: '9090' } }))
    await waitFor(() => expect(saveButton()).not.toBeDisabled())

    await save()
    expect(mockUpdateGlobalEnv).toHaveBeenCalledWith([
      { key: 'PORT', value: '9090' },
      { key: 'DATABASE_URL', value: 'postgres://u:pw@h/db' },
      { key: 'API_TOKEN', value: 'tok' },
      { key: 'TZ', value: 'UTC' },
    ])
  })

  it('yt2f: a PEM body line whose key the locked view blanked comes back, key and value', async () => {
    server = pemLocked
    renderForm()
    await waitFor(() => expect(value(1).value).toBe('8080'))
    fireEvent.change(value(1), { target: { value: '9090' } })
    await serve(pemUnlocked)
    await waitFor(() => expect(saveButton()).not.toBeDisabled())

    await save()
    expect(mockUpdateGlobalEnv).toHaveBeenCalledWith([
      { key: 'PORT', value: '9090' },
      { key: 'SSH_KEY', value: '"-----BEGIN' },
      { key: 'AAA', value: 'bbb' },
      { key: 'TZ', value: 'UTC' },
    ])
  })

  it('yt2f: a DATABASE_URL renamed while locked keeps its own value', async () => {
    await editLockedThenUnlock(() => fireEvent.change(keyInput(2), { target: { value: 'DB_URL' } }))
    await waitFor(() => expect(saveButton()).not.toBeDisabled())

    await save()
    expect(mockUpdateGlobalEnv).toHaveBeenCalledWith([
      { key: 'PORT', value: '8080' },
      { key: 'DB_URL', value: 'postgres://u:pw@h/db' },
      { key: 'API_TOKEN', value: 'tok' },
      { key: 'TZ', value: 'UTC' },
    ])
  })

  it('yt2f: a secret deleted while locked stays deleted, and the others keep their own values', async () => {
    await editLockedThenUnlock(() => fireEvent.click(screen.getAllByLabelText('Remove global env 3')[0]))
    await waitFor(() => expect(saveButton()).not.toBeDisabled())

    await save()
    expect(mockUpdateGlobalEnv).toHaveBeenCalledWith([
      { key: 'PORT', value: '8080' },
      { key: 'DATABASE_URL', value: 'postgres://u:pw@h/db' },
      { key: 'TZ', value: 'UTC' },
    ])
  })
})

describe('global env form refuses to save rows it cannot line up (agent-os-yt2f)', () => {
  it('yt2f: the file gained a row while locked: Save is refused, nothing is written, Reload loads the file', async () => {
    await editLockedThenUnlock(() => fireEvent.change(value(1), { target: { value: '9090' } }), plusRowUnlocked)

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(/can't be saved/)
    expect(screen.getAllByRole('alert')).toHaveLength(1)
    expect(value(1).value).toBe('9090')
    expect(saveButton()).toBeDisabled()
    fireEvent.click(saveButton())
    // A mutation calls the API after a tick: let it, so "not called" can fail.
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 20))
    })
    expect(mockUpdateGlobalEnv).not.toHaveBeenCalled()

    fireEvent.click(screen.getByRole('button', { name: 'Reload' }))
    await waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument())
    expect(value(1).value).toBe('8080')
    fireEvent.change(value(1), { target: { value: '9090' } })
    await save()
    expect(mockUpdateGlobalEnv.mock.calls).toEqual([[[
      { key: 'PORT', value: '9090' },
      { key: 'DATABASE_URL', value: 'postgres://u:pw@h/db' },
      { key: 'API_TOKEN', value: 'tok' },
      { key: 'TZ', value: 'UTC' },
      { key: 'NEW', value: '1' },
    ]]])
  })
})
