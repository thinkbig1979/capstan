import { Link, useLocation } from 'react-router-dom'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Boxes, LayoutDashboard, PanelLeftOpen, Settings } from 'lucide-react'

interface CollapsedRailProps {
  stackCount: number
  updateCount: number
  onToggleSidebar: () => void
}

export function CollapsedRail({ stackCount, updateCount, onToggleSidebar }: CollapsedRailProps) {
  const location = useLocation()
  const isDashboard = location.pathname === '/'
  const isSettings = location.pathname.startsWith('/settings')

  return (
    <aside
      aria-label="Collapsed navigation rail"
      className="hidden md:flex flex-col items-center w-14 shrink-0 border-r border-sidebar-border bg-sidebar text-sidebar-foreground py-2 gap-1"
    >
      <Button
        variant="ghost"
        size="icon"
        onClick={onToggleSidebar}
        className="h-10 w-10"
        aria-label="Expand sidebar"
        title="Expand sidebar"
      >
        <PanelLeftOpen className="h-5 w-5" />
      </Button>
      <div className="h-px w-8 bg-sidebar-border my-1" aria-hidden="true" />
      <Button
        asChild
        variant="ghost"
        size="icon"
        className={`h-10 w-10 ${
          isDashboard
            ? 'bg-sidebar-accent text-sidebar-accent-foreground'
            : ''
        }`}
        aria-label="Dashboard"
        aria-current={isDashboard ? 'page' : undefined}
        title="Dashboard"
      >
        <Link to="/">
          <LayoutDashboard className="h-5 w-5" />
        </Link>
      </Button>
      <Button
        variant="ghost"
        size="icon"
        onClick={onToggleSidebar}
        className="h-10 w-10 relative"
        aria-label={stackCount > 0 ? `Stacks (${stackCount})` : 'Stacks'}
        title={
          stackCount > 0
            ? `Stacks (${stackCount}) — click to expand`
            : 'Stacks — click to expand'
        }
      >
        <Boxes className="h-5 w-5" />
        {stackCount > 0 && (
          <Badge
            variant="secondary"
            className="absolute -top-0.5 -right-0.5 h-4 min-w-4 px-1 text-[9px] leading-none tabular-nums pointer-events-none"
          >
            {stackCount > 99 ? '99+' : stackCount}
          </Badge>
        )}
        {updateCount > 0 && (
          <span
            className="absolute -top-0.5 -left-0.5 h-2 w-2 rounded-full bg-amber-500 pointer-events-none"
            aria-hidden="true"
          />
        )}
      </Button>
      <div className="flex-1" aria-hidden="true" />
      <Button
        asChild
        variant="ghost"
        size="icon"
        className={`h-10 w-10 ${
          isSettings
            ? 'bg-sidebar-accent text-sidebar-accent-foreground'
            : ''
        }`}
        aria-label="Settings"
        aria-current={isSettings ? 'page' : undefined}
        title="Settings"
      >
        <Link to="/settings">
          <Settings className="h-5 w-5" />
        </Link>
      </Button>
    </aside>
  )
}
