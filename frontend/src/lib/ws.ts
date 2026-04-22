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
  private currentPath: string | null = null
  private currentOnMessage: ((data: string | ArrayBuffer) => void) | null = null
  private currentOptions: WSClientOptions | null = null
  private reconnectTimeout: ReturnType<typeof setTimeout> | null = null

  connect(path: string, onMessage: (data: string | ArrayBuffer) => void, options: WSClientOptions = {}) {
    const token = useAuthStore.getState().token
    const authDisabled = useAuthStore.getState().authDisabled

    if (!token && !authDisabled) {
      console.error('Cannot connect: not authenticated')
      return
    }

    this.currentPath = path
    this.currentOnMessage = onMessage
    this.currentOptions = options

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

    this.ws.onclose = () => {
      this.ws = null
      options.onClose?.()
      if (this.currentPath && this.currentOnMessage) {
        this.scheduleReconnect()
      }
    }

    this.ws.onerror = (error) => {
      this.ws = null
      options.onError?.(error)
    }
  }

  private scheduleReconnect() {
    if (this.reconnectAttempts < this.maxReconnects && this.currentPath && this.currentOnMessage) {
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
    if (this.ws && this.ws.readyState === WebSocket.OPEN) {
      this.ws.send(data)
    }
  }

  close() {
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
