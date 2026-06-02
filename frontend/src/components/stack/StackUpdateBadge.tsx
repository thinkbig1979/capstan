import { RefreshCw } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import type { UpdateJobStatus } from '@/stores/updateJobStore'

export interface StackUpdateBadgeProps {
  /** Number of available updates for this stack */
  count: number
  /** Called when the user clicks "Update & restart" */
  onUpdate: () => void
  /** Status of the active stack job, or undefined when no job is running */
  jobStatus?: UpdateJobStatus
  /** Whether the mutation is pending (button just submitted) */
  updatePending?: boolean
}

const ACTIVE_STATUSES = new Set<UpdateJobStatus>(['queued', 'pulling', 'recreating'])

function phaseLabel(status: UpdateJobStatus): string {
  switch (status) {
    case 'queued': return 'Queued…'
    case 'pulling': return 'Pulling…'
    case 'recreating': return 'Recreating…'
    default: return 'Updating…'
  }
}

export function StackUpdateBadge({
  count,
  onUpdate,
  jobStatus,
  updatePending = false,
}: StackUpdateBadgeProps) {
  const isActive = jobStatus !== undefined && ACTIVE_STATUSES.has(jobStatus)

  // Hide both badge and button when no updates and no active job
  if (count === 0 && !isActive) return null

  return (
    <>
      {count > 0 && (
        <Badge
          variant="destructive"
          className="bg-amber-500 text-white border-transparent hover:bg-amber-600"
        >
          {count} update{count > 1 ? 's' : ''} available
        </Badge>
      )}
      <Button
        variant="outline"
        size="sm"
        onClick={onUpdate}
        disabled={isActive || updatePending}
        className="shrink-0"
      >
        {isActive ? (
          <>
            <RefreshCw className="mr-2 h-4 w-4 animate-spin" />
            {phaseLabel(jobStatus!)}
          </>
        ) : (
          <>
            <RefreshCw className="mr-2 h-4 w-4" />
            Update &amp; restart
          </>
        )}
      </Button>
    </>
  )
}
