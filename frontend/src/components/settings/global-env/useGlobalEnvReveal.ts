import { useEffect, useState } from 'react'

/**
 * Per-row reveal state for sensitive values, keyed by the row's `_rowId` so a
 * delete above a revealed row does not move the reveal onto its neighbour
 * (agent-os-0b4m), gated behind the env-unlock
 * session: revealing requires auth to be disabled or the session already
 * unlocked, otherwise it opens the unlock dialog and defers the reveal until
 * it succeeds. Also re-masks every revealed value whenever the unlock
 * session ends (manual lock or auto-expiry).
 */
export function useGlobalEnvReveal(
  authDisabled: boolean,
  isUnlocked: () => boolean,
  unlockedUntil: number | null,
) {
  const [visible, setVisible] = useState<Record<number, boolean>>({})
  const [unlockDialogOpen, setUnlockDialogOpen] = useState(false)
  const [pendingRevealId, setPendingRevealId] = useState<number | null>(null)

  // Re-mask all values when the unlock session ends.
  useEffect(() => {
    if (unlockedUntil !== null) return
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setVisible({})
  }, [unlockedUntil])

  const toggleVisible = (rowId: number) => {
    if (visible[rowId]) {
      // Hiding never requires unlock.
      setVisible((prev) => ({ ...prev, [rowId]: false }))
      return
    }
    if (authDisabled || isUnlocked()) {
      setVisible((prev) => ({ ...prev, [rowId]: true }))
      return
    }
    setPendingRevealId(rowId)
    setUnlockDialogOpen(true)
  }

  const handleUnlocked = () => {
    if (pendingRevealId !== null) {
      setVisible((prev) => ({ ...prev, [pendingRevealId]: true }))
    }
    setPendingRevealId(null)
  }

  const handleDialogOpenChange = (open: boolean) => {
    setUnlockDialogOpen(open)
    if (!open) setPendingRevealId(null)
  }

  return {
    visible,
    setVisible,
    unlockDialogOpen,
    toggleVisible,
    handleUnlocked,
    handleDialogOpenChange,
  }
}
