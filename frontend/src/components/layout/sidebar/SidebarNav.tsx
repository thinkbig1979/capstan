import { Link, useLocation } from 'react-router'
import { LayoutDashboard, Server, Settings, type LucideIcon } from 'lucide-react'
import { useHostReclaimable } from '@/hooks/useHostSpace'
import { formatBytes } from '@/lib/format'
import { isHostPath } from './constants'

interface NavItemProps {
  to: string
  label: string
  icon: LucideIcon
  active: boolean
  testId?: string
  badge?: string
}

function NavItem({ to, label, icon: Icon, active, testId, badge }: NavItemProps) {
  return (
    <Link
      to={to}
      data-testid={testId}
      aria-current={active ? 'page' : undefined}
      // With a badge, name it "<label>, <n> unused", not "<label><n>": the bare
      // size would read as total disk use.
      aria-label={badge ? `${label}, ${badge} unused` : undefined}
      // 44 px rows below lg, where this list is the mobile overlay menu.
      className={`flex min-h-[44px] items-center gap-2.5 rounded-md px-2.5 text-sm transition-colors lg:min-h-9 ${
        active
          ? 'bg-sidebar-accent font-medium text-sidebar-accent-foreground'
          : 'text-sidebar-foreground/80 hover:bg-sidebar-accent/60 hover:text-sidebar-foreground'
      }`}
    >
      <Icon className="h-4 w-4 shrink-0" aria-hidden="true" />
      <span className="truncate">{label}</span>
      {badge && (
        <span
          data-testid={`${testId}-badge`}
          title={`${badge} of unused Docker data can be freed`}
          className="ml-auto shrink-0 rounded-full bg-warning/15 px-1.5 py-px font-mono text-[10.5px] tabular-nums text-foreground"
        >
          {badge}
        </span>
      )}
    </Link>
  )
}

/**
 * The app's main destinations, above the stack list, so the Docker host page
 * is reachable from the menu people already use (agent-os-hzxs).
 */
export function SidebarNav() {
  const { pathname } = useLocation()
  const reclaimable = useHostReclaimable()

  return (
    <nav aria-label="Main" className="space-y-0.5 border-b border-sidebar-border px-2 py-2">
      <NavItem to="/" label="Dashboard" icon={LayoutDashboard} active={pathname === '/'} />
      <NavItem
        to="/host"
        label="Docker host"
        icon={Server}
        active={isHostPath(pathname)}
        testId="sidebar-nav-host"
        badge={reclaimable ? formatBytes(reclaimable) : undefined}
      />
      <NavItem to="/settings" label="Settings" icon={Settings} active={pathname.startsWith('/settings')} />
    </nav>
  )
}
