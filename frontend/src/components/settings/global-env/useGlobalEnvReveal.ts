import { useEffect, useState } from 'react'

/**
 * Per-row reveal state for sensitive values, gated behind the env-unlock
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
  const [pendingRevealIndex, setPendingRevealIndex] = useState<number | null>(null)

  // Re-mask all values when the unlock session ends.
  useEffect(() => {
    if (unlockedUntil !== null) return
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setVisible({})
  }, [unlockedUntil])

  const toggleVisible = (index: number) => {
    if (visible[index]) {
      // Hiding never requires unlock.
      setVisible((prev) => ({ ...prev, [index]: false }))
      return
    }
    if (authDisabled || isUnlocked()) {
      setVisible((prev) => ({ ...prev, [index]: true }))
      return
    }
    setPendingRevealIndex(index)
    setUnlockDialogOpen(true)
  }

  const handleUnlocked = () => {
    if (pendingRevealIndex !== null) {
      setVisible((prev) => ({ ...prev, [pendingRevealIndex]: true }))
    }
    setPendingRevealIndex(null)
  }

  const handleDialogOpenChange = (open: boolean) => {
    setUnlockDialogOpen(open)
    if (!open) setPendingRevealIndex(null)
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
