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
  const { isAuthenticated, authDisabled } = useAuthStore()
  const wsClientRef = useRef<WSClient | null>(null)
  const onMessageRef = useRef(onMessage)
  const [lastMessage, setLastMessage] = useState<string | ArrayBuffer | null>(null)
  const [status, setStatus] = useState<'connecting' | 'connected' | 'disconnected' | 'reconnecting'>('disconnected')
  const [wsState, setWsState] = useState<WSState>('CLOSED')
  const [reconnectAttempts, setReconnectAttempts] = useState(0)

  useEffect(() => {
    onMessageRef.current = onMessage
  }, [onMessage])

  const wrappedOnMessage = useCallback((data: string | ArrayBuffer) => {
    setLastMessage(data)
    onMessageRef.current(data)
  }, [])

  useEffect(() => {
    if ((!isAuthenticated && !authDisabled) || options.skip) {
      return
    }

    const client = new WSClient()
    wsClientRef.current = client

    const enhancedOptions: WSClientOptions = {
      ...options,
      onOpen: () => {
        setStatus('connected')
        setWsState('OPEN')
        options.onOpen?.()
      },
      onClose: () => {
        setStatus('disconnected')
        setWsState('CLOSED')
        options.onClose?.()
      },
      onError: (error) => {
        setStatus('disconnected')
        options.onError?.(error)
      },
      onReconnecting: (attempt) => {
        setStatus('reconnecting')
        setWsState('RECONNECTING')
        setReconnectAttempts(attempt)
        options.onReconnecting?.(attempt)
      },
      onReconnectFailed: () => {
        setStatus('disconnected')
        setWsState('CLOSED')
        options.onReconnectFailed?.()
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
  }, [path, isAuthenticated, authDisabled, options.skip, options, wrappedOnMessage])

  const send = useCallback((data: string | ArrayBuffer) => {
    wsClientRef.current?.send(data)
  }, [])

  const reconnect = useCallback(() => {
    if ((!isAuthenticated && !authDisabled) || options.skip) {
      return
    }
    wsClientRef.current?.close()
    setStatus('connecting')
    setWsState('CONNECTING')
    setTimeout(() => {
      wsClientRef.current?.connect(path, wrappedOnMessage, options)
    }, 100)
  }, [path, isAuthenticated, authDisabled, options, wrappedOnMessage])

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

  const wrappedOnMessage = useCallback((data: string | ArrayBuffer) => {
    if (typeof data !== 'string') {
      console.warn('Expected string data for JSON parsing, got ArrayBuffer')
      return
    }

    try {
      const parsed = options.parse ? options.parse(data) : JSON.parse(data) as T
      setLastMessage(parsed)
      onMessage(parsed)
    } catch (error) {
      console.warn('Failed to parse WebSocket message as JSON:', error)
    }
  }, [onMessage, options])

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
