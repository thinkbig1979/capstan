import { useCallback, useRef, useState, useEffect } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { WSClient } from '@/lib/ws'
import { reconcileOnClose } from '@/lib/ws-reconcile'
import { queryKeys } from '@/lib/query-keys'
import { messageOrNull } from '@/lib/narrow'
import type { Outcome } from '@/types'

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
 *               success:false, or an error frame from the server)
 * - disconnected : the socket closed with no done or error frame, so the
 *               outcome is unknown. The operation may still be running on the
 *               server; the stack's queries are refetched (agent-os-s3fo).
 */
export type OperationStatus = 'idle' | 'running' | 'success' | 'no_change' | 'partial' | 'error' | 'disconnected'

// Edwin's wording (agent-os-s3fo decision, 2026-11-13).
const CONNECTION_LOST = 'Connection lost. The operation may still be running; check the stack status.'

interface OperationLine {
  type: string
  // agent-os-06c1: the rendered fields are `unknown`, not `string`. JSON.parse
  // returns `any`, so declaring them `string` is a claim tsc then stops
  // checking. This is the twin of useBackup.ts's frame handler.
  line?: unknown
  /** @deprecated Use outcome instead — kept for backward compatibility with backends not yet migrated. */
  success?: boolean
  error?: unknown
  phase?: string
  message?: unknown
  action?: string
  stack?: string
  /**
   * Typed outcome from the Action Truth Contract done frame.
   * Present when the backend has been migrated to emit outcome/reason.
   */
  outcome?: Outcome
  /** Human-readable description of the outcome. */
  reason?: unknown
}

export interface StreamingOperation {
  status: OperationStatus
  lines: string[]
  action: string
  error: string | null
  /**
   * True once the run's outcome is final: a done frame arrived, or the socket
   * ended. An error frame alone is not final (a scanner error mid-run is
   * followed by the run's done frame), so act on `status` only when this is set.
   */
  finished: boolean
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
  const [finished, setFinished] = useState(false)
  const queryClient = useQueryClient()
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
    setFinished(false)
    clientRef.current?.close()
    clientRef.current = null
  }, [])

  const execute = useCallback((stackId: string, opAction: 'pull' | 'start' | 'stop' | 'restart') => {
    clientRef.current?.close()

    setAction(opAction)
    setLines([])
    setError(null)
    setStatus('running')
    setFinished(false)
    completedRef.current = false
    // An error frame is the server saying the command failed; a close after
    // it is not a lost connection.
    let errorFrame = false

    // The URL is the command: the server runs it on every upgrade, so this
    // socket must never re-dial (agent-os-s3fo). clientRef keeps the client
    // after a close so Dismiss and unmount can still close() it.
    const client = new WSClient()
    clientRef.current = client

    client.connect(
      `/ws/operations/${encodeURIComponent(stackId)}/${opAction}`,
      (data) => {
        if (typeof data !== 'string') return
        try {
          const msg = JSON.parse(data) as OperationLine

          // agent-os-06c1: narrowed once, then used. Every field below lands in
          // `lines` (string[]) or `error` (string).
          const line = messageOrNull(msg.line)
          const phase = messageOrNull(msg.message)
          const reason = messageOrNull(msg.reason)
          const error = messageOrNull(msg.error)

          if (msg.type === 'data' && line) {
            setLines(prev => [...prev, line])
          } else if (msg.type === 'phase' && phase) {
            setLines(prev => [...prev, `--- ${phase} ---`])
          } else if (msg.type === 'start') {
            // initial handshake — no UI change needed
          } else if (msg.type === 'done') {
            const finalStatus = outcomeToStatus(msg)

            // Finding #18: set completedRef BEFORE calling client.close() so
            // the onClose handler never overwrites a real terminal outcome with
            // 'error' due to a race between the close and the onClose callback.
            completedRef.current = true
            setFinished(true)

            if (finalStatus === 'success') {
              setStatus('success')
              const label = reason ?? 'Operation completed successfully.'
              setLines(prev => [...prev, label])
            } else if (finalStatus === 'no_change') {
              setStatus('no_change')
              const label = reason ?? 'No change — already in desired state.'
              setLines(prev => [...prev, label])
            } else if (finalStatus === 'partial') {
              setStatus('partial')
              const label = reason ?? 'Operation partially completed.'
              setLines(prev => [...prev, label])
            } else {
              // error / failed
              setStatus('error')
              const errMsg = error ?? reason ?? 'Operation failed'
              setError(errMsg)
              setLines(prev => [...prev, `Error: ${errMsg}`])
            }

            client.close()
            clientRef.current = null
          } else if (msg.type === 'error') {
            errorFrame = true
            setStatus('error')
            const errMsg = error ?? 'Unknown error'
            setError(errMsg)
            setLines(prev => [...prev, `Error: ${errMsg}`])
          }
        } catch {
          // ignore non-JSON frames
        }
      },
      {
        reconnect: false,
        // No onError: an error event is always followed by a close, and the
        // close decides. A drop is not a failure (finding #18), and the
        // browser cannot tell a refused upgrade from a lost one.
        onClose: () => {
          setFinished(true)
          // Finding #18 / ws-reconcile: after a done frame the real outcome
          // stands. Without one, the server state is the truth: refetch it.
          reconcileOnClose({
            completed: completedRef.current,
            refetch: () => {
              queryClient.invalidateQueries({ queryKey: queryKeys.stack.detail(stackId) })
              queryClient.invalidateQueries({ queryKey: queryKeys.stacks() })
            },
          })
          if (!completedRef.current && !errorFrame) {
            // Leaves 'running', so the page's Start/Stop/Restart/Pull
            // buttons are enabled again.
            setStatus('disconnected')
            setLines(prev => [...prev, CONNECTION_LOST])
          }
        },
      }
    )
  }, [queryClient])

  return { status, lines, action, error, finished, execute, cancel, reset }
}
