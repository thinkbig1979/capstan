import { Button } from '@/components/ui/button'
import { Play, Square, RefreshCw, Trash2 } from 'lucide-react'

interface StackRowActionsProps {
  stackId: string
  stackName: string
  status: string
  isDeleting: boolean
  startPending: boolean
  stopPending: boolean
  restartPending: boolean
  deletePending: boolean
  onStart: (stackId: string, e: React.MouseEvent) => void
  onStop: (stackId: string, e: React.MouseEvent) => void
  onRestart: (stackId: string, e: React.MouseEvent) => void
  onDelete: (stackId: string, stackName: string, e: React.MouseEvent) => void
}

export function StackRowActions({
  stackId,
  stackName,
  status,
  isDeleting,
  startPending,
  stopPending,
  restartPending,
  deletePending,
  onStart,
  onStop,
  onRestart,
  onDelete,
}: StackRowActionsProps) {
  return (
    <div className="flex items-center gap-1" onClick={(e) => e.stopPropagation()} role="group" aria-label="Stack actions">
      {status !== 'running' && (
        <Button variant="ghost" size="icon" className="h-7 w-7" onClick={(e) => onStart(stackId, e)} disabled={startPending} title="Start" aria-label={`Start ${stackName}`}>
          <Play className="h-3.5 w-3.5" />
        </Button>
      )}
      {status === 'running' && (
        <Button variant="ghost" size="icon" className="h-7 w-7" onClick={(e) => onStop(stackId, e)} disabled={stopPending} title="Stop" aria-label={`Stop ${stackName}`}>
          <Square className="h-3.5 w-3.5" />
        </Button>
      )}
      {status === 'running' && (
        <Button variant="ghost" size="icon" className="h-7 w-7" onClick={(e) => onRestart(stackId, e)} disabled={restartPending} title="Restart" aria-label={`Restart ${stackName}`}>
          <RefreshCw className={`h-3.5 w-3.5 ${restartPending ? 'animate-spin' : ''}`} />
        </Button>
      )}
      <Button variant="ghost" size="icon" className="h-7 w-7 text-destructive hover:text-destructive" onClick={(e) => onDelete(stackId, stackName, e)} disabled={isDeleting || deletePending} title="Delete" aria-label={`Delete ${stackName}`}>
        <Trash2 className="h-3.5 w-3.5" />
      </Button>
    </div>
  )
}
