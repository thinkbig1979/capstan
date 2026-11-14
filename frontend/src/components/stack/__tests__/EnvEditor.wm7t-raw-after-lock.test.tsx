/**
 * agent-os-wm7t: the Raw tab was gated on the last payload's `locked` flag, so
 * after a lock it kept showing the whole file until the lock's refetch landed,
 * and for good if that refetch failed. Sequence from the review of PR #738
 * (probe C).
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { screen, waitFor, fireEvent, act } from '@testing-library/react'
import { renderWithProviders } from '../../../test/utils'
import { useEnvUnlockStore } from '@/stores/envUnlockStore'
import { useAuthStore } from '@/stores/authStore'
import { invalidateEnvUnlockQueries } from '@/lib/env-unlock-queries'
const mockGetEnv = vi.fn()

vi.mock('@/lib/api', () => ({
  stacksApi: {
    getEnv: (...args: unknown[]) => mockGetEnv(...args),
    updateEnv: vi.fn(),
    createEnv: vi.fn(),
    updateComposeAndEnv: vi.fn(),
  },
  authApi: { verifyPassword: vi.fn() },
  apiClient: { get: vi.fn(), put: vi.fn(), post: vi.fn() },
}))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), warning: vi.fn(), info: vi.fn() },
}))

import { EnvEditor } from '../EnvEditor'

const PEM = '-----BEGIN PRIVATE KEY-----'

const unlocked = {
  hasEnvFile: true,
  filename: '.env',
  entries: [
    { key: 'PORT', value: '8080', sensitive: false, comment: false, line: 1 },
    { key: 'TLS', value: PEM, sensitive: true, comment: false, line: 2 },
    { key: 'DB_PASSWORD', value: 'hunter2', sensitive: true, comment: false, line: 3 },
  ],
  raw: `PORT=8080\nTLS=${PEM}\nDB_PASSWORD=hunter2\n`,
}

const lockedPayload = {
  hasEnvFile: true,
  filename: '.env',
  locked: true,
  entries: [
    { key: 'PORT', value: '8080', sensitive: false, comment: false, line: 1 },
    { key: 'TLS', value: '', sensitive: true, comment: false, line: 2 },
    { key: 'DB_PASSWORD', value: '', sensitive: true, comment: false, line: 3 },
  ],
}

const val = (n: number) => screen.getAllByLabelText(`Environment variable value ${n}`)[0] as HTMLInputElement

async function loadUnlocked() {
  act(() => useEnvUnlockStore.getState().unlock('tok'))
  const r = renderWithProviders(<EnvEditor stackId="s1" />)
  await waitFor(() => expect(val(2).value).toBe(PEM))
  return r
}

function setup() {
  beforeEach(() => {
    vi.clearAllMocks()
    act(() => useEnvUnlockStore.getState().lock())
    useAuthStore.setState({ authDisabled: false })
    mockGetEnv.mockResolvedValue(unlocked)
  })
  afterEach(() => {
    act(() => useEnvUnlockStore.getState().lock())
  })
}

async function openRawUnlocked() {
  const r = await loadUnlocked()
  fireEvent.mouseDown(screen.getByRole('tab', { name: 'Raw Editor' }))
  fireEvent.click(screen.getByRole('tab', { name: 'Raw Editor' }))
  await waitFor(() => expect(screen.getByPlaceholderText('KEY=value')).toBeInTheDocument())
  expect((screen.getByPlaceholderText('KEY=value') as HTMLTextAreaElement).value).toContain('hunter2')
  return r
}

describe('EnvEditor: the Raw view after a lock (agent-os-wm7t)', () => {
  setup()

  it('the lock refetch fails: the raw text does not show the file', async () => {
    const { queryClient } = await openRawUnlocked()
    mockGetEnv.mockRejectedValue(new Error('offline'))

    act(() => useEnvUnlockStore.getState().lock())
    await act(() => invalidateEnvUnlockQueries(queryClient))
    await waitFor(() => expect(mockGetEnv).toHaveBeenCalledTimes(2))

    const raw = screen.queryByPlaceholderText('KEY=value') as HTMLTextAreaElement | null
    expect(raw?.value ?? '').not.toContain('hunter2')
  })

  it('before the lock refetch lands: the raw text does not show the file', async () => {
    await openRawUnlocked()

    act(() => useEnvUnlockStore.getState().lock())

    const raw = screen.queryByPlaceholderText('KEY=value') as HTMLTextAreaElement | null
    expect(raw?.value ?? '').not.toContain('hunter2')
  })

  it('control: the lock refetch succeeds on a clean editor, the Raw view goes away', async () => {
    const { queryClient } = await openRawUnlocked()
    mockGetEnv.mockResolvedValue(lockedPayload)

    act(() => useEnvUnlockStore.getState().lock())
    await act(() => invalidateEnvUnlockQueries(queryClient))

    await waitFor(() => expect(screen.queryByPlaceholderText('KEY=value')).toBeNull())
  })

  it('unlocking again after a failed lock refetch brings the Raw tab and the raw edits back', async () => {
    const { queryClient } = await openRawUnlocked()
    const textarea = () => screen.getByPlaceholderText('KEY=value') as HTMLTextAreaElement
    fireEvent.change(textarea(), { target: { value: `${textarea().value}EXTRA=1\n` } })
    mockGetEnv.mockRejectedValue(new Error('offline'))
    act(() => useEnvUnlockStore.getState().lock())
    await act(() => invalidateEnvUnlockQueries(queryClient))
    await waitFor(() => expect(mockGetEnv).toHaveBeenCalledTimes(2))
    expect(screen.queryByRole('tab', { name: 'Raw Editor' })).toBeNull()

    act(() => useEnvUnlockStore.getState().unlock('tok'))

    fireEvent.mouseDown(screen.getByRole('tab', { name: 'Raw Editor' }))
    fireEvent.click(screen.getByRole('tab', { name: 'Raw Editor' }))
    await waitFor(() => expect(textarea().value).toContain('EXTRA=1'))
    expect(textarea().value).toContain('hunter2')
  })
})

describe('EnvEditor: the Raw view gates on both the payload and the store (agent-os-wm7t)', () => {
  setup()

  it('the store is unlocked but the server answers locked (its token expired first): no Raw tab or view', async () => {
    mockGetEnv.mockResolvedValue(lockedPayload)
    act(() => useEnvUnlockStore.getState().unlock('tok'))
    renderWithProviders(<EnvEditor stackId="s1" />)
    await waitFor(() => expect(val(1).value).toBe('8080'))

    expect(screen.queryByRole('tab', { name: 'Raw Editor' })).toBeNull()
    expect(screen.queryByPlaceholderText('KEY=value')).toBeNull()
  })

  it('a lock in the Raw view shows the table at once, before the refetch lands', async () => {
    await openRawUnlocked()

    act(() => useEnvUnlockStore.getState().lock())

    expect(screen.queryByPlaceholderText('KEY=value')).toBeNull()
    expect(val(1).value).toBe('8080')
  })
})

describe('EnvEditor: the Raw view with auth disabled (agent-os-wm7t)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    act(() => useEnvUnlockStore.getState().lock())
    useAuthStore.setState({ authDisabled: true })
    mockGetEnv.mockResolvedValue(unlocked)
  })
  afterEach(() => {
    useAuthStore.setState({ authDisabled: false })
  })

  it('is shown and editable while the unlock store is locked', async () => {
    expect(useEnvUnlockStore.getState().unlockedUntil).toBeNull()
    renderWithProviders(<EnvEditor stackId="s1" />)
    await waitFor(() => expect(val(2).value).toBe(PEM))

    fireEvent.mouseDown(screen.getByRole('tab', { name: 'Raw Editor' }))
    fireEvent.click(screen.getByRole('tab', { name: 'Raw Editor' }))
    const textarea = (await screen.findByPlaceholderText('KEY=value')) as HTMLTextAreaElement
    expect(textarea.value).toContain('hunter2')
    fireEvent.change(textarea, { target: { value: 'PORT=1\n' } })
    expect((screen.getByPlaceholderText('KEY=value') as HTMLTextAreaElement).value).toBe('PORT=1\n')
  })
})
