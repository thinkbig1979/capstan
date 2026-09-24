import { AlertTriangle } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

interface StatusStaleNoticeProps {
  /** "Stack statuses" on a list, "This stack's status" on one stack. */
  subject: string
  onRetry?: () => void
  className?: string
}

/**
 * The server answered, but its live Docker read failed and the status it sent
 * is the last one stored (`statusStale`, agent-os-xjzr). Unlike
 * RefreshFailedNotice the request itself succeeded, so that component's
 * "could not refresh" wording would be wrong here.
 */
export function StatusStaleNotice({ subject, onRetry, className }: StatusStaleNoticeProps) {
  return (
    <div
      // role="alert" for the same reason as RefreshFailedNotice: it mounts
      // together with its text, and an alert is announced on insertion.
      role="alert"
      className={cn(
        'flex flex-wrap items-center justify-between gap-3 rounded-md border border-warning/30 bg-warning/10 px-3 py-2 text-sm',
        className,
      )}
    >
      <div className="flex items-center gap-2">
        <AlertTriangle className="h-4 w-4 shrink-0 text-warning" aria-hidden="true" />
        <span>
          {subject} may be out of date: Docker could not be read, so the last recorded status is shown.
        </span>
      </div>
      {onRetry && (
        <Button type="button" variant="outline" size="sm" onClick={onRetry}>
          Retry
        </Button>
      )}
    </div>
  )
}
