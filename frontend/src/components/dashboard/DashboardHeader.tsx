import { Button } from '@/components/ui/button'
import { RefreshCw, Plus } from 'lucide-react'

interface DashboardHeaderProps {
  onRefresh: () => void
  onCreateStack: () => void
  isRefreshing: boolean
}

export function DashboardHeader({ onRefresh, onCreateStack, isRefreshing }: DashboardHeaderProps) {
  return (
    <div className="flex items-center justify-between">
      <div>
        <h1 className="text-3xl font-bold tracking-tight">Dashboard</h1>
        <p className="text-muted-foreground">Welcome to Docker Manager</p>
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
