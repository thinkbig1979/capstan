import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { useAuthStore } from '@/stores/authStore'
import { queryKeys } from '@/lib/query-keys'

// ─── Mock WSClient ────────────────────────────────────────────────────────────

type OnMessageFn = (data: string | ArrayBuffer) => void
type WSOptions = {
  reconnect?: boolean
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

// The hook refetches the stack's queries after a drop (agent-os-s3fo), so it
// needs a QueryClient.
let queryClient: QueryClient

function wrapper({ children }: { children: ReactNode }) {
  return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
}

const renderOperation = () => renderHook(() => useStreamingOperation(), { wrapper })

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
  queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  // Allow auth
  useAuthStore.setState({ authDisabled: true, isAuthenticated: false, token: null })
})

afterEach(() => {
  vi.clearAllMocks()
})

// ─── done frame: outcome:'success' ───────────────────────────────────────────

describe('useStreamingOperation — done frame with outcome:success', () => {
  it('sets status to success', async () => {
    const { result } = renderOperation()

    act(() => { result.current.execute('my-stack', 'start') })

    act(() => {
      send({ type: 'done', outcome: 'success', reason: 'Stack is running', success: true })
    })

    await waitFor(() => expect(result.current.status).toBe('success'))
    expect(result.current.error).toBeNull()
  })

  it('appends the reason to lines', async () => {
    const { result } = renderOperation()
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
    const { result } = renderOperation()
    act(() => { result.current.execute('my-stack', 'start') })
    act(() => {
      send({ type: 'done', outcome: 'no_change', reason: 'Stack already running', success: true })
    })
    await waitFor(() => expect(result.current.status).toBe('no_change'))
    expect(result.current.error).toBeNull()
  })

  it('appends the no_change reason to lines', async () => {
    const { result } = renderOperation()
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
    const { result } = renderOperation()
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
    const { result } = renderOperation()
    act(() => { result.current.execute('my-stack', 'start') })
    act(() => {
      send({ type: 'done', outcome: 'failed', reason: 'Container exited with code 1', success: false })
    })
    await waitFor(() => expect(result.current.status).toBe('error'))
  })

  it('surfaces the reason as error', async () => {
    const { result } = renderOperation()
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
    const { result } = renderOperation()
    act(() => { result.current.execute('my-stack', 'start') })
    act(() => {
      // Old backend: no outcome, just success boolean
      send({ type: 'done', success: true })
    })
    await waitFor(() => expect(result.current.status).toBe('success'))
  })

  it('uses success:false to set status=error', async () => {
    const { result } = renderOperation()
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
    const { result } = renderOperation()
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
    const { result } = renderOperation()
    act(() => { result.current.execute('my-stack', 'start') })

    act(() => {
      send({ type: 'done', outcome: 'no_change', reason: 'Already running' })
    })
    await waitFor(() => expect(result.current.status).toBe('no_change'))

    act(() => { simulateClose() })
    expect(result.current.status).toBe('no_change')
  })

  // The close-without-done case that lived here ('Connection closed') is now
  // 'a close without a done frame shows ...' under "connection lost" below.
})

// ─── Connection lost (agent-os-s3fo) ──────────────────────────────────────────
// The socket's URL is the command, so it must never re-dial. A close with no
// done or error frame is a lost connection: an unknown outcome, never an
// assumed failure (finding #18), with the stack's status refetched.

const CONNECTION_LOST = 'Connection lost. The operation may still be running; check the stack status.'

