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
  closed = false

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
    this.closed = true
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

  // serveWS UPGRADES BEFORE ANY HANDLER LOGIC RUNS: upgradeConnection completes
  // the handshake (backend/internal/handlers/ws.go, serveWS), and only then do
  // cm.Add or the handler's own first request run. So by the time a handler-level
  // close arrives — terminal.go's "Failed to create terminal session" (1000),
  // logs.go's 1011s, a transient DB fault in GetStack — the 101 is already on
  // the wire and onopen has fired. ws.ts used to zero reconnectAttempts in
  // onopen unconditionally, so every such close landed against a fresh counter
  // and the ladder re-entered at attempt 1 forever, at Math.random() * 2000 ms —
  // mean 1s, matching the ~1/second storm observed against a live server
  // (agent-os-jj8u measured it; agent-os-hpe9 bounds it).
  //
  // The counter now resets only once the socket has stayed open past
  // RECONNECT_RESET_AFTER_MS (5 s in ws.ts). Arms below use literal advances of
  // 10 000 ms (past it) and 4 999 ms (just under it) rather than importing the
  // constant, so the failing-first run of this file compiled against the
  // unchanged ws.ts; if N ever moves past 10 s these arms say so loudly.
  //
  // Each arm is a socket COUNT, never a duration. The arms differ ONLY in
  // whether onopen fires and how long the socket stays open. Same close code,
  // same client. Without the controls the bounded arm would just be "reconnects
  // a bounded number of times" and would not identify the reset as the mechanism.
  function refuseTimes(n: number, code: number, fireOpen: boolean, openForMs = 0) {
    for (let i = 0; i < n; i++) {
      const socket = FakeSocket.instances[FakeSocket.instances.length - 1]
      // Once the cap holds there is no new socket to refuse; a browser never
      // re-fires onopen on a closed one, so neither does this harness.
      if (socket.closed) return
      if (fireOpen) socket.onopen?.()
      if (openForMs > 0) vi.advanceTimersByTime(openForMs)
      socket.fireClose(code)
      vi.advanceTimersByTime(60000)
    }
  }

  // 1 + maxReconnects: the initial socket, then one redial per attempt until
  // the 5-attempt cap. 12 cycles are offered, only 5 redials may be taken.
  const BOUNDED = 1 + 5

  it('upgrade-then-close with 1000 is bounded at 1 + maxReconnects', () => {
    connect()
    refuseTimes(12, 1000, true)

    expect(
      FakeSocket.instances.length,
      `sockets: 1 initial + 5 redials (maxReconnects) = ${BOUNDED}; 13 means the ladder re-entered at attempt 1 after every onopen`,
    ).toBe(BOUNDED)
  })

  it('upgrade-then-close with 1011 is bounded at 1 + maxReconnects', () => {
    connect()
    refuseTimes(12, 1011, true)

    expect(
      FakeSocket.instances.length,
      `sockets: 1 initial + 5 redials (maxReconnects) = ${BOUNDED}; 13 means the ladder re-entered at attempt 1 after every onopen`,
    ).toBe(BOUNDED)
  })

  // Duration-gated, not open-gated: a socket that was open but dropped BEFORE
  // the reset window still counts the close as a failed attempt.
  it('a close 4999 ms after open (under the reset window) still counts against the cap', () => {
    connect()
    refuseTimes(12, 1000, true, 4999)

    expect(
      FakeSocket.instances.length,
      `sockets: 1 initial + 5 redials (maxReconnects) = ${BOUNDED}; more means a close inside the window reset the counter`,
    ).toBe(BOUNDED)
  })

  it('CONTROL: the same 1000 close WITHOUT onopen stops at the 5-attempt cap', () => {
    connect()
    refuseTimes(12, 1000, false)

    expect(FakeSocket.instances.length).toBe(BOUNDED)
  })

  // CONTROL on the same instrument: a socket that stays open past the window and
  // then drops gets a FRESH ladder every time. 7 cycles = maxReconnects + 2, so a
  // sticky one-shot reset that only fires on the first recovery (or no reset at
  // all: 6 sockets, attempts 1..5, then give-up) fails here too. Recording
  // onReconnecting pins the ladder depth the user experiences as delay: every
  // drop after a recovered socket is attempt 1, i.e. a <= 2 s redial.
  it('CONTROL: a socket open past the reset window gets a fresh ladder on every later drop', () => {
    const attempts: number[] = []
    const client = new WSClient()
    client.connect('/ws/events', () => {}, { onReconnecting: (attempt) => attempts.push(attempt) })

    const cycles = 5 + 2
    refuseTimes(cycles, 1006, true, 10000)

    expect(
      FakeSocket.instances.length,
      `sockets: 1 initial + 1 redial per recovered-then-dropped cycle x ${cycles} = ${1 + cycles}`,
    ).toBe(1 + cycles)
    expect(attempts).toEqual(Array.from({ length: cycles }, () => 1))
    client.close()
  })

  // useWebSocket's manual reconnect() is close() then connect(). close() parks
  // the counter at maxReconnects so nothing redials behind the caller's back;
  // the onopen reset used to be what un-parked it. Without connect() starting a
  // fresh ladder, the first drop after a manual reconnect gives up on the spot
  // (seen as useWebSocket.test.tsx "reconnect keeps the close/reconnecting
  // wiring too": expected RECONNECTING, got CLOSED).
  it('a caller-initiated connect() after close() starts a fresh ladder', () => {
    const attempts: number[] = []
    const client = new WSClient()
    const options = { onReconnecting: (attempt: number) => attempts.push(attempt) }
    client.connect('/ws/events', () => {}, options)
    FakeSocket.instances[0].onopen?.()
    client.close()

    client.connect('/ws/events', () => {}, options)
    expect(FakeSocket.instances).toHaveLength(2)
    FakeSocket.instances[1].onopen?.()
    FakeSocket.instances[1].fireClose(1006)
    vi.advanceTimersByTime(60000)

    expect(FakeSocket.instances.length, 'sockets: 2 caller connects + 1 redial = 3').toBe(3)
    expect(attempts).toEqual([1])
    client.close()
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
