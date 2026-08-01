import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { WSClient, WS_CLOSE_AUTH_FAILURE, WS_CLOSE_RATE_LIMIT } from '@/lib/ws'
import { useAuthStore } from '@/stores/authStore'

// A refused connection used to trigger the ordinary reconnect path, so a client
// that hit the per-user cap would sit there refusing and reconnecting forever —
// the retry loop *is* the problem the cap exists to contain (agent-os-a0y).

class FakeSocket {
  static instances: FakeSocket[] = []
  onopen: (() => void) | null = null
  onclose: ((event: CloseEvent) => void) | null = null
  onmessage: ((event: MessageEvent) => void) | null = null
  onerror: ((event: Event) => void) | null = null
  binaryType = 'blob'
  sent: unknown[] = []

  url: string

  // Explicit field, not a constructor parameter property: the tsconfig sets
  // erasableSyntaxOnly, which rejects the shorthand.
  constructor(url: string) {
    this.url = url
    FakeSocket.instances.push(this)
  }

  send(data: unknown) {
    this.sent.push(data)
  }

  close() {}

  fireClose(code: number) {
    this.onclose?.(new CloseEvent('close', { code }))
  }
}

describe('WSClient close-code policy', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    FakeSocket.instances = []
    vi.stubGlobal('WebSocket', FakeSocket as unknown as typeof WebSocket)
    useAuthStore.setState({ token: 'test-token', authDisabled: false })
  })

  afterEach(() => {
    vi.useRealTimers()
    vi.unstubAllGlobals()
  })

  function connect(onClose?: (e: CloseEvent) => void) {
    const client = new WSClient()
    client.connect('/ws/terminal/stack-a/c1', () => {}, { onClose })
    return client
  }

  it('does not reconnect after a rate-limit close', () => {
    connect()
    expect(FakeSocket.instances).toHaveLength(1)

    FakeSocket.instances[0].fireClose(WS_CLOSE_RATE_LIMIT)
    vi.advanceTimersByTime(120000)

    expect(FakeSocket.instances).toHaveLength(1)
  })

  it('does not reconnect after an auth-failure close', () => {
    connect()
    FakeSocket.instances[0].fireClose(WS_CLOSE_AUTH_FAILURE)
    vi.advanceTimersByTime(120000)

    expect(FakeSocket.instances).toHaveLength(1)
  })

  it('still reconnects after an ordinary abnormal close', () => {
    connect()
    FakeSocket.instances[0].fireClose(1006)
    vi.advanceTimersByTime(120000)

    expect(FakeSocket.instances.length).toBeGreaterThan(1)
  })

  it('passes the close event through so callers can read the code', () => {
    const onClose = vi.fn()
    connect(onClose)

    FakeSocket.instances[0].fireClose(WS_CLOSE_RATE_LIMIT)

    expect(onClose).toHaveBeenCalledTimes(1)
    expect(onClose.mock.calls[0][0].code).toBe(WS_CLOSE_RATE_LIMIT)
  })
})
