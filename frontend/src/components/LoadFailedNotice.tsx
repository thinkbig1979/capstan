import { AlertTriangle } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

interface LoadFailedNoticeProps {
  /**
   * What could not be loaded, read straight after "Could not load" — so
   * "the scan depth", not "Scan depth".
   */
  what: string
  /**
   * What the operator cannot do until it loads, e.g. "Saving is disabled until
   * it loads." Omit on a read-only view.
   */
  consequence?: string
  onRetry: () => void
  className?: string
}

/**
 * A query that FAILED ITS FIRST LOAD and so has no data at all
 * (agent-os-gs2y, r6fx, v824). The first-load twin of RefreshFailedNotice.
 *
 * Rendered IN PLACE of the value or list the query would have filled. The
 * failure it replaces was a fallback that read as fact: a form seeded with a
 * default ("1 level deep") that one Save click persisted, or an empty state
 * ("No Docker images found on this host") for a host whose Docker read failed.
 * Nothing here claims a value, so no write can be seeded from it.
 */
export function LoadFailedNotice({ what, consequence, onRetry, className }: LoadFailedNoticeProps) {
  return (
    <div
      // role="alert" for the same reason as RefreshFailedNotice: it is mounted
      // together with its text, which a polite region may not announce.
      role="alert"
      className={cn(
        'flex flex-wrap items-center justify-between gap-3 rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm',
        className,
      )}
    >
      <div className="flex items-center gap-2">
        <AlertTriangle className="h-4 w-4 shrink-0 text-destructive" aria-hidden="true" />
        <span>
          Could not load {what}.{consequence ? ` ${consequence}` : ''}
        </span>
      </div>
      <Button type="button" variant="outline" size="sm" onClick={onRetry}>
        Retry
      </Button>
    </div>
  )
}
