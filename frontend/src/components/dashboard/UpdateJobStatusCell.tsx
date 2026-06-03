import { RefreshCw, CheckCircle, AlertCircle, Info, ChevronDown, ChevronRight } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Tooltip, TooltipTrigger, TooltipContent, TooltipProvider } from '@/components/ui/tooltip'
import type { UpdateJob } from '@/stores/updateJobStore'

// ── Main cell component ───────────────────────────────────────────────────────

export interface UpdateJobStatusCellProps {
  /** The active or recent job for this container, if any */
  job: UpdateJob | undefined
  /** Whether the log panel is currently expanded */
  expanded: boolean
  /** Called when the user clicks the expand chevron */
  onToggleExpand: () => void
  /** Called when the user triggers an update (no job, or job in error state) */
  onUpdate: () => void
  /** Label for the update button when container is running */
  isRunning: boolean
  /** Whether any other update mutation is pending (disables the button) */
  updatePending: boolean
}

const TERMINAL_STATUSES = new Set(['success', 'error'])

/**
 * Derive the effective display outcome from the job's typed `outcome` field
 * (truth-first) with a fallback to the legacy `status` field so the cell is
 * backward-compatible while the backend deploys the new contract.
 *
 * Important distinction:
 *   outcome='success'   → image digest genuinely advanced  → show green "Updated"
 *   outcome='no_change' → image already up to date, no-op  → show blue "Already up to date"
 *   outcome='failed'    → update failed                    → show red "Failed"
 *   outcome=undefined   → backend not yet migrated; fall back to status
 */
function resolveDisplayOutcome(job: UpdateJob): 'success' | 'no_change' | 'failed' | null {
  if (!TERMINAL_STATUSES.has(job.status)) return null
  // Prefer the explicit outcome when it has arrived.
  if (job.outcome === 'success') return 'success'
  if (job.outcome === 'no_change') return 'no_change'
  if (job.outcome === 'failed') return 'failed'
  // Fallback for backends that have not yet shipped the outcome field.
  if (job.status === 'success') return 'success'
  if (job.status === 'error') return 'failed'
  return null
}

