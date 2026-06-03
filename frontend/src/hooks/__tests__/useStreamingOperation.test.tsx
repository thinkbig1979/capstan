import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act, waitFor } from '@testing-library/react'
import { useAuthStore } from '@/stores/authStore'

// ─── Mock WSClient ────────────────────────────────────────────────────────────

type OnMessageFn = (data: string | ArrayBuffer) => void
type WSOptions = {
  onClose?: () => void
  onError?: (e: Event) => void
  onReconnectFailed?: () => void
}

let capturedOnMessage: OnMessageFn | null = null
let capturedOptions: WSOptions | null = null

const mockClose = vi.fn()

vi.mock('@/lib/ws', () => {
  class MockWSClient {
    connect(_path: string, onMessage: OnMessageFn, options: WSOptions = {}) {
      capturedOnMessage = onMessage
      capturedOptions = options
    }
    close = mockClose
  }
  return { WSClient: MockWSClient }
})

// ws-reconcile is a thin helper; test the real implementation side-effects
// by not mocking it — the hook calls reconcileOnClose which calls refetch when
// completed=false, but since refetch is a no-op stub in the hook itself, we
// just verify that status is NOT flipped to 'error' on a normal close.

import { useStreamingOperation } from '../useStreamingOperation'

// ─── Helpers ─────────────────────────────────────────────────────────────────

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
  // Allow auth
  useAuthStore.setState({ authDisabled: true, isAuthenticated: false, token: null })
})

afterEach(() => {
  vi.clearAllMocks()
})

// ─── done frame: outcome:'success' ───────────────────────────────────────────

describe('useStreamingOperation — done frame with outcome:success', () => {
  it('sets status to success', async () => {
    const { result } = renderHook(() => useStreamingOperation())

    act(() => { result.current.execute('my-stack', 'start') })

    act(() => {
      send({ type: 'done', outcome: 'success', reason: 'Stack is running', success: true })
    })

    await waitFor(() => expect(result.current.status).toBe('success'))
    expect(result.current.error).toBeNull()
  })

  it('appends the reason to lines', async () => {
    const { result } = renderHook(() => useStreamingOperation())
    act(() => { result.current.execute('my-stack', 'start') })
    act(() => {
      send({ type: 'done', outcome: 'success', reason: 'All services up' })
    })
    await waitFor(() => expect(result.current.status).toBe('success'))
    expect(result.current.lines).toContain('All services up')
  })
})

// ─── done frame: outcome:'no_change' ─────────────────────────────────────────

describe('useStreamingOperation — done frame with outcome:no_change', () => {
  it('sets status to no_change, NOT success', async () => {
    const { result } = renderHook(() => useStreamingOperation())
    act(() => { result.current.execute('my-stack', 'start') })
    act(() => {
      send({ type: 'done', outcome: 'no_change', reason: 'Stack already running', success: true })
    })
    await waitFor(() => expect(result.current.status).toBe('no_change'))
    expect(result.current.error).toBeNull()
  })

  it('appends the no_change reason to lines', async () => {
    const { result } = renderHook(() => useStreamingOperation())
    act(() => { result.current.execute('my-stack', 'stop') })
    act(() => {
      send({ type: 'done', outcome: 'no_change', reason: 'Already stopped' })
    })
    await waitFor(() => expect(result.current.status).toBe('no_change'))
    expect(result.current.lines).toContain('Already stopped')
  })
})

// ─── done frame: outcome:'partial' ───────────────────────────────────────────

describe('useStreamingOperation — done frame with outcome:partial', () => {
  it('sets status to partial', async () => {
    const { result } = renderHook(() => useStreamingOperation())
    act(() => { result.current.execute('my-stack', 'restart') })
    act(() => {
      send({ type: 'done', outcome: 'partial', reason: '1 of 2 services restarted' })
    })
    await waitFor(() => expect(result.current.status).toBe('partial'))
    expect(result.current.error).toBeNull()
  })
})

// ─── done frame: outcome:'failed' ────────────────────────────────────────────

describe('useStreamingOperation — done frame with outcome:failed', () => {
  it('sets status to error (not "failed")', async () => {
    const { result } = renderHook(() => useStreamingOperation())
    act(() => { result.current.execute('my-stack', 'start') })
    act(() => {
      send({ type: 'done', outcome: 'failed', reason: 'Container exited with code 1', success: false })
    })
    await waitFor(() => expect(result.current.status).toBe('error'))
  })

  it('surfaces the reason as error', async () => {
    const { result } = renderHook(() => useStreamingOperation())
    act(() => { result.current.execute('my-stack', 'start') })
    act(() => {
      send({ type: 'done', outcome: 'failed', reason: 'Out of memory', success: false })
    })
    await waitFor(() => expect(result.current.status).toBe('error'))
    expect(result.current.error).toBe('Out of memory')
  })
})

