import { AlertTriangle } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

interface RefreshFailedNoticeProps {
  /**
   * What could not be refreshed, read straight after "Could not refresh" — so
   * "the backup history", not "Backup history".
   */
  what: string
  /**
   * True on a WRITE-BACK surface. The operator is about to save from these
   * fields and has to know they may be stale, which a reader of a table does
   * not: place the notice near the save control in that case.
   */
  beforeSave?: boolean
  onRetry?: () => void
  className?: string
}

/**
 * A query that ALREADY HAS data and then fails to refresh (agent-os-wczm).
 *
 * `isError` is true for a refetch failure as well as a first-load failure, and
 * treating the two the same replaced populated views with an error box:
 * staleTime is 30s app-wide, refetchOnWindowFocus is on, and a 500 is not
 * auto-retryable, so one 500 after a tab-away destroyed correct UI — and on the
 * forms, whatever the operator had typed into it.
 *
 * This is the other half of the fix: guard the error view on
 * `isError && !data` (or `isLoadingError`), and say the refresh failed here,
 * WITHOUT taking anything away. The values on screen are still the last ones
 * the server really sent, which is not the same as fabricating a value —
 * nothing agent-os-r1kc refuses is being shown.
 */
export function RefreshFailedNotice({
  what,
  beforeSave = false,
  onRetry,
  className,
}: RefreshFailedNoticeProps) {
  return (
    <div
      role="status"
      className={cn(
        'flex flex-wrap items-center justify-between gap-3 rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm',
        className,
      )}
    >
      <div className="flex items-center gap-2">
        <AlertTriangle className="h-4 w-4 shrink-0 text-destructive" aria-hidden="true" />
        <span>
          Could not refresh {what}. The values shown are the last ones the server sent
          {beforeSave ? ', so check them before saving.' : '.'}
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
