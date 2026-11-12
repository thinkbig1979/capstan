/**
 * agent-os-3761: text typed while a save is in flight must survive the save's
 * own refetch. The save's success marks the editor clean and the refetch then
 * hydrates it, so before the fix the newer typing was wiped with no notice.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, waitFor, fireEvent } from '@testing-library/react'
import { renderWithProviders } from '../../../test/utils'

type Payload = {
  hasEnvFile: true
  filename: string
  entries: Array<{ key: string; value: string; sensitive: boolean; comment: boolean; line: number }>
  raw: string
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

async function startSave(value: string) {
  renderWithProviders(<EnvEditor stackId="s1" />)
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
  })

  it('3761: with no newer typing the editor takes the refetch and is clean', async () => {
    await startSave('9090')

    await finishAndRefetch()

    await waitFor(() => expect(saveButton()).toBeDisabled())
    expect(portInput().value).toBe('9090')
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })
})
