import { useCallback, useRef, useState, useEffect } from 'react'
import { WSClient } from '@/lib/ws'

export type OperationStatus = 'idle' | 'running' | 'success' | 'error'

export interface OperationLine {
  type: string
  line?: string
  success?: boolean
  error?: string
  phase?: string
  message?: string
  action?: string
  stack?: string
}

export interface StreamingOperation {
  status: OperationStatus
  lines: string[]
  action: string
  error: string | null
  execute: (stackId: string, action: 'pull' | 'start' | 'stop' | 'restart') => void
  cancel: () => void
  reset: () => void
}

export function useStreamingOperation(): StreamingOperation {
  const [status, setStatus] = useState<OperationStatus>('idle')
  const [lines, setLines] = useState<string[]>([])
  const [action, setAction] = useState('')
  const [error, setError] = useState<string | null>(null)
  const clientRef = useRef<WSClient | null>(null)
  const completedRef = useRef(false)

  useEffect(() => {
    return () => {
      if (clientRef.current) {
        clientRef.current.close()
        clientRef.current = null
      }
    }
  }, [])

  const cancel = useCallback(() => {
    clientRef.current?.close()
    clientRef.current = null
    setStatus(prev => prev === 'running' ? 'idle' : prev)
  }, [])

  const reset = useCallback(() => {
    setStatus('idle')
    setLines([])
    setAction('')
    setError(null)
    clientRef.current?.close()
    clientRef.current = null
  }, [])

  const execute = useCallback((stackId: string, opAction: 'pull' | 'start' | 'stop' | 'restart') => {
    clientRef.current?.close()

    setAction(opAction)
    setLines([])
    setError(null)
    setStatus('running')
    completedRef.current = false

    const client = new WSClient()
    clientRef.current = client

    client.connect(
      `/ws/operations/${encodeURIComponent(stackId)}/${opAction}`,
      (data) => {
        if (typeof data !== 'string') return
        try {
          const msg = JSON.parse(data) as OperationLine

          if (msg.type === 'data' && msg.line) {
            setLines(prev => [...prev, msg.line!])
          } else if (msg.type === 'phase' && msg.message) {
            setLines(prev => [...prev, `--- ${msg.message} ---`])
          } else if (msg.type === 'start') {
            // initial message
          } else if (msg.type === 'done') {
            if (msg.success) {
              setStatus('success')
              setLines(prev => [...prev, 'Operation completed successfully.'])
            } else {
              setStatus('error')
              const errMsg = msg.error || 'Operation failed'
              setError(errMsg)
              setLines(prev => [...prev, `Error: ${errMsg}`])
            }
            client.close()
            clientRef.current = null
            completedRef.current = true
          } else if (msg.type === 'error') {
            setStatus('error')
            const errMsg = msg.error || 'Unknown error'
            setError(errMsg)
            setLines(prev => [...prev, `Error: ${errMsg}`])
          }
        } catch {
          // ignore non-JSON
        }
      },
      {
        onClose: () => {
          if (!completedRef.current) {
            setStatus('error')
            setLines(prev => [...prev, 'Connection closed.'])
          }
          clientRef.current = null
        },
        onError: () => {
          if (!completedRef.current) {
            setStatus('error')
            setError('Connection failed')
          }
          clientRef.current = null
        },
        onReconnectFailed: () => {
          if (!completedRef.current) {
            setStatus('error')
            setError('Connection lost and reconnect failed')
          }
          clientRef.current = null
        },
      }
    )
  }, [])

  return { status, lines, action, error, execute, cancel, reset }
}
