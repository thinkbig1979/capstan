import { useQuery } from '@tanstack/react-query'
import { dashboardApi } from '@/lib/api'
import { queryKeys } from '@/lib/query-keys'
import { useDashboardMetricsContext } from '@/hooks/DashboardMetricsContext'
import { formatBytes } from '@/lib/format'

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

function Vital({ label, percent, value }: { label: string; percent?: number; value: string }) {
  return (
    <span className="flex items-center gap-1.5" title={`Host ${label}`}>
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
 * shared dashboard-metrics WebSocket and the dashboard stats query.
 */
export function HeaderVitals() {
  const { aggregates, isConnected } = useDashboardMetricsContext()
  const { data: stats } = useQuery({
    queryKey: queryKeys.dashboardStats(),
    queryFn: dashboardApi.stats,
    refetchInterval: 60000,
  })

  if (!isConnected && !stats) return null

  return (
    <div className="hidden lg:flex items-center gap-4 mr-2" data-testid="header-vitals">
      {isConnected && (
        <>
          <Vital
            label="cpu"
            percent={aggregates.totalCpuPercent}
            value={`${aggregates.totalCpuPercent.toFixed(0)}%`}
          />
          <Vital
            label="mem"
            percent={aggregates.totalMemPercent}
            value={formatBytes(aggregates.totalMemUsage)}
          />
        </>
      )}
      {stats && <Vital label="disk" value={formatBytes(stats.diskUsage?.total ?? 0)} />}
    </div>
  )
}
