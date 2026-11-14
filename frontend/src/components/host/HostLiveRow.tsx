import { Suspense, lazy, type ReactNode } from 'react'
import { Skeleton } from '@/components/ui/skeleton'
import type { DashboardStats } from '@/types'
import type { DashboardAggregateMetrics } from '@/hooks/useDashboardMetrics'
import type { ContainerMetricHistory } from '@/hooks/useMetricsBase'
import { formatBytes } from '@/lib/format'
import { colorForThreshold, cpuTotalSeries } from './live-metrics'

// Lazy: recharts is only needed once a trend line renders.
const Sparkline = lazy(() => import('@/components/dashboard/Sparkline').then((m) => ({ default: m.Sparkline })))

const NO_VALUE = '—'

function LiveTile({ label, value, sub, children }: { label: string; value: ReactNode; sub?: ReactNode; children?: ReactNode }) {
  return (
    <div data-testid={`host-live-${label.toLowerCase()}`} className="flex min-h-[104px] flex-col gap-1 rounded-lg border bg-card p-4">
      <span className="text-label">{label}</span>
      <span className="font-mono text-xl font-semibold tabular-nums">{value}</span>
      {sub && <span className="text-xs text-muted-foreground">{sub}</span>}
      {children}
    </div>
  )
}

interface HostLiveRowProps {
  stats: DashboardStats | undefined
  aggregates: DashboardAggregateMetrics
  isConnected: boolean
  containers: ContainerMetricHistory[]
}

/**
 * CPU, memory, network and processes from the dashboard-metrics WebSocket
 * (the former Metrics tab's Performance card, agent-os-mi4r). The four tiles
 * always render, with a dash until the socket connects, so the row never
 * changes height under the Automatic cleanup anchor.
 */
export function HostLiveRow({ stats, aggregates, isConnected, containers }: HostLiveRowProps) {
  // agent-os-p9e1: the server omits runningContainers when Docker could not answer.
  const hostCountUnavailable = stats?.runningContainers === undefined
  const cpuSeries = cpuTotalSeries(containers)

  return (
    <section aria-label="Live" className="space-y-2">
      <div className="flex items-center gap-2" role="status">
        <span className="text-label">Live</span>
        <span
          className={`h-2 w-2 rounded-full ${isConnected ? 'bg-success animate-pulse' : 'bg-muted-foreground'}`}
          aria-hidden="true"
        />
        <span className="text-xs text-muted-foreground">{isConnected ? 'Updates every few seconds' : 'Connecting...'}</span>
      </div>
      <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
        <LiveTile
          label="CPU"
          value={
            isConnected ? (
              <span className={colorForThreshold(aggregates.totalCpuPercent)}>{aggregates.totalCpuPercent.toFixed(1)}%</span>
            ) : (
              NO_VALUE
            )
          }
        >
          {isConnected && cpuSeries.length > 1 && (
            <div data-testid="host-live-cpu-trend" data-last={cpuSeries[cpuSeries.length - 1]}>
              <Suspense fallback={<Skeleton className="h-7 w-full" />}>
                <Sparkline series={cpuSeries} thresholdPercent={aggregates.totalCpuPercent} width="100%" height={28} />
              </Suspense>
            </div>
          )}
        </LiveTile>
        <LiveTile
          label="Memory"
          value={
            isConnected ? (
              <span className={colorForThreshold(aggregates.totalMemPercent)}>
                {formatBytes(aggregates.totalMemUsage)}
                <span className="text-sm font-normal text-muted-foreground"> of {formatBytes(aggregates.totalMemLimit)}</span>
              </span>
            ) : (
              NO_VALUE
            )
          }
          sub={isConnected ? `${aggregates.totalMemPercent.toFixed(0)}% · swap ${formatBytes(aggregates.totalSwap)}` : undefined}
        />
        <LiveTile
          label="Network"
          value={isConnected ? `↓ ${formatBytes(aggregates.totalNetRx)}` : NO_VALUE}
          sub={isConnected ? `↑ ${formatBytes(aggregates.totalNetTx)} since start` : undefined}
        />
        <LiveTile
          label="Processes"
          value={isConnected ? String(aggregates.totalPids) : NO_VALUE}
          sub={
            isConnected && !hostCountUnavailable
              ? `across ${stats?.runningContainers} running containers`
              : undefined
          }
        />
      </div>
    </section>
  )
}
