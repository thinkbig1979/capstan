import { useAuthStore } from '@/stores/authStore'

export type WSState = 'CONNECTING' | 'OPEN' | 'CLOSING' | 'CLOSED' | 'RECONNECTING'

export interface WSClientOptions {
  binary?: boolean
  onOpen?: () => void
  onClose?: () => void
  onError?: (error: Event) => void
  onReconnecting?: (attempt: number) => void
  onReconnectFailed?: () => void
}

export class WSClient {
  private ws: WebSocket | null = null
  private reconnectAttempts = 0
  private maxReconnects = 5
  private reconnectDelay = 2000
  private state: WSState = 'CLOSED'
  private currentPath: string | null = null
  private currentOnMessage: ((data: string | ArrayBuffer) => void) | null = null
  private currentOptions: WSClientOptions | null = null
  private reconnectTimeout: ReturnType<typeof setTimeout> | null = null

  connect(path: string, onMessage: (data: string | ArrayBuffer) => void, options: WSClientOptions = {}) {
    const token = useAuthStore.getState().token
    const authDisabled = useAuthStore.getState().authDisabled

    if (!token && !authDisabled) {
      console.error('Cannot connect: no JWT token available')
      return
    }

    this.currentPath = path
    this.currentOnMessage = onMessage
    this.currentOptions = options
    this.state = 'CONNECTING'

    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    const url = token
      ? `${protocol}//${window.location.host}/api/v1${path}?token=${token}`
      : `${protocol}//${window.location.host}/api/v1${path}`

    this.ws = new WebSocket(url)
    this.ws.binaryType = options.binary ? 'arraybuffer' : 'blob'

    this.ws.onopen = () => {
      this.state = 'OPEN'
      this.reconnectAttempts = 0
      options.onOpen?.()
    }

    this.ws.onmessage = (event) => {
      const data = options.binary ? event.data : event.data as string
      onMessage(data)
    }

    this.ws.onclose = () => {
      this.state = 'CLOSED'
      options.onClose?.()
      this.scheduleReconnect()
    }

    this.ws.onerror = (error) => {
      options.onError?.(error)
    }
  }

  private scheduleReconnect() {
    if (this.reconnectAttempts < this.maxReconnects && this.currentPath && this.currentOnMessage) {
      this.state = 'RECONNECTING'
      this.reconnectAttempts++
      const delay = this.reconnectDelay * Math.pow(2, this.reconnectAttempts - 1)
      this.currentOptions?.onReconnecting?.(this.reconnectAttempts)

      this.reconnectTimeout = setTimeout(() => {
        if (this.currentPath && this.currentOnMessage) {
          this.connect(this.currentPath, this.currentOnMessage, this.currentOptions || {})
        }
      }, delay)
    } else {
      this.currentOptions?.onReconnectFailed?.()
    }
  }

  send(data: string | ArrayBuffer) {
    this.ws?.send(data)
  }

  close() {
    if (this.reconnectTimeout) {
      clearTimeout(this.reconnectTimeout)
      this.reconnectTimeout = null
    }
    this.reconnectAttempts = this.maxReconnects
    this.ws?.close()
    this.ws = null
    this.state = 'CLOSED'
  }

  getState(): WSState {
    return this.state
  }

  getReconnectAttempts(): number {
    return this.reconnectAttempts
  }

  getMaxReconnects(): number {
    return this.maxReconnects
  }

  resetReconnectCounter() {
    this.reconnectAttempts = 0
  }
}
