import { useCallback, useRef, useState } from 'react'
import { toast } from 'sonner'
import { SESSION_WARNING_MINUTES } from './constants'

export interface UseInactivityTimerResult {
  disconnectCountdown: number | null
  resetInactivityTimer: () => void
  clearInactivityTimers: () => void
  clearDisconnectCountdown: () => void
}

// Owns the 25-minute-inactivity warning -> 5-minute grace period -> 60-second
// countdown -> forced-disconnect chain, lifted verbatim from the original
// Terminal component. `disconnect` is its only external input.
export function useInactivityTimer(disconnect: () => void): UseInactivityTimerResult {
  const [disconnectCountdown, setDisconnectCountdown] = useState<number | null>(null)
  const inactivityTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const disconnectTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  const clearInactivityTimers = useCallback(() => {
    if (inactivityTimerRef.current) {
      clearTimeout(inactivityTimerRef.current)
      inactivityTimerRef.current = null
    }
    if (disconnectTimerRef.current) {
      clearTimeout(disconnectTimerRef.current)
      disconnectTimerRef.current = null
    }
  }, [])

  const resetInactivityTimer = useCallback(() => {
    clearInactivityTimers()
    inactivityTimerRef.current = setTimeout(() => {
      toast.warning('Session will disconnect in 5 minutes', {
        duration: 300000,
      })
      disconnectTimerRef.current = setTimeout(() => {
        let remaining = 60
        setDisconnectCountdown(remaining)
        // A plain closure counter (rather than a functional setState updater)
        // keeps the side effects below out of the updater — they run once,
        // directly in this callback, not inside a function React may re-invoke.
        const countdownInterval = setInterval(() => {
          remaining -= 1
          if (remaining <= 0) {
            clearInterval(countdownInterval)
            setDisconnectCountdown(null)
            toast.error('Session disconnected due to inactivity (30 minutes)')
            disconnect()
            return
          }
          setDisconnectCountdown(remaining)
        }, 1000)
      }, 300000)
    }, SESSION_WARNING_MINUTES * 60 * 1000)
  }, [clearInactivityTimers, disconnect])

  const clearDisconnectCountdown = useCallback(() => {
    setDisconnectCountdown(null)
  }, [])

  return { disconnectCountdown, resetInactivityTimer, clearInactivityTimers, clearDisconnectCountdown }
}