describe('useStreamingOperation — connection lost', () => {
  it('passes reconnect: false to WSClient', () => {
    const { result } = renderOperation()
    act(() => { result.current.execute('my-stack', 'start') })
    expect(capturedOptions?.reconnect).toBe(false)
  })

  it(`a close without a done frame shows '${CONNECTION_LOST}' and invalidates the stack queries`, () => {
    const invalidate = vi.spyOn(queryClient, 'invalidateQueries')
    const { result } = renderOperation()
    act(() => { result.current.execute('my-stack', 'start') })

    act(() => { simulateClose() })

    expect(result.current.status).toBe('disconnected')
    expect(result.current.lines.at(-1)).toBe(CONNECTION_LOST)
    expect(result.current.error).toBeNull()
    expect(invalidate.mock.calls.map(([f]) => f?.queryKey)).toEqual([
      queryKeys.stack.detail('my-stack'),
      queryKeys.stacks(),
    ])
  })

  it('a drop leaves running, so the page re-enables its operation buttons', () => {
    const { result } = renderOperation()
    act(() => { result.current.execute('my-stack', 'restart') })
    expect(result.current.status).toBe('running')

    act(() => { simulateClose() })

    expect(result.current.status).not.toBe('running')
  })

  // Rewritten from 'sets status to error on WS error when not completed': an
  // error EVENT is the browser's, not the server's, and a close always
  // follows it. It must not read as a failed command.
  it('an error EVENT then close shows the connection lost status, not error', () => {
    const { result } = renderOperation()
    act(() => { result.current.execute('my-stack', 'start') })

    act(() => { simulateError() })
    act(() => { simulateClose() })

    expect(result.current.status).toBe('disconnected')
    expect(result.current.error).toBeNull()
    expect(result.current.lines.at(-1)).toBe(CONNECTION_LOST)
  })

  it('an error FRAME (the command really failed) then close stays error', () => {
    const invalidate = vi.spyOn(queryClient, 'invalidateQueries')
    const { result } = renderOperation()
    act(() => { result.current.execute('my-stack', 'start') })

    act(() => { send({ type: 'error', error: 'compose exploded' }) })
    act(() => { simulateClose() })

    expect(result.current.status).toBe('error')
    expect(result.current.error).toBe('compose exploded')
    expect(result.current.lines).not.toContain(CONNECTION_LOST)
    // The status is still refetched: the run ended without a verified result.
    expect(invalidate).toHaveBeenCalled()
  })

  it('a close after a done frame invalidates nothing here and keeps the outcome', () => {
    const invalidate = vi.spyOn(queryClient, 'invalidateQueries')
    const { result } = renderOperation()
    act(() => { result.current.execute('my-stack', 'stop') })

    act(() => { send({ type: 'done', outcome: 'success', reason: 'stack stopped', success: true }) })
    act(() => { simulateClose() })

    expect(result.current.status).toBe('success')
    expect(result.current.lines).not.toContain(CONNECTION_LOST)
    expect(invalidate).not.toHaveBeenCalled()
  })

  it('Dismiss after a drop closes the client', () => {
    const { result } = renderOperation()
    act(() => { result.current.execute('my-stack', 'start') })
    act(() => { simulateClose() })
    mockClose.mockClear()

    act(() => { result.current.reset() })

    expect(mockClose).toHaveBeenCalledTimes(1)
  })

  it('unmount after a drop closes the client', () => {
    const { result, unmount } = renderOperation()
    act(() => { result.current.execute('my-stack', 'start') })
    act(() => { simulateClose() })
    mockClose.mockClear()

    unmount()

    expect(mockClose).toHaveBeenCalledTimes(1)
  })
})

// ─── Data / phase frames ──────────────────────────────────────────────────────

describe('useStreamingOperation — data and phase frames', () => {
  it('appends data lines', async () => {
    const { result } = renderOperation()
    act(() => { result.current.execute('my-stack', 'pull') })
    act(() => {
      send({ type: 'data', line: 'Pulling image layer...' })
    })
    await waitFor(() => expect(result.current.lines).toContain('Pulling image layer...'))
    expect(result.current.status).toBe('running')
  })

  it('wraps phase messages', async () => {
    const { result } = renderOperation()
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
    const { result } = renderOperation()
    act(() => { result.current.execute('my-stack', 'start') })
    expect(result.current.status).toBe('running')
    act(() => { result.current.reset() })
    expect(result.current.status).toBe('idle')
    expect(result.current.lines).toHaveLength(0)
    expect(result.current.error).toBeNull()
  })
})

