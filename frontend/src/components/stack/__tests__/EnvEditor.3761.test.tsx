/**
 * agent-os-3761: text typed while a save is in flight must survive the save's
 * own refetch. The save's success marks the editor clean and the refetch then
 * hydrates it, so before the fix the newer typing was wiped with no notice.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, waitFor, fireEvent, act } from '@testing-library/react'
import { renderWithProviders } from '../../../test/utils'
import { queryKeys } from '@/lib/query-keys'

type Payload = {
  hasEnvFile: true
  filename: string
  entries: Array<{ key: string; value: string; sensitive: boolean; comment: boolean; line: number }>
  raw?: string
  locked?: boolean
}

const envFile = (port: string): Payload => ({
  hasEnvFile: true,
  filename: '.env',
  entries: [{ key: 'PORT', value: port, sensitive: false, comment: false, line: 1 }],
  raw: `PORT=${port}\n`,
})

let disk = envFile('8080')
let finishSave: () => void = () => {}
const mockGetEnv = vi.fn()
const mockUpdateEnv = vi.fn()

vi.mock('@/lib/api', () => ({
  stacksApi: {
    getEnv: (...args: unknown[]) => mockGetEnv(...args),
    updateEnv: (...args: unknown[]) => mockUpdateEnv(...args),
    createEnv: vi.fn(),
    updateComposeAndEnv: vi.fn(),
  },
  apiClient: { get: vi.fn(), put: vi.fn(), post: vi.fn() },
}))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), warning: vi.fn(), info: vi.fn() },
}))

import { EnvEditor } from '../EnvEditor'

const portInput = () => screen.getAllByLabelText('Environment variable value 1')[0] as HTMLInputElement
const saveButton = () => screen.getAllByRole('button', { name: /^Save$/ })[0]

beforeEach(() => {
  vi.clearAllMocks()
  disk = envFile('8080')
  mockGetEnv.mockImplementation(() => Promise.resolve(disk))
  // The save stays in flight until the test calls finishSave.
  mockUpdateEnv.mockImplementation((_id: string, body: { entries?: Array<{ value: string }> }) => {
    return new Promise((resolve) => {
      finishSave = () => {
        disk = envFile(body.entries?.[0]?.value ?? '')
        resolve({ outcome: 'success', reason: 'Saved' })
      }
    })
  })
})

let queryClient: ReturnType<typeof renderWithProviders>['queryClient']

/** A file that also holds a secret, as the backend sends it locked or unlocked. */
const secretFile = (port: string, locked = false): Payload => ({
  hasEnvFile: true,
  filename: '.env',
  ...(locked ? { locked: true } : { raw: `PORT=${port}\nAPI_KEY=real\n` }),
  entries: [
    { key: 'PORT', value: port, sensitive: false, comment: false, line: 1 },
    { key: 'API_KEY', value: locked ? '' : 'real', sensitive: true, comment: false, line: 2 },
  ],
})

/** The file changes on disk (a pull, another tab), then the editor's query refetches. */
async function external(next: Payload) {
  disk = next
  const calls = mockGetEnv.mock.calls.length
  await act(async () => {
    await queryClient.invalidateQueries({ queryKey: queryKeys.stack.env('s1') })
  })
  await waitFor(() => expect(mockGetEnv.mock.calls.length).toBeGreaterThan(calls))
}

async function startSave(value: string) {
  ;({ queryClient } = renderWithProviders(<EnvEditor stackId="s1" />))
  await waitFor(() => expect(portInput().value).toBe('8080'))
  fireEvent.change(portInput(), { target: { value } })
  fireEvent.click(saveButton())
  await waitFor(() => expect(mockUpdateEnv).toHaveBeenCalledTimes(1))
}

async function finishAndRefetch() {
  const calls = mockGetEnv.mock.calls.length
  finishSave()
  await waitFor(() => expect(mockGetEnv.mock.calls.length).toBeGreaterThan(calls))
}

