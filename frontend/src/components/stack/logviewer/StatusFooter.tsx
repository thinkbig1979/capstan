import { MAX_LOG_BUFFER } from './constants'

interface StatusFooterProps {
  filteredCount: number
  totalCount: number
}

export function StatusFooter({ filteredCount, totalCount }: StatusFooterProps) {
  return (
    <div className="flex items-center justify-between text-xs text-muted-foreground">
      <span>
        Showing {filteredCount} {filteredCount === 1 ? 'log' : 'logs'}
        {totalCount !== filteredCount && ` of ${totalCount} total`}
      </span>
      <span>Max buffer: {MAX_LOG_BUFFER} lines</span>
    </div>
  )
}
