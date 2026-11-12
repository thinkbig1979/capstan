/**
 * agent-os-yt2f: the locked view now hides more than a value. A commented-out
 * secret arrives cut to "#KEY=", a multi-line secret's body rows arrive with key
 * and value blank, a URL credential in a comment arrives starred, a quoted
 * secret loses its quote. An editor that kept its edits across the lock
 * (agent-os-vqf5) must save the real rows after the unlock, never the locked
 * shapes, and must refuse to save when it cannot line the two payloads up.
 *
 * The payloads are exactly what the Go handlers produce (redactEnvResponse over
 * parseEnvFile, generated at e9e7c55), not hand-written.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { screen, waitFor, fireEvent, act } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { notifyManager } from '@tanstack/react-query'
import { renderWithProviders } from '../../../test/utils'
import { useEnvUnlockStore } from '@/stores/envUnlockStore'
import { useAuthStore } from '@/stores/authStore'
import { queryKeys } from '@/lib/query-keys'

const mockGetEnv = vi.fn()
const mockUpdateEnv = vi.fn()
const mockVerifyPassword = vi.fn()

vi.mock('@/lib/api', () => ({
  stacksApi: {
    getEnv: (...args: unknown[]) => mockGetEnv(...args),
    updateEnv: (...args: unknown[]) => mockUpdateEnv(...args),
    createEnv: vi.fn(),
    updateComposeAndEnv: vi.fn(),
  },
  authApi: { verifyPassword: (...args: unknown[]) => mockVerifyPassword(...args) },
  apiClient: { get: vi.fn(), put: vi.fn(), post: vi.fn() },
}))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), warning: vi.fn(), info: vi.fn() },
}))

import { EnvEditor } from '../EnvEditor'

// PORT=8080 / #OLD_SECRET=commentedvalue / SSH_KEY="-----BEGIN / bodylineAAAA / -----END" / TZ=UTC
const pemUnlocked = {"hasEnvFile":true,"filename":".env","entries":[{"key":"PORT","value":"8080","line":1,"sensitive":false},{"key":"","value":"#OLD_SECRET=commentedvalue","line":2,"sensitive":false,"comment":true},{"key":"SSH_KEY","value":"\"-----BEGIN","line":3,"sensitive":true},{"key":"bodylineAAAA","value":"bodylineAAAA","line":4,"sensitive":false,"comment":true},{"key":"","value":"-----END\"","line":5,"sensitive":false,"comment":true},{"key":"TZ","value":"UTC","line":6,"sensitive":false}],"raw":"PORT=8080\n#OLD_SECRET=commentedvalue\nSSH_KEY=\"-----BEGIN\nbodylineAAAA\n-----END\"\nTZ=UTC\n"}
const pemLocked = {"hasEnvFile":true,"filename":".env","entries":[{"key":"PORT","value":"8080","line":1,"sensitive":false},{"key":"","value":"#OLD_SECRET=","line":2,"sensitive":true,"comment":true},{"key":"SSH_KEY","value":"","line":3,"sensitive":true},{"key":"","value":"","line":4,"sensitive":true,"comment":true},{"key":"","value":"","line":5,"sensitive":true,"comment":true},{"key":"TZ","value":"UTC","line":6,"sensitive":false}],"locked":true}

// PORT=8080 / # backup at postgres://u:pw@h/db / RESTIC_REPOSITORY: sftp:u:pw@h:/r / DB_PASSWORD="s3 cret" / TZ=UTC
const urlUnlocked = {"hasEnvFile":true,"filename":".env","entries":[{"key":"PORT","value":"8080","line":1,"sensitive":false},{"key":"","value":"# backup at postgres://u:pw@h/db","line":2,"sensitive":false,"comment":true},{"key":"RESTIC_REPOSITORY","value":"RESTIC_REPOSITORY: sftp:u:pw@h:/r","line":3,"sensitive":true,"comment":true},{"key":"DB_PASSWORD","value":"s3 cret","line":4,"sensitive":true,"quote":"\""},{"key":"TZ","value":"UTC","line":5,"sensitive":false}],"raw":"PORT=8080\n# backup at postgres://u:pw@h/db\nRESTIC_REPOSITORY: sftp:u:pw@h:/r\nDB_PASSWORD=\"s3 cret\"\nTZ=UTC\n"}
const urlLocked = {"hasEnvFile":true,"filename":".env","entries":[{"key":"PORT","value":"8080","line":1,"sensitive":false},{"key":"","value":"# backup at postgres://***@h/db","line":2,"sensitive":true,"comment":true},{"key":"RESTIC_REPOSITORY","value":"","line":3,"sensitive":true,"comment":true},{"key":"DB_PASSWORD","value":"","line":4,"sensitive":true},{"key":"TZ","value":"UTC","line":5,"sensitive":false}],"locked":true}

// The PEM file with NEW=1 appended on disk while the editor was locked: one row
// more than the locked baseline, so the two cannot be lined up.
const pemPlusRowUnlocked = {"hasEnvFile":true,"filename":".env","entries":[{"key":"PORT","value":"8080","line":1,"sensitive":false},{"key":"","value":"#OLD_SECRET=commentedvalue","line":2,"sensitive":false,"comment":true},{"key":"SSH_KEY","value":"\"-----BEGIN","line":3,"sensitive":true},{"key":"bodylineAAAA","value":"bodylineAAAA","line":4,"sensitive":false,"comment":true},{"key":"","value":"-----END\"","line":5,"sensitive":false,"comment":true},{"key":"TZ","value":"UTC","line":6,"sensitive":false},{"key":"NEW","value":"1","line":7,"sensitive":false}],"raw":"PORT=8080\n#OLD_SECRET=commentedvalue\nSSH_KEY=\"-----BEGIN\nbodylineAAAA\n-----END\"\nTZ=UTC\nNEW=1\n"}

type Payload = typeof pemUnlocked | typeof pemLocked | typeof urlUnlocked | typeof urlLocked | typeof pemPlusRowUnlocked
type Entry = { key: string; value: string; comment?: boolean; quote?: string; inlineComment?: string }

let server: Payload
let queryClient: ReturnType<typeof renderWithProviders>['queryClient']

const input = (n: number) =>
  screen.getAllByLabelText(`Environment variable value ${n}`)[0] as HTMLInputElement
const keyInput = (n: number) =>
  screen.getAllByLabelText(`Environment variable key ${n}`)[0] as HTMLInputElement
const saveButton = () => screen.getAllByRole('button', { name: /^Save$/ })[0] as HTMLButtonElement

/** Every field a table save writes back, so a lost quote or comment flag shows. */
const wire = (e: Entry) => [e.key, e.value, !!e.comment, e.quote ?? '', e.inlineComment ?? '']
const savedRows = () => (mockUpdateEnv.mock.calls[0][1] as { entries: Entry[] }).entries.map(wire)
/** The unlocked file's rows, with the given row overrides. */
const realRows = (p: { entries: Entry[] }, overrides: Record<number, Partial<Entry>> = {}) =>
  p.entries.map((e, i) => wire({ ...e, ...overrides[i] }))

