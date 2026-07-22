import { cn } from '@/lib/utils'
import { hasAnsi } from '@/lib/ansi'
import { getLogLevelColor } from './log-utils'
import { renderMessage } from './render-message'
import type { DisplayLogMessage } from './types'

interface LogLineProps {
  log: DisplayLogMessage
  wrap: boolean
  showTimestamps: boolean
  searchTerm: string
  containerColor: string
}

export function LogLine({ log, wrap, showTimestamps, searchTerm, containerColor }: LogLineProps) {
  const logLevelColor = hasAnsi(log.message) ? '' : getLogLevelColor(log.message)

  return (
    <div
      className={cn(
        'flex gap-2 py-0.5',
        wrap ? 'whitespace-pre-wrap wrap-break-word' : 'whitespace-pre'
      )}
      role="log"
    >
      {showTimestamps && log.timestamp && (
        <span className="text-muted-foreground shrink-0">[{log.timestamp}]</span>
      )}
      <span className={cn('font-medium shrink-0', containerColor)}>[{log.container}]</span>
      <span className={cn('flex-1', logLevelColor)}>{renderMessage(log.message, searchTerm)}</span>
    </div>
  )
}
