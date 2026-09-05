import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { WSClient, WS_CLOSE_AUTH_FAILURE, WS_CLOSE_RATE_LIMIT, WS_CLOSE_NOT_FOUND } from '@/lib/ws'
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

  // agent-os-vi0o: a permanent failure (e.g. a deleted stack) must not redial
  // either — retrying cannot change that the resource is gone.
  it('does not reconnect after a not-found close', () => {
    connect()
    FakeSocket.instances[0].fireClose(WS_CLOSE_NOT_FOUND)
    vi.advanceTimersByTime(120000)

    expect(FakeSocket.instances).toHaveLength(1)
  })

  it('still reconnects after an ordinary abnormal close', () => {
    connect()
    FakeSocket.instances[0].fireClose(1006)
    vi.advanceTimersByTime(120000)

    expect(FakeSocket.instances.length).toBeGreaterThan(1)
  })

  // 1000 specifically, not 1006. The case above uses an abnormal close, but the
  // code four metered handlers actually sent on a cap refusal was
  // CloseNormalClosure (agent-os-jj8u), and 1000 is the one that has to keep
  // reconnecting — it is a legitimate ordinary disconnect from any handler that
  // is not refusing. Pinning it here means a future edit to shouldReconnectAfter's
  // code list cannot quietly turn every clean server close into a dead socket.
  it('still reconnects after a normal close (1000)', () => {
    connect()
    FakeSocket.instances[0].fireClose(1000)
    vi.advanceTimersByTime(120000)

    expect(FakeSocket.instances.length).toBeGreaterThan(1)
  })

  // THE RECONNECT LADDER IS NOT BOUNDED ON A REFUSAL, which is why the backend
  // close code carries the whole weight of stopping the loop.
  //
  // maxReconnects is 5, so a 1000 close looks like it costs at most 5 retries.
  // It does not, because serveWS UPGRADES BEFORE IT REFUSES: upgradeConnection
  // completes the handshake, and only then does cm.Add fail and a close frame
  // get written (backend/internal/handlers/ws.go). The 101 is already on the
  // wire, so onopen fires, ws.ts:68 zeroes reconnectAttempts, and the close
  // lands against a counter that was just reset. The ladder re-enters at
  // attempt 1 forever, at Math.random() * 2000 ms — mean 1s, matching the
  // ~1/second refusal storm observed against a live server.
  //
  // The two arms differ ONLY in whether onopen fires. Same close code, same
  // client. Without that control the first arm would just be "reconnects a lot"
  // and would not identify the reset as the mechanism.
  function refuseTimes(n: number, code: number, fireOpen: boolean) {
    for (let i = 0; i < n; i++) {
      const socket = FakeSocket.instances[FakeSocket.instances.length - 1]
      if (fireOpen) socket.onopen?.()
      socket.fireClose(code)
      vi.advanceTimersByTime(60000)
    }
  }

  it('upgrade-then-refuse with 1000 redials past maxReconnects, unbounded', () => {
    connect()
    refuseTimes(12, 1000, true)

    // 13 = the initial socket plus one redial per refusal. maxReconnects is 5,
    // so anything above 6 proves the cap is never reached.
    expect(FakeSocket.instances.length).toBe(13)
  })

  it('CONTROL: the same 1000 close WITHOUT onopen stops at the 5-attempt cap', () => {
    connect()
    refuseTimes(12, 1000, false)

    expect(FakeSocket.instances.length).toBe(6)
  })

  it('upgrade-then-refuse with 4429 does not redial at all', () => {
    connect()
    FakeSocket.instances[0].onopen?.()
    FakeSocket.instances[0].fireClose(WS_CLOSE_RATE_LIMIT)
    vi.advanceTimersByTime(120000)

    expect(FakeSocket.instances).toHaveLength(1)
  })

  // Same upgrade-before-close mechanism as the 4429 case above, for the
  // permanent-failure code (agent-os-vi0o): terminal.go's GetStack close runs
  // after serveWS upgrades, so onopen has fired before this close arrives too.
  it('upgrade-then-refuse with 4404 does not redial at all', () => {
    connect()
    FakeSocket.instances[0].onopen?.()
    FakeSocket.instances[0].fireClose(WS_CLOSE_NOT_FOUND)
    vi.advanceTimersByTime(120000)

    expect(FakeSocket.instances).toHaveLength(1)
  })

  it('passes the close event through so callers can read the code', () => {
    const onClose = vi.fn()
    connect(onClose)

    FakeSocket.instances[0].fireClose(WS_CLOSE_RATE_LIMIT)

    expect(onClose).toHaveBeenCalledTimes(1)
    expect(onClose.mock.calls[0][0].code).toBe(WS_CLOSE_RATE_LIMIT)
  })
})
