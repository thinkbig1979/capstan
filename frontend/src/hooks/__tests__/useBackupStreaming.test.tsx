/**
 * Tests for useBackupStreaming — finding #17: the hook must NOT assert failure
 * on a bare WS disconnect; it must reconcile to server truth by refetching the
 * backup history instead.
 *
 * Backend terminal frame shape (B5, streamAndFinalize in backup.go):
 *   { type: 'done', success: boolean, error?: string }
 *
 * Action Truth Contract shape (migrated backends):
 *   { type: 'done', outcome: 'success'|'no_change'|'partial'|'failed', reason?: string, error?: string }
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { useAuthStore } from '@/stores/authStore'

// ─── Mock WSClient ─────────────────────────────────────────────────────────────

type OnMessageFn = (data: string | ArrayBuffer) => void
type WSOptions = {
  onClose?: () => void
  onError?: (e: Event) => void
  onReconnectFailed?: () => void
}

let capturedOnMessage: OnMessageFn | null = null
let capturedOptions: WSOptions | null = null

const mockWSClose = vi.fn()

vi.mock('@/lib/ws', () => {
  class MockWSClient {
    connect(_path: string, onMessage: OnMessageFn, options: WSOptions = {}) {
      capturedOnMessage = onMessage
      capturedOptions = options
    }
    close = mockWSClose
  }
  return { WSClient: MockWSClient }
})

// ─── Mock @tanstack/react-query's useQueryClient ──────────────────────────────

const mockInvalidateQueries = vi.fn()

vi.mock('@tanstack/react-query', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-query')>()
  return {
    ...actual,
    useQueryClient: () => ({ invalidateQueries: mockInvalidateQueries }),
  }
})

// ─── Mock toast ───────────────────────────────────────────────────────────────

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), info: vi.fn(), warning: vi.fn() },
}))

import { useBackupStreaming } from '../useBackup'

// ─── Helpers ──────────────────────────────────────────────────────────────────

function createWrapper() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  )
}

function send(msg: object) {
  capturedOnMessage!(JSON.stringify(msg))
}

function simulateClose() {
  capturedOptions?.onClose?.()
}

function simulateError() {
  capturedOptions?.onError?.(new Event('error'))
}

beforeEach(() => {
  vi.clearAllMocks()
  capturedOnMessage = null
  capturedOptions = null
  mockInvalidateQueries.mockResolvedValue(undefined)
  useAuthStore.setState({ authDisabled: true, isAuthenticated: false, token: null })
})

afterEach(() => {
  vi.clearAllMocks()
})

// ─── Finding #17: WS close WITHOUT terminal frame ─────────────────────────────

describe('useBackupStreaming — finding #17: WS close without terminal frame', () => {
  it('does NOT set status to error when WS closes without a done frame', async () => {
    const wrapper = createWrapper()
    const { result } = renderHook(() => useBackupStreaming(), { wrapper })

    act(() => { result.current.connect('/ws/backups/run/abc') })
    expect(result.current.status).toBe('running')

    // Socket closes without a done frame (e.g. server restart, network drop)
    act(() => { simulateClose() })

    // Must NOT flip to 'error' — the backend op may have succeeded on its
    // detached context and persisted a run record.
    expect(result.current.status).not.toBe('error')
  })

  it('refetches backup history when WS closes without a done frame', async () => {
    const wrapper = createWrapper()
    const { result } = renderHook(() => useBackupStreaming(), { wrapper })

    act(() => { result.current.connect('/ws/backups/run/abc') })
    act(() => { simulateClose() })

    // reconcileOnClose must have triggered a history/status invalidation
    expect(mockInvalidateQueries).toHaveBeenCalled()
  })

  it('appends a reconciliation message to lines instead of an error', async () => {
    const wrapper = createWrapper()
    const { result } = renderHook(() => useBackupStreaming(), { wrapper })

    act(() => { result.current.connect('/ws/backups/run/abc') })
    act(() => { send({ type: 'data', line: 'Running backup...' }) })
    act(() => { simulateClose() })

    // Lines should contain the disconnect note, not a bare error
    expect(result.current.lines.some((l) => l.toLowerCase().includes('connection closed'))).toBe(true)
    expect(result.current.error).toBeNull()
  })
})

// ─── done frame: legacy shape (success boolean) ───────────────────────────────

describe('useBackupStreaming — legacy done frame (success boolean)', () => {
  it('sets status to success on success:true', async () => {
    const wrapper = createWrapper()
    const { result } = renderHook(() => useBackupStreaming(), { wrapper })

    act(() => { result.current.connect('/ws/backups/run/abc') })
    act(() => { send({ type: 'done', success: true }) })

    await waitFor(() => expect(result.current.status).toBe('success'))
    expect(result.current.error).toBeNull()
  })

  it('sets status to error on success:false', async () => {
    const wrapper = createWrapper()
    const { result } = renderHook(() => useBackupStreaming(), { wrapper })

    act(() => { result.current.connect('/ws/backups/run/abc') })
    act(() => { send({ type: 'done', success: false, error: 'restic exited 1' }) })

    await waitFor(() => expect(result.current.status).toBe('error'))
    expect(result.current.error).toBe('restic exited 1')
  })
})

// ─── done frame: Action Truth Contract shape (outcome field) ──────────────────

describe('useBackupStreaming — ATC done frame (outcome field)', () => {
  it('sets status to success on outcome:success', async () => {
    const wrapper = createWrapper()
    const { result } = renderHook(() => useBackupStreaming(), { wrapper })

    act(() => { result.current.connect('/ws/backups/run/abc') })
    act(() => { send({ type: 'done', outcome: 'success', reason: 'All stacks backed up' }) })

    await waitFor(() => expect(result.current.status).toBe('success'))
    expect(result.current.error).toBeNull()
    expect(result.current.lines).toContain('All stacks backed up')
  })

  it('sets status to success on outcome:no_change', async () => {
    const wrapper = createWrapper()
    const { result } = renderHook(() => useBackupStreaming(), { wrapper })

    act(() => { result.current.connect('/ws/backups/run/abc') })
    act(() => { send({ type: 'done', outcome: 'no_change', reason: 'No files changed since last backup' }) })

    await waitFor(() => expect(result.current.status).toBe('success'))
    expect(result.current.error).toBeNull()
  })

  it('sets status to partial on outcome:partial', async () => {
    const wrapper = createWrapper()
    const { result } = renderHook(() => useBackupStreaming(), { wrapper })

    act(() => { result.current.connect('/ws/backups/run/abc') })
    act(() => { send({ type: 'done', outcome: 'partial', reason: '2 of 3 stacks backed up' }) })

    await waitFor(() => expect(result.current.status).toBe('partial'))
    expect(result.current.error).toBeNull()
    expect(result.current.lines).toContain('2 of 3 stacks backed up')
  })

  it('sets status to error on outcome:failed', async () => {
    const wrapper = createWrapper()
    const { result } = renderHook(() => useBackupStreaming(), { wrapper })

    act(() => { result.current.connect('/ws/backups/run/abc') })
    act(() => { send({ type: 'done', outcome: 'failed', reason: 'Repository unreachable' }) })

    await waitFor(() => expect(result.current.status).toBe('error'))
    expect(result.current.error).toBe('Repository unreachable')
  })
})

// ─── Race guard: clean close after done frame stays success ───────────────────

describe('useBackupStreaming — race guard (finding #17 / B2 pattern)', () => {
  it('does NOT flip to error when socket closes after a success done frame', async () => {
    const wrapper = createWrapper()
    const { result } = renderHook(() => useBackupStreaming(), { wrapper })

    act(() => { result.current.connect('/ws/backups/run/abc') })

    // Backend sends done, then the socket closes naturally
    act(() => { send({ type: 'done', success: true }) })
    await waitFor(() => expect(result.current.status).toBe('success'))

    // The onClose fires after done (normal WS lifecycle) — must NOT overwrite
    act(() => { simulateClose() })

    expect(result.current.status).toBe('success')
    expect(result.current.error).toBeNull()
  })

  it('does NOT flip to error when socket closes after an ATC success done frame', async () => {
    const wrapper = createWrapper()
    const { result } = renderHook(() => useBackupStreaming(), { wrapper })

    act(() => { result.current.connect('/ws/backups/restore/xyz') })

    act(() => { send({ type: 'done', outcome: 'success', reason: 'Restore complete' }) })
    await waitFor(() => expect(result.current.status).toBe('success'))

    act(() => { simulateClose() })

    expect(result.current.status).toBe('success')
  })

  it('does NOT flip to error when socket closes after a partial done frame', async () => {
    const wrapper = createWrapper()
    const { result } = renderHook(() => useBackupStreaming(), { wrapper })

    act(() => { result.current.connect('/ws/backups/run/abc') })

    act(() => { send({ type: 'done', outcome: 'partial', reason: '1 of 2 stacks backed up' }) })
    await waitFor(() => expect(result.current.status).toBe('partial'))

    act(() => { simulateClose() })

    expect(result.current.status).toBe('partial')
  })
})

// ─── History invalidated on done ──────────────────────────────────────────────

describe('useBackupStreaming — history invalidation on done', () => {
  it('invalidates history queries on a successful done frame', async () => {
    const wrapper = createWrapper()
    const { result } = renderHook(() => useBackupStreaming(), { wrapper })

    act(() => { result.current.connect('/ws/backups/run/abc') })
    act(() => { send({ type: 'done', success: true }) })

    await waitFor(() => expect(result.current.status).toBe('success'))

    expect(mockInvalidateQueries).toHaveBeenCalled()
  })

  it('invalidates history queries on a failed done frame', async () => {
    const wrapper = createWrapper()
    const { result } = renderHook(() => useBackupStreaming(), { wrapper })

    act(() => { result.current.connect('/ws/backups/run/abc') })
    act(() => { send({ type: 'done', success: false, error: 'Disk full' }) })

    await waitFor(() => expect(result.current.status).toBe('error'))

    expect(mockInvalidateQueries).toHaveBeenCalled()
  })
})

// ─── Genuine connection error ─────────────────────────────────────────────────

describe('useBackupStreaming — genuine connection error', () => {
  it('sets status to error on WS error (not just close)', async () => {
    const wrapper = createWrapper()
    const { result } = renderHook(() => useBackupStreaming(), { wrapper })

    act(() => { result.current.connect('/ws/backups/run/abc') })
    act(() => { simulateError() })

    await waitFor(() => expect(result.current.status).toBe('error'))
    expect(result.current.error).toBe('WebSocket connection failed')
  })
})

// ─── onDone callback ──────────────────────────────────────────────────────────

describe('useBackupStreaming — onDone callback', () => {
  it('calls onDone callback after a success done frame', async () => {
    const wrapper = createWrapper()
    const { result } = renderHook(() => useBackupStreaming(), { wrapper })
    const onDone = vi.fn()

    act(() => { result.current.connect('/ws/backups/run/abc', onDone) })
    act(() => { send({ type: 'done', success: true }) })

    await waitFor(() => expect(result.current.status).toBe('success'))
    expect(onDone).toHaveBeenCalledOnce()
  })

  it('calls onDone callback after a failure done frame', async () => {
    const wrapper = createWrapper()
    const { result } = renderHook(() => useBackupStreaming(), { wrapper })
    const onDone = vi.fn()

    act(() => { result.current.connect('/ws/backups/run/abc', onDone) })
    act(() => { send({ type: 'done', success: false }) })

    await waitFor(() => expect(result.current.status).toBe('error'))
    expect(onDone).toHaveBeenCalledOnce()
  })
})

// ─── reset ────────────────────────────────────────────────────────────────────

describe('useBackupStreaming — reset', () => {
  it('resets to idle status', () => {
    const wrapper = createWrapper()
    const { result } = renderHook(() => useBackupStreaming(), { wrapper })

    act(() => { result.current.connect('/ws/backups/run/abc') })
    expect(result.current.status).toBe('running')

    act(() => { result.current.reset() })

    expect(result.current.status).toBe('idle')
    expect(result.current.lines).toHaveLength(0)
    expect(result.current.error).toBeNull()
  })
})

// ─── Refused attach (agent-os-mjrl) ──────────────────────────────────────────
//
// Attach refuses the 25th live viewer of one run BY RESULT: a done frame with
// outcome 'refused' and a reason naming the limit (services/backup_runner.go,
// agent-os-nt0m). The run itself is fine and still streaming to the other 24
// viewers, so the widget must NOT report the run as failed. Two-sided on one
// instrument: the same done-frame path still maps a genuine failure, an
// interrupted run and a success to their own buckets.

const REFUSED_REASON =
  'too many viewers attached to this run (limit 24); close another viewer and reconnect'

describe('useBackupStreaming — refused attach is a refused stream, not a failed run', () => {
  it('maps outcome:refused to status unavailable with the limit reason', async () => {
    const wrapper = createWrapper()
    const { result } = renderHook(() => useBackupStreaming(), { wrapper })
    const onDone = vi.fn()

    act(() => { result.current.connect('/ws/backups/run/abc', onDone) })
    act(() => { send({ type: 'data', line: 'replayed line' }) })
    act(() => { send({ type: 'done', outcome: 'refused', reason: REFUSED_REASON }) })

    await waitFor(() => expect(result.current.status).toBe('unavailable'))
    expect(result.current.error).toBe(REFUSED_REASON)
    expect(onDone).toHaveBeenCalledWith('unavailable')
    // The refusal must not be painted as an error line in the log.
    expect(result.current.lines.some((l) => l.startsWith('Error:'))).toBe(false)
    expect(result.current.lines.some((l) => l.includes(REFUSED_REASON))).toBe(true)
  })

  it('CONTROL: outcome:failed still maps to error', async () => {
    const wrapper = createWrapper()
    const { result } = renderHook(() => useBackupStreaming(), { wrapper })
    const onDone = vi.fn()

    act(() => { result.current.connect('/ws/backups/run/abc', onDone) })
    act(() => { send({ type: 'done', outcome: 'failed', reason: 'Repository unreachable' }) })

    await waitFor(() => expect(result.current.status).toBe('error'))
    expect(result.current.error).toBe('Repository unreachable')
    expect(onDone).toHaveBeenCalledWith('error')
  })

  it('CONTROL: outcome:interrupted still maps to error', async () => {
    const wrapper = createWrapper()
    const { result } = renderHook(() => useBackupStreaming(), { wrapper })
    const onDone = vi.fn()

    act(() => { result.current.connect('/ws/backups/run/abc', onDone) })
    act(() => { send({ type: 'done', outcome: 'interrupted', reason: 'interrupted by server restart' }) })

    await waitFor(() => expect(result.current.status).toBe('error'))
    expect(result.current.error).toBe('interrupted by server restart')
    expect(onDone).toHaveBeenCalledWith('error')
  })

  it('CONTROL: outcome:success still maps to success', async () => {
    const wrapper = createWrapper()
    const { result } = renderHook(() => useBackupStreaming(), { wrapper })
    const onDone = vi.fn()

    act(() => { result.current.connect('/ws/backups/run/abc', onDone) })
    act(() => { send({ type: 'done', outcome: 'success', reason: 'All stacks backed up' }) })

    await waitFor(() => expect(result.current.status).toBe('success'))
    expect(result.current.error).toBeNull()
    expect(onDone).toHaveBeenCalledWith('success')
  })
})
