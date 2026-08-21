import { formatBytes } from '@/lib/format'
import type { DashboardStats } from '@/types'

export type HostView = 'containers' | 'images' | 'volumes' | 'networks' | 'build-cache'

interface HostStripProps {
  stats: DashboardStats | undefined
  activeView?: HostView
  onNavigate: (view: HostView) => void
}

/**
 * Quiet strip of host-level housekeeping links (containers, images, volumes,
 * networks, build cache), demoted out of the top tab row per the redesign.
 */
export function HostStrip({ stats, activeView, onNavigate }: HostStripProps) {
  const links: { view: HostView; label: string; detail?: string }[] = [
    {
      view: 'containers',
      label: 'Containers',
      detail: stats ? String(stats.totalContainers) : undefined,
    },
    {
      view: 'images',
      label: 'Images',
      detail: stats ? formatBytes(stats.diskUsage?.images ?? 0) : undefined,
    },
    {
      view: 'volumes',
      label: 'Volumes',
      detail: stats ? formatBytes(stats.diskUsage?.volumes ?? 0) : undefined,
    },
    { view: 'networks', label: 'Networks' },
    {
      view: 'build-cache',
      label: 'Build cache',
      detail: stats ? formatBytes(stats.diskUsage?.buildCache ?? 0) : undefined,
    },
  ]

  return (
    <div className="flex flex-wrap items-center gap-1 text-sm" data-testid="host-strip">
      <span className="text-label mr-1.5">Host</span>
      {links.map((l) => (
        <button
          key={l.view}
          type="button"
          onClick={() => onNavigate(l.view)}
          aria-current={activeView === l.view ? 'page' : undefined}
          className={`inline-flex items-center gap-1.5 rounded-md border px-2.5 py-1.5 transition-colors ${
            activeView === l.view
              ? 'border-border bg-card text-foreground'
              : 'border-transparent text-muted-foreground hover:bg-card hover:border-border hover:text-foreground'
          }`}
        >
          {l.label}
          {l.detail !== undefined && (
            <span className="font-mono text-xs text-muted-foreground tabular-nums">{l.detail}</span>
          )}
        </button>
      ))}
    </div>
  )
}
