import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { Status as StatusPill, StatusDot, type StatusTone } from '@/components/ui/status'
import { cn } from '@/lib/utils'

export type Status = 'running' | 'stopped' | 'partial' | 'error' | 'unknown'

const statusConfig: Record<Status, { label: string; tone: StatusTone }> = {
  running: { label: 'Running', tone: 'success' },
  // Stopped is a normal, intentional state — neutral, not red. Red is
  // reserved for actual errors.
  stopped: { label: 'Stopped', tone: 'neutral' },
  partial: { label: 'Partial', tone: 'warning' },
  // "error" means the stack's compose file is missing/unreadable (Capstan can't
  // resolve its state) — distinct from an intentionally stopped stack.
  error: { label: 'Error', tone: 'error' },
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
