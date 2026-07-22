import { useEffect, useState } from 'react'

/**
 * Gates revealing the repository password behind the env-unlock session:
 * revealing requires auth to be disabled or the session already unlocked,
 * otherwise it opens the unlock dialog and defers the reveal until it
 * succeeds. Also re-masks the password whenever the unlock session ends
 * (manual lock or auto-expiry).
 */
export function usePasswordReveal(authDisabled: boolean, isUnlocked: () => boolean, unlockedUntil: number | null) {
  const [showPassword, setShowPassword] = useState(false)
  const [unlockDialogOpen, setUnlockDialogOpen] = useState(false)
  const [pendingReveal, setPendingReveal] = useState(false)

  // Re-mask password reveal when unlock session expires
  useEffect(() => {
    if (unlockedUntil !== null) return
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setShowPassword(false)
  }, [unlockedUntil])

  const handleTogglePasswordReveal = () => {
    if (showPassword) {
      setShowPassword(false)
      return
    }
    if (authDisabled || isUnlocked()) {
      setShowPassword(true)
      return
    }
    setPendingReveal(true)
    setUnlockDialogOpen(true)
  }

  const handleUnlocked = () => {
    if (pendingReveal) setShowPassword(true)
    setPendingReveal(false)
  }

  const handleDialogOpenChange = (open: boolean) => {
    setUnlockDialogOpen(open)
    if (!open) setPendingReveal(false)
  }

  return {
    showPassword,
    unlockDialogOpen,
    handleTogglePasswordReveal,
    handleUnlocked,
    handleDialogOpenChange,
  }
}
