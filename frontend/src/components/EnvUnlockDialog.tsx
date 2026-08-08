import { useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { LoadingSpinner } from '@/components/LoadingSkeleton'
import { authApi } from '@/lib/api'
import { useEnvUnlockStore } from '@/stores/envUnlockStore'
import { classifyError } from '@/lib/error-handler'
import { toast } from 'sonner'
import { useAuth } from '@/hooks/useAuth'
import { invalidateEnvUnlockQueries } from '@/lib/env-unlock-queries'

interface EnvUnlockDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  onUnlocked?: () => void
}

export function EnvUnlockDialog({ open, onOpenChange, onUnlocked }: EnvUnlockDialogProps) {
  const { authDisabled } = useAuth()
  const queryClient = useQueryClient()
  const unlock = useEnvUnlockStore((s) => s.unlock)
  const [password, setPassword] = useState('')
  const [submitting, setSubmitting] = useState(false)

  const reset = () => {
    setPassword('')
    setSubmitting(false)
  }

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!password) return
    setSubmitting(true)
    try {
      // Read the token defensively rather than destructuring: a 200 with an
      // unexpected body should still open the window it just paid for, and be
      // reported as a failed reveal rather than as a wrong password.
      const verified = await authApi.verifyPassword(password)
      unlock(verified?.unlockToken ?? null)
      // The secret surfaces were fetched redacted while locked, so the cached
      // copies hold blanks. Refetch them with the token attached, or the reveal
      // toggles would uncover empty strings (agent-os-7o5s).
      await invalidateEnvUnlockQueries(queryClient)
      toast.success('Environment variables unlocked for 5 minutes')
      onUnlocked?.()
      onOpenChange(false)
      reset()
    } catch (err) {
      toast.error(classifyError(err).message || 'Invalid password')
      setPassword('')
    } finally {
      setSubmitting(false)
    }
  }

  if (authDisabled) {
    return null
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) reset()
        onOpenChange(next)
      }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Unlock environment variables</DialogTitle>
          <DialogDescription>
            Enter your password to reveal sensitive values for the next 5 minutes.
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={handleSubmit} className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="env-unlock-password">Password</Label>
            <Input
              id="env-unlock-password"
              type="password"
              autoFocus
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              disabled={submitting}
            />
          </div>
          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              onClick={() => onOpenChange(false)}
              disabled={submitting}
            >
              Cancel
            </Button>
            <Button type="submit" disabled={submitting || !password}>
              {submitting ? (
                <>
                  <span className="mr-2"><LoadingSpinner size="small" /></span>
                  Verifying...
                </>
              ) : (
                'Unlock'
              )}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
