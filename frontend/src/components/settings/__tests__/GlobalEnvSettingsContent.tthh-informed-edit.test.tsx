/**
 * agent-os-tthh, the global twin of agent-os-ocnh: a row edited blind while
 * locked refuses Save after the unlock. Once its value is in view, what the
 * user types into it is a choice, not a blind edit, so Save must take it. An
 * edit while the value is masked, an edit while the session is locked again,
 * and an edit that leaves the key blank (the save would drop the real row) stay
 * refused.
 *
 * Payloads as in GlobalEnvSettingsContent.yt2f.test.tsx (redactGlobalEnvVars'
 * fail-closed branch: every key and value blank while locked).
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { GlobalEnvSettingsContent } from '../GlobalEnvSettingsContent'
import { useEnvUnlockStore } from '@/stores/envUnlockStore'
import { useAuthStore } from '@/stores/authStore'
import { queryKeys } from '@/lib/query-keys'

type Payload = { vars: Array<{ key: string; value: string }>; locked?: boolean }

// PORT=8080 / DB_PASSWORD=pw / TZ=UTC
const unlocked: Payload = {"locked":false,"vars":[{"key":"PORT","value":"8080"},{"key":"DB_PASSWORD","value":"pw"},{"key":"TZ","value":"UTC"}]}
const locked: Payload = {"locked":true,"vars":[{"key":"","value":""},{"key":"","value":""},{"key":"","value":""}]}

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

async function afterATick() {
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 20))
  })
}

async function expectSaveRefused() {
  expect(await screen.findByRole('alert')).toHaveTextContent(/can't be saved/)
  expect(saveButton()).toBeDisabled()
  fireEvent.click(saveButton())
  await afterATick()
  expect(mockUpdateGlobalEnv).not.toHaveBeenCalled()
}

/** Locked from the first load, `edit` runs on the hidden rows, then the unlock lands. */
async function editBlindThenUnlock(edit: () => void) {
  server = locked
  client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  render(
    <QueryClientProvider client={client}>
      <GlobalEnvSettingsContent />
    </QueryClientProvider>,
  )
  await waitFor(() => expect(keyInput(3)).toBeInTheDocument())
  edit()
  // An unlock opens the session window and then serves the unlocked payload.
  act(() => useEnvUnlockStore.getState().unlock('tok'))
  await serve(unlocked)
  await expectSaveRefused()
}

beforeEach(() => {
  vi.clearAllMocks()
  act(() => useEnvUnlockStore.getState().lock())
  useAuthStore.setState({ authDisabled: false })
  mockGetGlobalEnv.mockImplementation(() => Promise.resolve(server))
  mockUpdateGlobalEnv.mockResolvedValue(undefined)
})

describe('global env: an edit made with the value in view lifts the blind-edit refusal (agent-os-tthh)', () => {
  it('tthh: a value typed blind, then a key typed with the value visible: Save sends what is on screen', async () => {
    await editBlindThenUnlock(() => fireEvent.change(value(2), { target: { value: 'typed' } }))
    expect(value(2).type).toBe('text') // the value is in view
    fireEvent.change(keyInput(2), { target: { value: 'FOO' } })

    await waitFor(() => expect(saveButton()).not.toBeDisabled())
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    fireEvent.click(saveButton())
    await waitFor(() => expect(mockUpdateGlobalEnv).toHaveBeenCalledTimes(1))
    expect(mockUpdateGlobalEnv.mock.calls).toEqual([[[
      { key: 'PORT', value: '8080' },
      { key: 'FOO', value: 'typed' },
      { key: 'TZ', value: 'UTC' },
    ]]])
  })

  // The global page has no Undo, so the ocnh "Undo back to the blind edit" arm
  // has no route here. Its counterpart: only the content last typed in view is
  // trusted, so an edit after it made while the value is masked is refused again.
  it('tthh: after an informed edit, a later edit made while the value is masked is refused again', async () => {
    await editBlindThenUnlock(() => fireEvent.change(value(2), { target: { value: 'typed' } }))
    fireEvent.change(keyInput(2), { target: { value: 'DB_PASSWORD' } }) // typed in view; the value masks now
    await waitFor(() => expect(saveButton()).not.toBeDisabled())
    expect(value(2).type).toBe('password')

    fireEvent.change(value(2), { target: { value: 'guess' } })
    await expectSaveRefused()
  })

  // D1 amendment (review F1, probe P-e): a row whose key stays blank is dropped
  // by the save, so typing into it in view would let Save drop the real row the
  // user never saw (DB_PASSWORD here), with no notice. Such an edit is not informed.
  it('tthh: an edit that leaves the key blank does not lift the refusal (review F1)', async () => {
    await editBlindThenUnlock(() => fireEvent.change(value(2), { target: { value: 'typed' } }))
    expect(keyInput(2).value).toBe('')
    expect(value(2).type).toBe('text') // the value is in view
    fireEvent.change(value(2), { target: { value: 'guess' } })

    await expectSaveRefused()
  })

  // Review F2, probe P-b: an edit made after a lock lands, before its payload,
  // is not informed even though the row still shows the unlocked value.
  it('tthh: an edit made while the session is locked again is refused after the next unlock (review F2)', async () => {
    await editBlindThenUnlock(() => fireEvent.change(value(2), { target: { value: 'typed' } }))
    fireEvent.change(keyInput(2), { target: { value: 'FOO' } })
    await waitFor(() => expect(saveButton()).not.toBeDisabled())

    act(() => useEnvUnlockStore.getState().lock()) // the lock's refetch has not landed
    fireEvent.change(value(2), { target: { value: 'while-stale' } })
    await serve(locked)
    act(() => useEnvUnlockStore.getState().unlock('tok'))
    await serve(unlocked)
    await expectSaveRefused()
  })

  it('tthh: control: a secret-looking key typed blind, then its value edited while masked: still refused', async () => {
    await editBlindThenUnlock(() => fireEvent.change(keyInput(2), { target: { value: 'DB_PASSWORD' } }))
    expect(value(2).type).toBe('password') // the value is masked
    fireEvent.change(value(2), { target: { value: 'guess' } })

    await expectSaveRefused()
  })
})
