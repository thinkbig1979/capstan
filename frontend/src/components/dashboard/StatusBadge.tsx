import { Badge } from '@/components/ui/badge'
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { cn } from '@/lib/utils'

export type Status = 'running' | 'stopped' | 'partial' | 'unknown'

const statusConfig: Record<Status, { label: string; className: string }> = {
  running: {
    label: 'Running',
    className: 'bg-green-500/15 text-green-700 dark:text-green-400 border-green-500/25',
  },
  stopped: {
    label: 'Stopped',
    className: 'bg-red-500/15 text-red-700 dark:text-red-400 border-red-500/25',
  },
  partial: {
    label: 'Partial',
    className: 'bg-yellow-500/15 text-yellow-700 dark:text-yellow-400 border-yellow-500/25',
  },
  unknown: {
    label: 'Unknown',
    className: 'bg-gray-500/15 text-gray-700 dark:text-gray-400 border-gray-500/25',
  },
}

interface StatusBadgeProps {
  status: Status
  pulse?: boolean
  className?: string
}

export function StatusBadge({ status, pulse = true, className }: StatusBadgeProps) {
  const config = statusConfig[status] || statusConfig.unknown

  return (
    <TooltipProvider>
      <Tooltip>
        <TooltipTrigger asChild>
          <Badge
            variant="outline"
            className={cn(
              config.className,
              className,
            )}
          >
            {status === 'running' && (
              <span className={cn(
                'mr-1.5 inline-block h-2 w-2 rounded-full bg-green-500',
                pulse && 'animate-pulse',
              )} />
            )}
            {config.label}
          </Badge>
        </TooltipTrigger>
        <TooltipContent>
          <p>Stack is {config.label.toLowerCase()}</p>
        </TooltipContent>
      </Tooltip>
    </TooltipProvider>
  )
}
