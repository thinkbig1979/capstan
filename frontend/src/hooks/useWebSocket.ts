import { useEffect, useRef, useState, useCallback } from 'react'
import { useAuthStore } from '@/stores/authStore'
import { WSClient, type WSClientOptions, type WSState } from '@/lib/ws'

export interface UseWebSocketOptions extends WSClientOptions {
  skip?: boolean
}

export interface UseWebSocketReturn {
  status: 'connecting' | 'connected' | 'disconnected' | 'reconnecting'
  send: (data: string | ArrayBuffer) => void
  lastMessage: string | ArrayBuffer | null
  reconnect: () => void
  disconnect: () => void
  wsState: WSState
  reconnectAttempts: number
}

export function useWebSocket(
  path: string,
  onMessage: (data: string | ArrayBuffer) => void,
  options: UseWebSocketOptions = {}
): UseWebSocketReturn {
  const isAuthenticated = useAuthStore((s) => s.isAuthenticated)
  const authDisabled = useAuthStore((s) => s.authDisabled)
  const wsClientRef = useRef<WSClient | null>(null)
  const onMessageRef = useRef(onMessage)
  const optionsRef = useRef(options)
  const [lastMessage, setLastMessage] = useState<string | ArrayBuffer | null>(null)
  const [status, setStatus] = useState<'connecting' | 'connected' | 'disconnected' | 'reconnecting'>('disconnected')
  const [wsState, setWsState] = useState<WSState>('CLOSED')
  const [reconnectAttempts, setReconnectAttempts] = useState(0)

  useEffect(() => {
    onMessageRef.current = onMessage
  }, [onMessage])

  useEffect(() => {
    optionsRef.current = options
  }, [options])

  const wrappedOnMessage = useCallback((data: string | ArrayBuffer) => {
    setLastMessage(data)
    onMessageRef.current(data)
  }, [])

  useEffect(() => {
    if ((!isAuthenticated && !authDisabled) || optionsRef.current.skip) {
      return
    }

    const client = new WSClient()
    wsClientRef.current = client
    const opts = optionsRef.current

    const enhancedOptions: WSClientOptions = {
      ...opts,
      onOpen: () => {
        setStatus('connected')
        setWsState('OPEN')
        opts.onOpen?.()
      },
      onClose: () => {
        setStatus('disconnected')
        setWsState('CLOSED')
        opts.onClose?.()
      },
      onError: (error) => {
        setStatus('disconnected')
        opts.onError?.(error)
      },
      onReconnecting: (attempt) => {
        setStatus('reconnecting')
        setWsState('RECONNECTING')
        setReconnectAttempts(attempt)
        opts.onReconnecting?.(attempt)
      },
      onReconnectFailed: () => {
        setStatus('disconnected')
        setWsState('CLOSED')
        opts.onReconnectFailed?.()
      },
    }

    setTimeout(() => {
      setStatus('connecting')
      setWsState('CONNECTING')
    }, 0)
    client.connect(path, wrappedOnMessage, enhancedOptions)

    return () => {
      client.close()
      wsClientRef.current = null
      setStatus('disconnected')
      setWsState('CLOSED')
      setReconnectAttempts(0)
    }
  }, [path, isAuthenticated, authDisabled, wrappedOnMessage, options?.skip])

  const send = useCallback((data: string | ArrayBuffer) => {
    wsClientRef.current?.send(data)
  }, [])

  const reconnect = useCallback(() => {
    if ((!isAuthenticated && !authDisabled) || optionsRef.current.skip) {
      return
    }
    const opts = optionsRef.current
    wsClientRef.current?.close()
    setStatus('connecting')
    setWsState('CONNECTING')
    setTimeout(() => {
      wsClientRef.current?.connect(path, wrappedOnMessage, opts)
    }, 100)
  }, [path, isAuthenticated, authDisabled, wrappedOnMessage])

  const disconnect = useCallback(() => {
    wsClientRef.current?.close()
  }, [])

  return {
    status,
    send,
    lastMessage,
    reconnect,
    disconnect,
    wsState,
    reconnectAttempts,
  }
}

export interface UseWebSocketJSONOptions<T> extends Omit<UseWebSocketOptions, 'binary'> {
  parse?: (data: string) => T
}

export interface UseWebSocketJSONReturn<T> extends Omit<UseWebSocketReturn, 'lastMessage'> {
  lastMessage: T | null
}

export function useWebSocketJSON<T>(
  path: string,
  onMessage: (data: T) => void,
  options: UseWebSocketJSONOptions<T> = {}
): UseWebSocketJSONReturn<T> {
  const [lastMessage, setLastMessage] = useState<T | null>(null)
  const onMessageRef = useRef(onMessage)
  const parseRef = useRef(options.parse)

  useEffect(() => {
    onMessageRef.current = onMessage
  }, [onMessage])

  useEffect(() => {
    parseRef.current = options.parse
  }, [options.parse])

  const wrappedOnMessage = useCallback((data: string | ArrayBuffer) => {
    if (typeof data !== 'string') {
      console.warn('Expected string data for JSON parsing, got ArrayBuffer')
      return
    }

    try {
      const parsed = parseRef.current ? parseRef.current(data) : JSON.parse(data) as T
      setLastMessage(parsed)
      onMessageRef.current(parsed)
    } catch (error) {
      console.warn('Failed to parse WebSocket message as JSON:', error)
    }
  }, [])

  const ws = useWebSocket(path, wrappedOnMessage, { ...options, binary: false })

  return {
    ...ws,
    lastMessage,
  }
}

export function useWebSocketBinary(
  path: string,
  onMessage: (data: ArrayBuffer) => void,
  options: UseWebSocketOptions = {}
): UseWebSocketReturn {
  const wrappedOnMessage = useCallback((data: string | ArrayBuffer) => {
    if (typeof data === 'string') {
      console.warn('Expected ArrayBuffer data, got string')
      return
    }
    onMessage(data)
  }, [onMessage])

  return useWebSocket(path, wrappedOnMessage, { ...options, binary: true })
}