// ─── agent-os-06c1: the parsed frame is asserted, not validated ──────────────

describe('useStreamingOperation — the parsed frame is asserted, not validated', () => {
  // The exact twin of useBackup.ts's handler. Fixing one and leaving the other
  // is this repo's most-repeated defect, so both move together.
  it('does not append a non-string data line to lines', async () => {
    const { result } = renderOperation()
    act(() => { result.current.execute('my-stack', 'start') })
    act(() => { send({ type: 'data', line: { text: 'starting' } }) })

    expect(result.current.lines.every((l) => typeof l === 'string')).toBe(true)
    expect(result.current.lines.join(' ')).not.toContain('[object Object]')
  })

  it('does not render a non-string phase message into lines', async () => {
    const { result } = renderOperation()
    act(() => { result.current.execute('my-stack', 'start') })
    act(() => { send({ type: 'phase', message: { phase: 'pulling' } }) })

    expect(result.current.lines.join(' ')).not.toContain('[object Object]')
  })

  it('does not let a non-string error escape into the error string', async () => {
    const { result } = renderOperation()
    act(() => { result.current.execute('my-stack', 'start') })
    act(() => { send({ type: 'error', error: { cause: 'no such image' } }) })

    await waitFor(() => expect(result.current.status).toBe('error'))
    expect(typeof result.current.error).toBe('string')
    expect(result.current.error).not.toContain('[object Object]')
  })

  it('still carries real string frames through unchanged', async () => {
    const { result } = renderOperation()
    act(() => { result.current.execute('my-stack', 'start') })
    act(() => { send({ type: 'data', line: 'starting' }) })
    act(() => { send({ type: 'phase', message: 'pulling' }) })

    expect(result.current.lines).toContain('starting')
    expect(result.current.lines).toContain('--- pulling ---')
  })
})

// agent-os-ljm0 review R2: an error FRAME (a scanner error, docker_lifecycle.go)
// arrives mid-run and the run goes on to a done frame. `finished` is what tells
// a consumer the outcome is final: only a done frame or the socket ending.
describe('useStreamingOperation — finished marks the terminal outcome (ljm0 R2)', () => {
  it('ljm0R2: an error frame mid-run is not finished; the later done frame is', () => {
    const { result } = renderOperation()
    act(() => { result.current.execute('my-stack', 'start') })
    expect(result.current.finished).toBe(false)

    act(() => { send({ type: 'error', error: 'line too long' }) })
    expect(result.current.status).toBe('error')
    expect(result.current.finished).toBe(false)

    act(() => { send({ type: 'done', outcome: 'success' }) })
    expect(result.current.status).toBe('success')
    expect(result.current.finished).toBe(true)
  })

  it('ljm0R2: an error frame then the socket ending is finished, still error', () => {
    const { result } = renderOperation()
    act(() => { result.current.execute('my-stack', 'start') })
    act(() => { send({ type: 'error', error: 'compose exploded' }) })
    act(() => { simulateClose() })

    expect(result.current.status).toBe('error')
    expect(result.current.finished).toBe(true)
  })

  it('ljm0R2: a new execute and reset clear finished', () => {
    const { result } = renderOperation()
    act(() => { result.current.execute('my-stack', 'start') })
    act(() => { send({ type: 'done', outcome: 'success' }) })
    expect(result.current.finished).toBe(true)

    act(() => { result.current.execute('my-stack', 'stop') })
    expect(result.current.finished).toBe(false)
    act(() => { send({ type: 'done', outcome: 'success' }) })
    act(() => { result.current.reset() })
    expect(result.current.finished).toBe(false)
  })
})
