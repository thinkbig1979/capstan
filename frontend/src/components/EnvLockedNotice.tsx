import type { ReactNode } from 'react'
import { Lock } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

interface EnvLockedNoticeProps {
  /** What a locked session cannot do here, and why. */
  children: ReactNode
  onUnlock: () => void
  unlockLabel?: string
  className?: string
}

/**
 * The strip shown where a locked session meets controls the server refuses
 * while env values are locked, with the unlock action beside the reason
 * (agent-os-ly6i, h71i).
 */
export function EnvLockedNotice({ children, onUnlock, unlockLabel = 'Unlock', className }: EnvLockedNoticeProps) {
  return (
    <div
      role="status"
      className={cn(
        'flex flex-wrap items-center justify-between gap-3 rounded-md border bg-muted/50 px-3 py-2 text-sm',
        className,
      )}
    >
      <div className="flex items-center gap-2">
        <Lock className="h-4 w-4 shrink-0" aria-hidden="true" />
        <span>{children}</span>
      </div>
      <Button type="button" size="sm" variant="outline" onClick={onUnlock}>
        {unlockLabel}
      </Button>
    </div>
  )
}