function renderEditor() {
  ;({ queryClient } = renderWithProviders(<EnvEditor stackId="s1" />))
}

async function serve(next: Payload) {
  server = next
  const calls = mockGetEnv.mock.calls.length
  await act(async () => {
    await queryClient.invalidateQueries({ queryKey: queryKeys.stack.env('s1') })
  })
  await waitFor(() => expect(mockGetEnv.mock.calls.length).toBeGreaterThan(calls))
}

async function save() {
  fireEvent.click(saveButton())
  await waitFor(() => expect(mockUpdateEnv).toHaveBeenCalledTimes(1))
}

/** Locked from the first load, PORT edited, then the unlocked payload arrives. */
async function editLockedThenUnlock(locked: Payload, unlocked: Payload) {
  server = locked
  renderEditor()
  await waitFor(() => expect(input(1).value).toBe('8080'))
  fireEvent.change(input(1), { target: { value: '9090' } })
  await serve(unlocked)
  await waitFor(() => expect(saveButton()).not.toBeDisabled())
}

beforeEach(() => {
  vi.clearAllMocks()
  act(() => useEnvUnlockStore.getState().lock())
  useAuthStore.setState({ authDisabled: false })
  server = pemUnlocked
  mockGetEnv.mockImplementation(() => Promise.resolve(server))
  mockUpdateEnv.mockResolvedValue({ outcome: 'success', reason: 'Saved' })
})

afterEach(() => {
  act(() => useEnvUnlockStore.getState().lock())
})

