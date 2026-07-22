import { ArrowDown } from 'lucide-react'
import type { LogTimeRange } from '@/stores/uiStore'
import { CONTAINER_COLORS } from './constants'
import { LogLine } from './LogLine'
import type { DisplayLogMessage } from './types'

interface LogListProps {
  ref?: React.Ref<HTMLDivElement>
  filteredLogs: DisplayLogMessage[]
  totalLogsCount: number
  hasRunningContainers: boolean
  errorsOnly: boolean
  timeRange: LogTimeRange
  wrap: boolean
  showTimestamps: boolean
  searchTerm: string
  containerColors: Map<string, string>
  scrolledUp: boolean
  newCount: number
  onJumpToLatest: () => void
}

export function LogList({
  ref,
  filteredLogs,
  totalLogsCount,
  hasRunningContainers,
  errorsOnly,
  timeRange,
  wrap,
  showTimestamps,
  searchTerm,
  containerColors,
  scrolledUp,
  newCount,
  onJumpToLatest,
}: LogListProps) {
  return (
    <div className="relative flex-1 min-h-0">
      <div
        ref={ref}
        className="h-full overflow-auto rounded-lg border bg-muted/50 p-4 font-mono text-sm"
      >
        {filteredLogs.length === 0 ? (
          <div className="flex h-full items-center justify-center text-muted-foreground">
            {!hasRunningContainers
              ? 'No containers are running. Start the stack to view logs.'
              : totalLogsCount === 0
                ? 'Waiting for logs...'
                : errorsOnly
                  ? 'No errors or warnings match current filters'
                  : timeRange !== 'all'
                    ? 'No logs in selected time range'
                    : 'No logs match current filters'}
          </div>
        ) : (
          filteredLogs.map((log) => (
            <LogLine
              key={log.id}
              log={log}
              wrap={wrap}
              showTimestamps={showTimestamps}
              searchTerm={searchTerm}
              containerColor={containerColors.get(log.container) ?? CONTAINER_COLORS[0]}
            />
          ))
        )}
      </div>

      {scrolledUp && (
        <button
          type="button"
          onClick={onJumpToLatest}
          className="absolute bottom-4 left-1/2 -translate-x-1/2 flex items-center gap-1.5 rounded-full bg-primary px-3 py-1.5 text-xs font-medium text-primary-foreground shadow-md hover:bg-primary/90 transition-colors"
        >
          <ArrowDown className="h-3.5 w-3.5" />
          {newCount > 0 ? `${newCount} new line${newCount === 1 ? '' : 's'}` : 'Jump to latest'}
        </button>
      )}
    </div>
  )
}
