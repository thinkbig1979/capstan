import { useCallback, useRef, useState, useEffect } from 'react'
import { WSClient } from '@/lib/ws'
import { reconcileOnClose } from '@/lib/ws-reconcile'

/**
 * OperationStatus mirrors the Action Truth Contract outcomes for stack
 * streaming operations (audit finding #18).
 *
 * - idle      : no operation in progress
 * - running   : operation in progress
 * - success   : completed; effect verified
 * - no_change : completed; resource was already in desired state (NOT a green
 *               success — render as info/neutral)
 * - partial   : completed with partial success (render as warning)
 * - error     : failed (maps from backend outcome:'failed' or legacy
 *               success:false, or connection drop without a done frame)
 */
export type OperationStatus = 'idle' | 'running' | 'success' | 'no_change' | 'partial' | 'error'

export interface OperationLine {
  type: string
  line?: string
  /** @deprecated Use outcome instead — kept for backward compatibility with backends not yet migrated. */
  success?: boolean
  error?: string
  phase?: string
  message?: string
  action?: string
  stack?: string
  /**
   * Typed outcome from the Action Truth Contract done frame.
   * Present when the backend has been migrated to emit outcome/reason.
   */
  outcome?: 'success' | 'no_change' | 'partial' | 'failed'
  /** Human-readable description of the outcome. */
  reason?: string
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

/**
 * Map a done frame's outcome (Action Truth Contract) or legacy success flag
 * to an OperationStatus value. Backend 'failed' → 'error' so callers only
 * deal with frontend-level status names.
 */
function outcomeToStatus(msg: OperationLine): OperationStatus {
  if (msg.outcome) {
    // Prefer the typed outcome when present (migrated backend).
    switch (msg.outcome) {
      case 'success':   return 'success'
      case 'no_change': return 'no_change'
      case 'partial':   return 'partial'
      case 'failed':    return 'error'
    }
  }
  // Legacy fallback: key off success boolean (pre-migration backends).
  return msg.success ? 'success' : 'error'
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
            // initial handshake — no UI change needed
          } else if (msg.type === 'done') {
            const finalStatus = outcomeToStatus(msg)

            // Finding #18: set completedRef BEFORE calling client.close() so
            // the onClose handler never overwrites a real terminal outcome with
            // 'error' due to a race between the close and the onClose callback.
            completedRef.current = true

            if (finalStatus === 'success') {
              setStatus('success')
              const label = msg.reason || 'Operation completed successfully.'
              setLines(prev => [...prev, label])
            } else if (finalStatus === 'no_change') {
              setStatus('no_change')
              const label = msg.reason || 'No change — already in desired state.'
              setLines(prev => [...prev, label])
            } else if (finalStatus === 'partial') {
              setStatus('partial')
              const label = msg.reason || 'Operation partially completed.'
              setLines(prev => [...prev, label])
            } else {
              // error / failed
              setStatus('error')
              const errMsg = msg.error || msg.reason || 'Operation failed'
              setError(errMsg)
              setLines(prev => [...prev, `Error: ${errMsg}`])
            }

            client.close()
            clientRef.current = null
          } else if (msg.type === 'error') {
            setStatus('error')
            const errMsg = msg.error || 'Unknown error'
            setError(errMsg)
            setLines(prev => [...prev, `Error: ${errMsg}`])
          }
        } catch {
          // ignore non-JSON frames
        }
      },
      {
        onClose: () => {
          // Finding #18 / ws-reconcile: if a terminal done frame was received,
          // completedRef is already true and we must NOT overwrite the real
          // outcome. If the socket closed without a done frame we cannot safely
          // assert failure — reconcile by leaving the status alone; callers
          // that need source-of-truth recovery should pass a refetch function.
          reconcileOnClose({
            completed: completedRef.current,
            refetch: () => {
              // Without a refetch callback available here we do nothing —
              // the caller's query will invalidate on the next user interaction.
              // We intentionally do NOT flip to 'error' on unexplained closes
              // (finding #18: backup live status lies on disconnect).
            },
          })
          if (!completedRef.current) {
            setLines(prev => [...prev, 'Connection closed unexpectedly.'])
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
