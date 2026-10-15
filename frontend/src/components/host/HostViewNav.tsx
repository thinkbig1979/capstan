import { NavLink } from 'react-router'
import { cn } from '@/lib/utils'
import { HOST_VIEW_ITEMS } from './host-views'

/** Links between the overview and the five list views of /host. */
export function HostViewNav() {
  const linkClass = ({ isActive }: { isActive: boolean }) =>
    cn(
      'inline-flex items-center rounded-md border px-2.5 py-1.5 transition-colors',
      isActive
        ? 'border-border bg-card text-foreground'
        : 'border-transparent text-muted-foreground hover:bg-card hover:border-border hover:text-foreground',
    )
  return (
    <nav aria-label="Docker host" className="flex flex-wrap items-center gap-1 text-sm" data-testid="host-view-nav">
      <NavLink to="/host" end className={linkClass}>
        Overview
      </NavLink>
      {HOST_VIEW_ITEMS.map((v) => (
        <NavLink key={v.view} to={`/host/${v.view}`} className={linkClass}>
          {v.label}
        </NavLink>
      ))}
    </nav>
  )
}
