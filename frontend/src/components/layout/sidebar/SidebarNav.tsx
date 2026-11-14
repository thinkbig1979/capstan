import { Link, useLocation } from 'react-router'
import { LayoutDashboard, Settings, type LucideIcon } from 'lucide-react'

interface NavItemProps {
  to: string
  label: string
  icon: LucideIcon
  active: boolean
}

function NavItem({ to, label, icon: Icon, active }: NavItemProps) {
  return (
    <Link
      to={to}
      aria-current={active ? 'page' : undefined}
      // 44 px rows below lg, where this list is the mobile overlay menu.
      className={`flex min-h-[44px] items-center gap-2.5 rounded-md px-2.5 text-sm transition-colors lg:min-h-9 ${
        active
          ? 'bg-sidebar-accent font-medium text-sidebar-accent-foreground'
          : 'text-sidebar-foreground/80 hover:bg-sidebar-accent/60 hover:text-sidebar-foreground'
      }`}
    >
      <Icon className="h-4 w-4 shrink-0" aria-hidden="true" />
      <span className="truncate">{label}</span>
    </Link>
  )
}

/**
 * The app's main destinations, above the stack list. The Docker host is the
 * dashboard's Host tab (agent-os-mi4r), so it has no entry of its own here.
 */
export function SidebarNav() {
  const { pathname } = useLocation()

  return (
    <nav aria-label="Main" className="space-y-0.5 border-b border-sidebar-border px-2 py-2">
      <NavItem to="/" label="Dashboard" icon={LayoutDashboard} active={pathname === '/'} />
      <NavItem to="/settings" label="Settings" icon={Settings} active={pathname.startsWith('/settings')} />
    </nav>
  )
}
