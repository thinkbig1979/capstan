import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Play, Square, RefreshCw, Trash2, MoreHorizontal } from 'lucide-react'

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
      {/* Destructive actions live behind the overflow menu, not as an
          always-visible red button next to Start/Stop. */}
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button variant="ghost" size="icon" className="h-7 w-7" disabled={isDeleting} title="More actions" aria-label={`More actions for ${stackName}`}>
            <MoreHorizontal className="h-3.5 w-3.5" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end">
          <DropdownMenuItem
            className="text-destructive focus:text-destructive"
            disabled={isDeleting || deletePending}
            onClick={(e) => onDelete(stackId, stackName, e)}
          >
            <Trash2 className="h-4 w-4" />
            Delete {stackName}
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  )
}