describe('EnvEditor typing during a save (agent-os-3761)', () => {
  it('3761: typing while the save is in flight survives the save\'s refetch; the editor stays dirty with no notice', async () => {
    await startSave('9090')
    fireEvent.change(portInput(), { target: { value: '9191' } })

    await finishAndRefetch()

    await waitFor(() => expect(saveButton()).not.toBeDisabled())
    expect(portInput().value).toBe('9191')
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('3761: the kept typing can be saved next', async () => {
    await startSave('9090')
    fireEvent.change(portInput(), { target: { value: '9191' } })
    await finishAndRefetch()
    await waitFor(() => expect(saveButton()).not.toBeDisabled())

    fireEvent.click(saveButton())

    await waitFor(() => expect(mockUpdateEnv).toHaveBeenCalledTimes(2))
    const body = mockUpdateEnv.mock.calls[1][1] as { entries: Array<{ value: string }> }
    expect(body.entries[0].value).toBe('9191')
    // _rowId is client-side identity and must not reach the wire.
    expect(body.entries.every((e) => !('_rowId' in e))).toBe(true)
  })

  it('3761: with no newer typing the editor takes the refetch and is clean', async () => {
    await startSave('9090')

    await finishAndRefetch()

    await waitFor(() => expect(saveButton()).toBeDisabled())
    expect(portInput().value).toBe('9090')
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('3761: once a save\'s refetch is consumed, a later change that equals that save is not taken for it', async () => {
    await startSave('9090')
    await finishAndRefetch()
    await waitFor(() => expect(saveButton()).toBeDisabled())
    expect(portInput().value).toBe('9090')
    await external(envFile('7070'))
    await waitFor(() => expect(portInput().value).toBe('7070'))
    fireEvent.change(portInput(), { target: { value: '6060' } })

    // Another tab puts back the very text the earlier save wrote.
    await external(envFile('9090'))

    expect(await screen.findByRole('alert')).toBeInTheDocument()
    expect(portInput().value).toBe('6060')
  })

  it('3761: a save that stayed dirty moves the baseline, so a later lock and unlock raise no changed-on-disk notice', async () => {
    disk = secretFile('8080')
    mockUpdateEnv.mockImplementation((_id: string, body: { entries?: Array<{ value: string }> }) => {
      return new Promise((resolve) => {
        finishSave = () => {
          disk = secretFile(body.entries?.[0]?.value ?? '')
          resolve({ outcome: 'success', reason: 'Saved' })
        }
      })
    })
    await startSave('9090')
    fireEvent.change(portInput(), { target: { value: '9191' } })
    await finishAndRefetch()
    await waitFor(() => expect(saveButton()).not.toBeDisabled())

    await external(secretFile('9090', true))
    expect(await screen.findByRole('alert')).toBeInTheDocument()
    await external(secretFile('9090'))

    // Only the locked-again alert was up; once unlocked nothing is, and the edit stays.
    await waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument())
    expect(portInput().value).toBe('9191')
  })

  it('3761: after a FAILED save (outcome failed), a later change equal to the failed body is not taken for that save', async () => {
    mockUpdateEnv.mockResolvedValue({ outcome: 'failed', reason: 'disk full' })
    await startSave('9090')
    await waitFor(() => expect(saveButton()).not.toBeDisabled())
    fireEvent.change(portInput(), { target: { value: '9191' } })

    // Another tab writes the very text the failed save tried to write.
    await external(envFile('9090'))

    expect(await screen.findByRole('alert')).toBeInTheDocument()
    expect(portInput().value).toBe('9191')
  })

  it('3761: after a REJECTED save, a later change equal to the failed body is not taken for that save', async () => {
    mockUpdateEnv.mockRejectedValue(new Error('network down'))
    await startSave('9090')
    await waitFor(() => expect(saveButton()).not.toBeDisabled())
    fireEvent.change(portInput(), { target: { value: '9191' } })

    await external(envFile('9090'))

    expect(await screen.findByRole('alert')).toBeInTheDocument()
    expect(portInput().value).toBe('9191')
  })

  it('3761: after a save that stayed dirty the raw text follows the file, so a pull equal to the edited rows raises no notice', async () => {
    await startSave('9090')
    fireEvent.change(portInput(), { target: { value: '9191' } })
    await finishAndRefetch()
    await waitFor(() => expect(saveButton()).not.toBeDisabled())

    // The pull brings exactly what the editor shows: nothing to warn about.
    await external(envFile('9191'))

    await waitFor(() => expect(saveButton()).toBeDisabled())
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(portInput().value).toBe('9191')
  })
})
