import { Button } from '@/components/ui/button'
import { ArrowDownToLine, Play, RotateCcw, Square as SquareIcon } from 'lucide-react'
import type { BulkAction } from './constants'

interface BulkActionBarProps {
  selectedCount: number
  totalVisible: number
  bulkPending: boolean
  onSelectAllVisible: () => void
  onRunBulk: (action: BulkAction) => void
}

export function BulkActionBar({
  selectedCount,
  totalVisible,
  bulkPending,
  onSelectAllVisible,
  onRunBulk,
}: BulkActionBarProps) {
  return (
    <div className="px-3 py-2 border-b bg-muted/30 space-y-1.5">
      <div className="flex items-center justify-between text-[11px]">
        <span className="font-medium">
          {selectedCount} selected
        </span>
        <button
          type="button"
          onClick={onSelectAllVisible}
          className="text-primary hover:underline"
        >
          {selectedCount === totalVisible && totalVisible > 0
            ? 'Clear all'
            : 'Select all'}
        </button>
      </div>
      <div className="grid grid-cols-4 gap-1">
        <Button
          variant="outline"
          size="sm"
          className="h-7 px-0"
          disabled={selectedCount === 0 || bulkPending}
          onClick={() => onRunBulk('start')}
          title="Start selected"
        >
          <Play className="h-3.5 w-3.5" />
        </Button>
        <Button
          variant="outline"
          size="sm"
          className="h-7 px-0"
          disabled={selectedCount === 0 || bulkPending}
          onClick={() => onRunBulk('stop')}
          title="Stop selected"
        >
          <SquareIcon className="h-3.5 w-3.5" />
        </Button>
        <Button
          variant="outline"
          size="sm"
          className="h-7 px-0"
          disabled={selectedCount === 0 || bulkPending}
          onClick={() => onRunBulk('restart')}
          title="Restart selected"
        >
          <RotateCcw className="h-3.5 w-3.5" />
        </Button>
        <Button
          variant="outline"
          size="sm"
          className="h-7 px-0"
          disabled={selectedCount === 0 || bulkPending}
          onClick={() => onRunBulk('pull')}
          title="Pull selected"
        >
          <ArrowDownToLine className="h-3.5 w-3.5" />
        </Button>
      </div>
    </div>
  )
}
