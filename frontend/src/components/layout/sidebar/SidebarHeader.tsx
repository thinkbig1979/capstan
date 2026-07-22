import { Link } from 'react-router-dom'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import {
  ArrowUpCircle,
  ArrowUpDown,
  Boxes,
  ListChecks,
  PanelLeftClose,
  Search,
  X,
} from 'lucide-react'
import type { StackStatus } from '@/types'

interface SidebarHeaderProps {
  stackCount: number
  updateCount: number
  selecting: boolean
  onToggleSelecting: () => void
  onCollapseSidebar: () => void
  searchQuery: string
  onSearchChange: (value: string) => void
  statusFilter: StackStatus | 'all'
  onStatusFilterChange: (status: StackStatus | 'all') => void
  sortBy: 'name' | 'status'
  onToggleSort: () => void
}

export function SidebarHeader({
  stackCount,
  updateCount,
  selecting,
  onToggleSelecting,
  onCollapseSidebar,
  searchQuery,
  onSearchChange,
  statusFilter,
  onStatusFilterChange,
  sortBy,
  onToggleSort,
}: SidebarHeaderProps) {
  return (
    <div className="p-3 border-b space-y-2">
      <div className="flex items-center justify-between">
        <h2 className="text-sm font-semibold flex items-center gap-1.5">
          <Boxes className="h-4 w-4" />
          Stacks
          <span className="text-muted-foreground font-normal">
            ({stackCount})
          </span>
        </h2>
        <div className="flex items-center gap-0.5">
          {updateCount > 0 && (
            <Link
              to="/?tab=updates"
              title={`${updateCount} update${updateCount > 1 ? 's' : ''} available`}
            >
              <Badge className="h-5 gap-1 bg-amber-500 text-white border-transparent hover:bg-amber-600 px-1.5 text-[10px]">
                <ArrowUpCircle className="h-3 w-3" />
                {updateCount}
              </Badge>
            </Link>
          )}
          <Button
            variant={selecting ? 'secondary' : 'ghost'}
            size="icon"
            className="h-6 w-6"
            onClick={onToggleSelecting}
            aria-label={selecting ? 'Exit selection' : 'Select stacks'}
            aria-pressed={selecting}
            title={selecting ? 'Exit selection' : 'Select multiple stacks'}
          >
            <ListChecks className="h-3.5 w-3.5" />
          </Button>
          <Button
            variant="ghost"
            size="icon"
            className="h-6 w-6"
            onClick={onCollapseSidebar}
            aria-label="Collapse sidebar"
            title="Collapse sidebar"
          >
            <PanelLeftClose className="h-3.5 w-3.5" />
          </Button>
        </div>
      </div>

      <div className="relative">
        <label htmlFor="sidebar-stack-search" className="sr-only">Search stacks</label>
        <Search className="absolute left-2 top-1/2 -translate-y-1/2 h-3.5 w-3.5 text-muted-foreground" />
        <input
          id="sidebar-stack-search"
          type="text"
          placeholder="Search stacks..."
          value={searchQuery}
          onChange={(e) => onSearchChange(e.target.value)}
          className="w-full h-7 pl-7 pr-2 text-xs rounded-md border bg-background focus:outline-hidden focus:ring-1 focus:ring-ring"
        />
        {searchQuery && (
          <button
            type="button"
            onClick={() => onSearchChange('')}
            aria-label="Clear search"
            className="absolute right-1.5 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-sidebar-foreground"
          >
            <X className="h-3 w-3" />
          </button>
        )}
      </div>

      <div className="flex items-center gap-1 flex-wrap">
        {(['all', 'running', 'stopped', 'error'] as const).map((key) => (
          <button
            key={key}
            type="button"
            onClick={() => onStatusFilterChange(key)}
            className={`inline-flex items-center gap-1 h-5 px-1.5 rounded text-[10px] font-medium transition-colors ${
              statusFilter === key
                ? 'bg-primary text-primary-foreground'
                : 'bg-muted text-muted-foreground hover:bg-muted/80'
            }`}
          >
            {key === 'all' && 'All'}
            {key === 'running' && (
              <>
                <span className="h-1.5 w-1.5 rounded-full bg-current" />
                Running
              </>
            )}
            {key === 'stopped' && (
              <>
                <span className="h-1.5 w-1.5 rounded-sm bg-current" />
                Stopped
              </>
            )}
            {key === 'error' && (
              <>
                <span className="h-1.5 w-1.5 rounded-full bg-current" />
                Error
              </>
            )}
          </button>
        ))}
        <div className="flex-1" />
        <button
          type="button"
          onClick={onToggleSort}
          className="inline-flex items-center gap-0.5 h-5 px-1.5 rounded text-[10px] text-muted-foreground hover:bg-muted transition-colors"
          title={`Sort by ${sortBy === 'name' ? 'name' : 'status'}. Click to toggle.`}
        >
          <ArrowUpDown className="h-2.5 w-2.5" />
          {sortBy === 'name' ? 'A-Z' : 'St'}
        </button>
      </div>
    </div>
  )
}
