import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { Status as StatusPill, StatusDot, type StatusTone } from '@/components/ui/status'
import { cn } from '@/lib/utils'

export type Status = 'running' | 'stopped' | 'partial' | 'unknown'

const statusConfig: Record<Status, { label: string; tone: StatusTone }> = {
  running: { label: 'Running', tone: 'success' },
  stopped: { label: 'Stopped', tone: 'error' },
  partial: { label: 'Partial', tone: 'warning' },
  unknown: { label: 'Unknown', tone: 'neutral' },
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
          <StatusPill tone={config.tone} className={cn(className)}>
            {status === 'running' && (
              <StatusDot tone="success" pulse={pulse} className="mr-1.5" />
            )}
            {config.label}
          </StatusPill>
        </TooltipTrigger>
        <TooltipContent>
          <p>Stack is {config.label.toLowerCase()}</p>
        </TooltipContent>
      </Tooltip>
    </TooltipProvider>
  )
}
