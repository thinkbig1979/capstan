import { Button } from '@/components/ui/button'
import { RefreshCw, Plus } from 'lucide-react'

interface DashboardHeaderProps {
  onRefresh: () => void
  onCreateStack: () => void
  isRefreshing: boolean
  /** One-line state summary (e.g. "25 stacks · 3 running · 24 containers"). */
  subtitle?: string
}

export function DashboardHeader({ onRefresh, onCreateStack, isRefreshing, subtitle }: DashboardHeaderProps) {
  return (
    <div className="flex items-center justify-between">
      {/* The page identity lives in the header breadcrumb; this row only
          carries the fleet summary and actions. */}
      <div className="min-w-0">
        {subtitle && (
          <p className="text-sm text-muted-foreground font-mono tabular-nums truncate">{subtitle}</p>
        )}
      </div>
      <div className="flex items-center gap-2">
        <Button variant="outline" size="icon" onClick={onRefresh} disabled={isRefreshing} aria-label="Refresh dashboard">
          <RefreshCw className={`h-4 w-4 ${isRefreshing ? 'animate-spin' : ''}`} />
        </Button>
        <Button onClick={onCreateStack} aria-label="Create new stack">
          <Plus className="mr-2 h-4 w-4" />
          New Stack
        </Button>
      </div>
    </div>
  )
}
