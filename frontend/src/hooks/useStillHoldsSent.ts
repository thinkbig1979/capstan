import { useEffect, useRef } from 'react'

/**
 * "Mark clean only if the editor still holds what was sent" (agent-os-3761,
 * agent-os-lqcw, agent-os-k564). A save's callback closes over the render that
 * clicked Save, so typing that landed while the save was in flight is not in
 * its scope: it reads the latest editor state through a ref instead, and an
 * editor that moved on stays dirty.
 *
 * `remember` records what a save sent; `stillHolds` (call it only from a
 * callback, never while rendering) is true when the latest state still holds
 * it, by the editor's own `same`.
 */
export function useStillHoldsSent<T>(current: T, same: (latest: T, sent: T) => boolean) {
  const latest = useRef(current)
  useEffect(() => {
    latest.current = current
  })
  const sentRef = useRef<{ value: T } | null>(null)
  return {
    remember: (value: T) => {
      sentRef.current = { value }
    },
    stillHolds: () => sentRef.current !== null && same(latest.current, sentRef.current.value),
  }
}
