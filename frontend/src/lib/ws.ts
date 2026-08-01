import { useAuthStore } from '@/stores/authStore'

export type WSState = 'CONNECTING' | 'OPEN' | 'CLOSING' | 'CLOSED' | 'RECONNECTING'

/** Close codes the server uses for policy refusals. Mirrors
 *  CloseCodeAuthFailure / CloseCodeRateLimit in backend/internal/handlers/ws.go. */
export const WS_CLOSE_AUTH_FAILURE = 4401
export const WS_CLOSE_RATE_LIMIT = 4429

/** A policy refusal is a decision, not a blip. Retrying cannot change the
 *  outcome, and for the connection cap the retry loop *is* the problem the cap
 *  exists to contain — the runaway client would sit there refusing and
 *  reconnecting forever (agent-os-a0y). */
// A missing code means we cannot tell, so reconnect — the reconnect ladder is
// the safe default and only an explicit policy code suppresses it.
function shouldReconnectAfter(code: number | undefined): boolean {
  return code !== WS_CLOSE_AUTH_FAILURE && code !== WS_CLOSE_RATE_LIMIT
}

export interface WSClientOptions {
  binary?: boolean
  onOpen?: () => void
  /** Receives the close event so callers can distinguish a policy refusal
   *  (4401/4429) from an ordinary disconnect. */
  onClose?: (event: CloseEvent) => void
  onError?: (error: Event) => void
  onReconnecting?: (attempt: number) => void
  onReconnectFailed?: () => void
}

// Cap on the exponential backoff base so a long-dead connection doesn't end up
// waiting minutes between attempts.
export const MAX_RECONNECT_DELAY_MS = 30000

export class WSClient {
  private ws: WebSocket | null = null
  private reconnectAttempts = 0
  private maxReconnects = 5
  private reconnectDelay = 2000
  private currentPath: string | null = null
  private currentOnMessage: ((data: string | ArrayBuffer) => void) | null = null
  private currentOptions: WSClientOptions | null = null
  private reconnectTimeout: ReturnType<typeof setTimeout> | null = null
  private recoveryListenersAttached = false
  private readonly handleRecoverySignal = () => this.attemptRecovery()

  connect(path: string, onMessage: (data: string | ArrayBuffer) => void, options: WSClientOptions = {}): boolean {
    const token = useAuthStore.getState().token
    const authDisabled = useAuthStore.getState().authDisabled

    if (!token && !authDisabled) {
      console.error('Cannot connect: not authenticated')
      return false
    }

    this.currentPath = path
    this.currentOnMessage = onMessage
    this.currentOptions = options
    this.attachRecoveryListeners()

    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    const url = `${protocol}//${window.location.host}/api/v1${path}`

    this.ws = new WebSocket(url)
    this.ws.binaryType = options.binary ? 'arraybuffer' : 'blob'

    this.ws.onopen = () => {
      this.reconnectAttempts = 0
      if (token && token !== 'cookie') {
        this.ws!.send(JSON.stringify({ type: 'auth', token }))
      }
      options.onOpen?.()
    }

    this.ws.onmessage = (event) => {
      const data = options.binary ? event.data : event.data as string
      onMessage(data)
    }

    this.ws.onclose = (event) => {
      this.ws = null
      options.onClose?.(event)
      if (!shouldReconnectAfter(event?.code)) {
        // Stop retrying and stop tracking the path, so a later recovery signal
        // (focus/online) does not resurrect the loop either.
        this.currentPath = null
        this.currentOnMessage = null
        return
      }
      if (this.currentPath && this.currentOnMessage) {
        this.scheduleReconnect()
      }
    }

    this.ws.onerror = (error) => {
      this.ws = null
      options.onError?.(error)
    }

    return true
  }

  private scheduleReconnect() {
    if (this.reconnectAttempts < this.maxReconnects && this.currentPath && this.currentOnMessage) {
      this.reconnectAttempts++
      // Full jitter (AWS "Exponential Backoff and Jitter"): sampling the whole
      // [0, cap] range spreads reconnecting clients out further than equal
      // jitter, which matters when many tabs/clients reconnect after a shared
      // outage. The cap keeps a long-dead connection from waiting minutes
      // between attempts.
      const cappedBase = Math.min(
        this.reconnectDelay * Math.pow(2, this.reconnectAttempts - 1),
        MAX_RECONNECT_DELAY_MS,
      )
      const delay = Math.random() * cappedBase
      this.currentOptions?.onReconnecting?.(this.reconnectAttempts)

      this.reconnectTimeout = setTimeout(() => {
        this.reconnectTimeout = null
        if (this.currentPath && this.currentOnMessage) {
          this.connect(this.currentPath, this.currentOnMessage, this.currentOptions || {})
        }
      }, delay)
    } else {
      this.currentOptions?.onReconnectFailed?.()
    }
  }

  /**
   * Browser WebSockets expose no ping/pong API, and the backend's ping/pong
   * (see backend/internal/handlers/ws.go: protocol-level PING every 30s,
   * PONG resets a 60s read deadline) is answered by the browser automatically
   * and never surfaces to JS — there's no app-level heartbeat we can add
   * without a backend contract change, and no safe generic message type to
   * invent (the terminal channel forwards arbitrary bytes straight to a PTY).
   *
   * What we can do: once the 5-attempt cap is hit, `this.ws` and
   * `this.reconnectTimeout` are both null and nothing will ever retry again,
   * even though the caller still wants a connection (currentPath/currentOnMessage
   * are still set). The same "stuck" state can also happen if a backgrounded
   * tab silently loses its socket without onclose ever firing (throttled
   * timers, sleep/wake, network changes). `online`/`visibilitychange` are
   * cheap, safe signals to re-check that state and resume.
   */
  private attachRecoveryListeners() {
    if (this.recoveryListenersAttached) return
    this.recoveryListenersAttached = true
    window.addEventListener('online', this.handleRecoverySignal)
    document.addEventListener('visibilitychange', this.handleRecoverySignal)
  }

  private detachRecoveryListeners() {
    if (!this.recoveryListenersAttached) return
    this.recoveryListenersAttached = false
    window.removeEventListener('online', this.handleRecoverySignal)
    document.removeEventListener('visibilitychange', this.handleRecoverySignal)
  }

  private attemptRecovery() {
    if (document.visibilityState === 'hidden') return
    if (!this.currentPath || !this.currentOnMessage) return
    // A connection is already open/connecting, or a backoff attempt is
    // already scheduled — nothing to resume.
    if (this.ws || this.reconnectTimeout) return

    this.reconnectAttempts = 0
    this.connect(this.currentPath, this.currentOnMessage, this.currentOptions || {})
  }

  send(data: string | ArrayBuffer) {
    if (this.ws && this.ws.readyState === WebSocket.OPEN) {
      this.ws.send(data)
    }
  }

  close() {
    this.detachRecoveryListeners()
    if (this.reconnectTimeout) {
      clearTimeout(this.reconnectTimeout)
      this.reconnectTimeout = null
    }
    this.reconnectAttempts = this.maxReconnects
    this.currentPath = null
    this.currentOnMessage = null
    this.currentOptions = null
    if (this.ws) {
      const ws = this.ws
      this.ws = null
      if (ws.readyState === WebSocket.CONNECTING || ws.readyState === WebSocket.OPEN) {
        ws.close()
      }
    }
  }

}