describe('EnvEditor saves the real rows after an unlock, not the locked shapes (agent-os-yt2f)', () => {
  it('yt2f: a commented secret and a PEM body come back whole', async () => {
    await editLockedThenUnlock(pemLocked, pemUnlocked)

    await save()
    expect(savedRows()).toEqual(realRows(pemUnlocked, { 0: { value: '9090' } }))
  })

  it('yt2f: a starred URL credential in a comment, a colon row and a quoted secret come back whole', async () => {
    await editLockedThenUnlock(urlLocked, urlUnlocked)

    await save()
    expect(savedRows()).toEqual(realRows(urlUnlocked, { 0: { value: '9090' } }))
  })

  it('yt2f: Undo and Redo after the unlock never bring a locked shape back', async () => {
    server = pemLocked
    renderEditor()
    await waitFor(() => expect(input(1).value).toBe('8080'))
    fireEvent.change(input(1), { target: { value: '9090' } })
    fireEvent.change(input(1), { target: { value: '9191' } })
    await serve(pemUnlocked)
    await waitFor(() => expect(saveButton()).not.toBeDisabled())

    const keyboard = userEvent.setup().keyboard
    await keyboard('{Control>}z{/Control}')
    await keyboard('{Control>}z{/Control}')
    await waitFor(() => expect(input(1).value).toBe('8080'))
    await keyboard('{Control>}y{/Control}')
    await waitFor(() => expect(input(1).value).toBe('9090'))

    await save()
    expect(savedRows()).toEqual(realRows(pemUnlocked, { 0: { value: '9090' } }))
  })

  it('yt2f: a secret renamed while locked keeps its own body', async () => {
    server = pemLocked
    renderEditor()
    await waitFor(() => expect(input(1).value).toBe('8080'))
    fireEvent.change(keyInput(3), { target: { value: 'DEPLOY_KEY' } })
    await serve(pemUnlocked)
    await waitFor(() => expect(saveButton()).not.toBeDisabled())

    await save()
    expect(savedRows()).toEqual(realRows(pemUnlocked, { 2: { key: 'DEPLOY_KEY' } }))
  })

  it('yt2f: a commented secret deleted while locked stays deleted, and the rows after it keep their own values', async () => {
    server = pemLocked
    renderEditor()
    await waitFor(() => expect(input(1).value).toBe('8080'))
    fireEvent.click(screen.getAllByLabelText('Delete entry 2')[0])
    await serve(pemUnlocked)
    await waitFor(() => expect(saveButton()).not.toBeDisabled())

    await save()
    expect(savedRows()).toEqual(realRows(pemUnlocked).filter((_, i) => i !== 1))
  })
})

describe('EnvEditor refuses to save rows it cannot line up (agent-os-yt2f)', () => {
  it('yt2f: the file gained a row while locked: Save is refused, nothing is written, Reload loads the file', async () => {
    server = pemLocked
    renderEditor()
    await waitFor(() => expect(input(1).value).toBe('8080'))
    fireEvent.change(input(1), { target: { value: '9090' } })
    await serve(pemPlusRowUnlocked)

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(/can't be saved/)
    expect(screen.getAllByRole('alert')).toHaveLength(1)
    expect(input(1).value).toBe('9090')
    expect(saveButton()).toBeDisabled()
    fireEvent.click(saveButton())

    // The Raw view's Save is refused too.
    await userEvent.setup().click(screen.getByRole('tab', { name: 'Raw Editor' }))
    const rawSave = screen.getAllByRole('button', { name: /^Save$/ }).at(-1) as HTMLButtonElement
    expect(rawSave).toBeDisabled()
    fireEvent.click(rawSave)
    expect(mockUpdateEnv).not.toHaveBeenCalled()

    fireEvent.click(screen.getByRole('button', { name: 'Reload' }))
    await waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument())
    await userEvent.setup().click(screen.getByRole('tab', { name: 'Table View' }))
    expect(input(1).value).toBe('8080')
    fireEvent.change(input(1), { target: { value: '9090' } })
    await save()
    expect(savedRows()).toEqual(realRows(pemPlusRowUnlocked, { 0: { value: '9090' } }))
  })
})

const SCHEDULERS: Record<string, (cb: () => void) => void> = {
  'default (setTimeout 0)': (cb) => void setTimeout(cb, 0),
  'microtask': (cb) => queueMicrotask(cb),
  'synchronous': (cb) => cb(),
}

describe.each(Object.entries(SCHEDULERS))('EnvEditor unlocked through the eye button, scheduler: %s (agent-os-yt2f)', (_name, scheduler) => {
  beforeEach(() => {
    notifyManager.setScheduler(scheduler)
    server = pemLocked
    mockVerifyPassword.mockImplementation(() => {
      server = pemUnlocked
      return Promise.resolve({ ok: true, unlockToken: 'tok', expiresIn: 300 })
    })
  })

  afterEach(() => {
    notifyManager.setScheduler((cb) => void setTimeout(cb, 0))
  })

  it('yt2f: the stale reveal toggle and the refetch, in either order, still save the real rows', async () => {
    renderWithProviders(<EnvEditor stackId="s1" />)
    const user = userEvent.setup()
    await waitFor(() => expect(input(1).value).toBe('8080'))
    fireEvent.change(input(1), { target: { value: '9090' } })
    await user.click(screen.getAllByRole('button', { name: 'Toggle visibility for entry 3' })[0])
    await screen.findByText('Unlock environment variables')
    await user.type(screen.getByLabelText('Password'), 'pw')
    await user.click(screen.getByRole('button', { name: 'Unlock' }))
    await waitFor(() => expect(input(3).value).toBe('"-----BEGIN'))

    expect(input(1).value).toBe('9090')
    await save()
    expect(savedRows()).toEqual(realRows(pemUnlocked, { 0: { value: '9090' } }))
  })
})
