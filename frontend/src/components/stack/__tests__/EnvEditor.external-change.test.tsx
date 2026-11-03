/**
 * agent-os-umig, env half: a git pull rewrites the .env file on disk. A clean
 * env editor shows the pulled values; one with unsaved edits keeps them and
 * offers Reload. A lock-state change keeps the plain reload (agent-os-vqf5
 * owns the edits that still discards). Table mode only: the raw view is
 * hydrated by the same block in EnvEditor.tsx.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, waitFor, fireEvent } from '@testing-library/react'
import { QueryClient } from '@tanstack/react-query'
import { renderWithProviders } from '../../../test/utils'

type Payload = {
  hasEnvFile: true
  filename: string
  entries: Array<{ key: string; value: string; sensitive: boolean; comment: boolean; line: number }>
  raw?: string
  locked?: boolean
}

const envFile = (port: string, extra: Partial<Payload> = {}): Payload => ({
  hasEnvFile: true,
  filename: '.env',
  entries: [{ key: 'PORT', value: port, sensitive: false, comment: false, line: 1 }],
  raw: `PORT=${port}\n`,
  ...extra,
})

let disk: Payload = envFile('8080')
const mockGetEnv = vi.fn()
const mockUpdateEnv = vi.fn()
const mockGitPull = vi.fn()

vi.mock('@/lib/api', () => ({
  stacksApi: {
    getEnv: (...args: unknown[]) => mockGetEnv(...args),
    updateEnv: (...args: unknown[]) => mockUpdateEnv(...args),
    createEnv: vi.fn(),
    updateComposeAndEnv: vi.fn(),
  },
  gitApi: {
    pull: (...args: unknown[]) => mockGitPull(...args),
  },
  apiClient: { get: vi.fn(), put: vi.fn(), post: vi.fn() },
}))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), warning: vi.fn(), info: vi.fn() },
}))

import { EnvEditor } from '../EnvEditor'
import { useGitPull } from '@/hooks/useGit'

const NOTICE = 'This file changed on disk. Reload to see the new version; saving now overwrites it.'

function EditorWithPull() {
  const pull = useGitPull()
  return (
    <>
      <button type="button" onClick={() => pull.mutate({ stackId: 's1' })}>
        Pull now
      </button>
      <EnvEditor stackId="s1" />
    </>
  )
}

function renderEditor() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  renderWithProviders(<EditorWithPull />, { queryClient })
}

// Desktop and mobile renderings both carry the label; the first is enough.
const portInput = () => screen.getAllByLabelText('Environment variable value 1')[0] as HTMLInputElement

async function loaded(port = '8080') {
  await waitFor(() => expect(portInput().value).toBe(port))
}

function edit(value: string) {
  fireEvent.change(portInput(), { target: { value } })
}

async function pullTo(next: Payload) {
  disk = next
  const calls = mockGetEnv.mock.calls.length
  fireEvent.click(screen.getByText('Pull now'))
  await waitFor(() => expect(mockGetEnv.mock.calls.length).toBeGreaterThan(calls))
}

beforeEach(() => {
  vi.clearAllMocks()
  disk = envFile('8080')
  mockGetEnv.mockImplementation(() => Promise.resolve(disk))
  mockUpdateEnv.mockImplementation((_id: string, body: { entries?: Array<{ value: string }> }) => {
    disk = envFile(body.entries?.[0]?.value ?? '')
    return Promise.resolve({ outcome: 'success', reason: 'Saved' })
  })
  mockGitPull.mockResolvedValue({ outcome: 'success', reason: 'Pulled' })
})

describe('EnvEditor after a git pull (agent-os-umig)', () => {
  it('a clean editor shows the pulled values', async () => {
    renderEditor()
    await loaded()

    await pullTo(envFile('7070'))

    await waitFor(() => expect(portInput().value).toBe('7070'))
    expect(screen.queryByText(NOTICE)).not.toBeInTheDocument()
  })

  it('unsaved edits stay, the notice shows, and Reload loads the pulled values', async () => {
    renderEditor()
    await loaded()
    edit('9090')

    await pullTo(envFile('7070'))

    expect(await screen.findByText(NOTICE)).toBeInTheDocument()
    expect(portInput().value).toBe('9090')
    expect(screen.getByText('Save')).not.toBeDisabled()

    fireEvent.click(screen.getByText('Reload'))

    await waitFor(() => expect(portInput().value).toBe('7070'))
    expect(screen.queryByText(NOTICE)).not.toBeInTheDocument()
    expect(screen.getByText('Save')).toBeDisabled()
  })

  it('a second change while the notice is up: Reload loads the newest values', async () => {
    renderEditor()
    await loaded()
    edit('9090')
    await pullTo(envFile('7070'))
    expect(await screen.findByText(NOTICE)).toBeInTheDocument()
    await pullTo(envFile('6060'))
    await waitFor(() => expect(mockGetEnv).toHaveBeenCalledTimes(3))

    fireEvent.click(screen.getByText('Reload'))

    await waitFor(() => expect(portInput().value).toBe('6060'))
  })

  it('no notice when the pulled values equal what the editor already shows', async () => {
    renderEditor()
    await loaded()
    edit('7070')

    await pullTo(envFile('7070'))

    await waitFor(() => expect(screen.getByText('Save')).toBeDisabled())
    expect(screen.queryByText(NOTICE)).not.toBeInTheDocument()
    expect(portInput().value).toBe('7070')
  })

  it('no notice and edits kept when the file on disk did not change', async () => {
    renderEditor()
    await loaded()
    edit('9090')

    await pullTo(envFile('8080'))

    expect(screen.queryByText(NOTICE)).not.toBeInTheDocument()
    expect(portInput().value).toBe('9090')
    expect(screen.getByText('Save')).not.toBeDisabled()
  })

  it('a successful save clears the notice', async () => {
    renderEditor()
    await loaded()
    edit('9090')
    await pullTo(envFile('7070'))
    expect(await screen.findByText(NOTICE)).toBeInTheDocument()

    fireEvent.click(screen.getByText('Save'))

    await waitFor(() => expect(mockUpdateEnv).toHaveBeenCalled())
    await waitFor(() => expect(screen.queryByText(NOTICE)).not.toBeInTheDocument())
    expect(portInput().value).toBe('9090')
  })

  it('a failed save keeps the notice', async () => {
    mockUpdateEnv.mockResolvedValue({ outcome: 'failed', reason: 'disk full' })
    renderEditor()
    await loaded()
    edit('9090')
    await pullTo(envFile('7070'))
    expect(await screen.findByText(NOTICE)).toBeInTheDocument()

    fireEvent.click(screen.getByText('Save'))

    await waitFor(() => expect(mockUpdateEnv).toHaveBeenCalled())
    await waitFor(() => expect(screen.getByText('Save')).not.toBeDisabled())
    expect(screen.getByText(NOTICE)).toBeInTheDocument()
    expect(portInput().value).toBe('9090')
  })

  it('a lock-state change keeps the plain reload (no notice; agent-os-vqf5)', async () => {
    renderEditor()
    await loaded()
    edit('9090')

    await pullTo(envFile('7070', { locked: true, raw: undefined }))

    await waitFor(() => expect(portInput().value).toBe('7070'))
    expect(screen.queryByText(NOTICE)).not.toBeInTheDocument()
  })
})
