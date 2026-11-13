import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router'
import { dashboardApi } from '@/lib/api'
import { queryKeys } from '@/lib/query-keys'
import { useDashboardMetricsContext } from '@/hooks/DashboardMetricsContext'
import { atLeastBytes, formatBytes } from '@/lib/format'
import { useHostReclaimable, useHostSummary } from '@/hooks/useHostSpace'

function Gauge({ percent, hot }: { percent: number; hot: boolean }) {
  const width = Math.max(0, Math.min(100, percent))
  return (
    <span className="inline-block h-[5px] w-11 overflow-hidden rounded-full bg-muted" aria-hidden="true">
      <span
        className={`block h-full rounded-full ${hot ? 'bg-warning' : 'bg-success'}`}
        style={{ width: `${width}%` }}
      />
    </span>
  )
}

function Vital({ label, percent, value, title }: { label: string; percent?: number; value: string; title?: string }) {
  return (
    <span className="flex items-center gap-1.5" title={title} data-testid={`header-vital-${label}`}>
      <span className="text-[9.5px] font-semibold uppercase tracking-widest text-muted-foreground">
        {label}
      </span>
      {percent !== undefined && <Gauge percent={percent} hot={percent >= 80} />}
      <span className="font-mono text-[11.5px] tabular-nums text-foreground">{value}</span>
    </span>
  )
}

/**
 * Host vitals (CPU / MEM / DISK) inline in the sticky command bar, fed by the
 * shared dashboard-metrics WebSocket and the dashboard stats query. DISK links
 * to the Docker host page and carries what can be freed (agent-os-hzxs).
 */
export function HeaderVitals() {
  const { aggregates, isConnected } = useDashboardMetricsContext()
  const { data: stats } = useQuery({
    queryKey: queryKeys.dashboardStats(),
    queryFn: dashboardApi.stats,
    refetchInterval: 60000,
  })

  const { data: summary } = useHostSummary()
  const reclaimable = useHostReclaimable()

  if (!isConnected && !stats) return null

  // AC15: an unmeasured volume size is named as unknown, never shown as 0.
  const diskTitle = [
    'Docker host: open the overview',
    reclaimable ? `${formatBytes(reclaimable)} unused can be freed` : undefined,
    summary?.disk.volumesSizeKnown === false ? 'Volumes: size unknown' : undefined,
  ]
    .filter(Boolean)
    .join('. ')

  return (
    <div className="hidden lg:flex items-center gap-4 mr-2" data-testid="header-vitals">
      {isConnected && (
        <>
          <Vital
            label="cpu"
            title="Host cpu"
            percent={aggregates.totalCpuPercent}
            value={`${aggregates.totalCpuPercent.toFixed(0)}%`}
          />
          <Vital
            label="mem"
            title="Host mem"
            percent={aggregates.totalMemPercent}
            value={formatBytes(aggregates.totalMemUsage)}
          />
        </>
      )}
      {/* agent-os-p9e1: diskUsage is omitted when Docker could not report it. */}
      {stats && (
        <Link
          to="/host"
          data-testid="header-disk-link"
          title={diskTitle}
          className="-mx-1.5 flex items-center gap-1.5 rounded-md px-1.5 py-0.5 transition-colors hover:bg-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
        >
          <Vital
            label="disk"
            value={
              stats.diskUsage
                ? atLeastBytes(stats.diskUsage.total, stats.diskUsage.volumesSizeKnown && stats.diskUsage.containersSizeKnown)
                : 'unavailable'
            }
          />
          {!!reclaimable && (
            <span
              data-testid="header-disk-unused"
              className="rounded-full bg-warning/15 px-1.5 py-px font-mono text-[10.5px] tabular-nums text-foreground"
            >
              {formatBytes(reclaimable)} unused
            </span>
          )}
        </Link>
      )}
    </div>
  )
}