export function UpdateJobStatusCell({
  job,
  expanded,
  onToggleExpand,
  onUpdate,
  isRunning,
  updatePending,
}: UpdateJobStatusCellProps) {
  const hasLog = !!job
  const showExpand = hasLog

  const statusCell = () => {
    if (!job || (TERMINAL_STATUSES.has(job.status) && !expanded)) {
      const displayOutcome = job ? resolveDisplayOutcome(job) : null

      if (displayOutcome === 'failed') {
        const errorMsg = job?.reason || job?.error
        return (
          <TooltipProvider>
            <Tooltip>
              <TooltipTrigger asChild>
                <div className="flex items-center gap-1">
                  <AlertCircle className="h-3.5 w-3.5 text-destructive" />
                  <span className="text-xs text-destructive">Failed</span>
                </div>
              </TooltipTrigger>
              {errorMsg && (
                <TooltipContent>
                  <p className="max-w-xs">{errorMsg}</p>
                </TooltipContent>
              )}
            </Tooltip>
          </TooltipProvider>
        )
      }

      if (displayOutcome === 'no_change') {
        return (
          <TooltipProvider>
            <Tooltip>
              <TooltipTrigger asChild>
                <div className="flex items-center gap-1">
                  <Info className="h-3.5 w-3.5 text-info" />
                  <span className="text-xs text-info">Already up to date</span>
                </div>
              </TooltipTrigger>
              {job?.reason && (
                <TooltipContent>
                  <p className="max-w-xs">{job.reason}</p>
                </TooltipContent>
              )}
            </Tooltip>
          </TooltipProvider>
        )
      }

      if (displayOutcome === 'success') {
        return (
          <TooltipProvider>
            <Tooltip>
              <TooltipTrigger asChild>
                <div className="flex items-center gap-1">
                  <CheckCircle className="h-3.5 w-3.5 text-success" />
                  <span className="text-xs text-success">Updated</span>
                </div>
              </TooltipTrigger>
              {job?.reason && (
                <TooltipContent>
                  <p className="max-w-xs">{job.reason}</p>
                </TooltipContent>
              )}
            </Tooltip>
          </TooltipProvider>
        )
      }

      // No job (or evicted terminal): show the update button
      return (
        <Button
          variant="default"
          size="sm"
          className="h-7 text-xs"
          onClick={onUpdate}
          disabled={updatePending}
        >
          <RefreshCw className="mr-1 h-3 w-3" />
          {isRunning ? 'Update & Restart' : 'Update'}
        </Button>
      )
    }

    if (job.status === 'queued') {
      return (
        <span className="text-xs text-muted-foreground px-2 py-1 rounded-md bg-muted">
          Queued…
        </span>
      )
    }

    if (job.status === 'pulling') {
      return (
        <div className="flex items-center gap-1 text-info">
          <RefreshCw className="h-3.5 w-3.5 animate-spin" />
          <span className="text-xs">Pulling…</span>
        </div>
      )
    }

    if (job.status === 'recreating') {
      return (
        <div className="flex items-center gap-1 text-info">
          <RefreshCw className="h-3.5 w-3.5 animate-spin" />
          <span className="text-xs">Recreating…</span>
        </div>
      )
    }

    // Terminal states (expanded) — derive from outcome for truthfulness.
    const displayOutcome = resolveDisplayOutcome(job)

    if (displayOutcome === 'failed') {
      const errorMsg = job.reason || job.error
      return (
        <TooltipProvider>
          <Tooltip>
            <TooltipTrigger asChild>
              <div className="flex items-center gap-1">
                <AlertCircle className="h-3.5 w-3.5 text-destructive" />
                <span className="text-xs text-destructive">Failed</span>
              </div>
            </TooltipTrigger>
            {errorMsg && (
              <TooltipContent>
                <p className="max-w-xs">{errorMsg}</p>
              </TooltipContent>
            )}
          </Tooltip>
        </TooltipProvider>
      )
    }

    if (displayOutcome === 'no_change') {
      return (
        <TooltipProvider>
          <Tooltip>
            <TooltipTrigger asChild>
              <div className="flex items-center gap-1">
                <Info className="h-3.5 w-3.5 text-info" />
                <span className="text-xs text-info">Already up to date</span>
              </div>
            </TooltipTrigger>
            {job.reason && (
              <TooltipContent>
                <p className="max-w-xs">{job.reason}</p>
              </TooltipContent>
            )}
          </Tooltip>
        </TooltipProvider>
      )
    }

    if (displayOutcome === 'success') {
      return (
        <TooltipProvider>
          <Tooltip>
            <TooltipTrigger asChild>
              <div className="flex items-center gap-1">
                <CheckCircle className="h-3.5 w-3.5 text-success" />
                <span className="text-xs text-success">Updated</span>
              </div>
            </TooltipTrigger>
            {job.reason && (
              <TooltipContent>
                <p className="max-w-xs">{job.reason}</p>
              </TooltipContent>
            )}
          </Tooltip>
        </TooltipProvider>
      )
    }

    return null
  }

  const displayOutcome = job ? resolveDisplayOutcome(job) : null

  return (
    <div className="space-y-1">
      <div className="flex items-center gap-1.5">
        {showExpand && (
          <button
            type="button"
            aria-label={expanded ? 'Collapse log' : 'Expand log'}
            onClick={onToggleExpand}
            className="text-muted-foreground hover:text-foreground transition-colors"
          >
            {expanded ? (
              <ChevronDown className="h-3.5 w-3.5" />
            ) : (
              <ChevronRight className="h-3.5 w-3.5" />
            )}
          </button>
        )}
        {statusCell()}
        {/* After a failed outcome, also offer a retry button next to the indicator */}
        {displayOutcome === 'failed' && (
          <Button
            variant="outline"
            size="sm"
            className="h-7 text-xs"
            onClick={onUpdate}
            disabled={updatePending}
          >
            Retry
          </Button>
        )}
      </div>
    </div>
  )
}