// ─── Legacy fallback: done with no outcome field ──────────────────────────────

describe('useStreamingOperation — legacy done frame (no outcome field)', () => {
  it('uses success:true to set status=success', async () => {
    const { result } = renderHook(() => useStreamingOperation())
    act(() => { result.current.execute('my-stack', 'start') })
    act(() => {
      // Old backend: no outcome, just success boolean
      send({ type: 'done', success: true })
    })
    await waitFor(() => expect(result.current.status).toBe('success'))
  })

  it('uses success:false to set status=error', async () => {
    const { result } = renderHook(() => useStreamingOperation())
    act(() => { result.current.execute('my-stack', 'start') })
    act(() => {
      send({ type: 'done', success: false, error: 'Command failed' })
    })
    await waitFor(() => expect(result.current.status).toBe('error'))
    expect(result.current.error).toBe('Command failed')
  })
})

// ─── Finding #18: clean close after success done does NOT flip to error ───────

describe('useStreamingOperation — race condition guard (finding #18)', () => {
  it('does NOT flip status to error when socket closes after a success done frame', async () => {
    const { result } = renderHook(() => useStreamingOperation())
    act(() => { result.current.execute('my-stack', 'start') })

    // Backend sends done first, then socket closes (normal flow)
    act(() => {
      send({ type: 'done', outcome: 'success', reason: 'ok', success: true })
    })
    await waitFor(() => expect(result.current.status).toBe('success'))

    // Socket close fires after done — must NOT overwrite the success status
    act(() => { simulateClose() })

    // Status must remain success
    expect(result.current.status).toBe('success')
    expect(result.current.error).toBeNull()
  })

  it('does NOT flip status to error when socket closes after a no_change done frame', async () => {
    const { result } = renderHook(() => useStreamingOperation())
    act(() => { result.current.execute('my-stack', 'start') })

    act(() => {
      send({ type: 'done', outcome: 'no_change', reason: 'Already running' })
    })
    await waitFor(() => expect(result.current.status).toBe('no_change'))

    act(() => { simulateClose() })
    expect(result.current.status).toBe('no_change')
  })

  it('does NOT assert error (but appends message) for unexpected close without done', async () => {
    const { result } = renderHook(() => useStreamingOperation())
    act(() => { result.current.execute('my-stack', 'start') })

    // Still running — close without done frame
    act(() => { simulateClose() })

    // Status must NOT be flipped to 'error' per ws-reconcile semantics
    // (we cannot assert failure on an unexplained disconnect — finding #18)
    expect(result.current.status).not.toBe('error')
    // But a message should appear in lines
    expect(result.current.lines.some(l => l.includes('Connection closed'))).toBe(true)
  })
})

// ─── Connection error ─────────────────────────────────────────────────────────

describe('useStreamingOperation — connection error', () => {
  it('sets status to error on WS error when not completed', async () => {
    const { result } = renderHook(() => useStreamingOperation())
    act(() => { result.current.execute('my-stack', 'start') })
    act(() => { simulateError() })
    await waitFor(() => expect(result.current.status).toBe('error'))
    expect(result.current.error).toBe('Connection failed')
  })
})

// ─── Data / phase frames ──────────────────────────────────────────────────────

describe('useStreamingOperation — data and phase frames', () => {
  it('appends data lines', async () => {
    const { result } = renderHook(() => useStreamingOperation())
    act(() => { result.current.execute('my-stack', 'pull') })
    act(() => {
      send({ type: 'data', line: 'Pulling image layer...' })
    })
    await waitFor(() => expect(result.current.lines).toContain('Pulling image layer...'))
    expect(result.current.status).toBe('running')
  })

  it('wraps phase messages', async () => {
    const { result } = renderHook(() => useStreamingOperation())
    act(() => { result.current.execute('my-stack', 'restart') })
    act(() => {
      send({ type: 'phase', message: 'Stopping containers' })
    })
    await waitFor(() => expect(result.current.lines).toContain('--- Stopping containers ---'))
  })
})

// ─── reset / cancel ───────────────────────────────────────────────────────────

describe('useStreamingOperation — reset', () => {
  it('resets status to idle', () => {
    const { result } = renderHook(() => useStreamingOperation())
    act(() => { result.current.execute('my-stack', 'start') })
    expect(result.current.status).toBe('running')
    act(() => { result.current.reset() })
    expect(result.current.status).toBe('idle')
    expect(result.current.lines).toHaveLength(0)
    expect(result.current.error).toBeNull()
  })
})
