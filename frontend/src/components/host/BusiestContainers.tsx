import { Suspense, lazy } from 'react'
import { Link } from 'react-router'
import { Skeleton } from '@/components/ui/skeleton'
import type { ContainerMetricHistory } from '@/hooks/useMetricsBase'
import { formatBytes } from '@/lib/format'
import { busiestByCpu, colorForThreshold } from './live-metrics'
import { hostViewHref } from './host-views'

// Lazy: recharts is only needed once the rows render.
const Sparkline = lazy(() => import('@/components/dashboard/Sparkline').then((m) => ({ default: m.Sparkline })))

const LIMIT = 5

function SparklineFallback() {
  return <Skeleton style={{ width: 60, height: 24 }} />
}

// Each row is exactly h-10 (border-box, so the 1px divider is inside it) and
// the list has a 1px border top and bottom: five rows are 5 x 40 + 2 px.
const ROW_CLASS = 'flex h-10 items-center gap-3 px-3 text-sm'
const RESERVE_CLASS = 'min-h-[202px]'

/**
 * The five containers using the most CPU, with CPU and memory trend lines
 * (agent-os-mi4r; the full list is the Containers view). The list reserves
 * the height of five rows, so arriving metrics do not move what sits below it.
 * While the metrics socket is down the hook keeps the last figures, so they
 * are hidden rather than shown as live.
 */
export function BusiestContainers({
  containers,
  isConnected,
  total,
}: {
  containers: ContainerMetricHistory[]
  isConnected: boolean
  /** Every container on the host, from the host summary; undefined when it is unreadable. */
  total?: number
}) {
  const rows = busiestByCpu(containers, LIMIT)

  return (
    <section aria-labelledby="busiest-containers-heading" className="space-y-3 rounded-lg border bg-card p-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-baseline gap-2">
          <h2 id="busiest-containers-heading" className="text-sm font-semibold">
            Busiest containers
          </h2>
          <span className="text-xs text-muted-foreground">top {LIMIT} by CPU</span>
        </div>
        <Link
          to={hostViewHref('containers')}
          className="inline-flex min-h-[44px] items-center text-sm font-medium text-primary underline underline-offset-4 sm:min-h-0"
        >
          {total === undefined ? 'All containers' : `All ${total} containers`} →
        </Link>
      </div>
      <div className={RESERVE_CLASS} data-testid="busiest-containers-list">
        {!isConnected ? (
          <p className="py-4 text-center text-sm text-muted-foreground">Connecting to live metrics...</p>
        ) : rows.length === 0 ? (
          <p className="py-4 text-center text-sm text-muted-foreground">No live container metrics yet. Waiting for data...</p>
        ) : (
          <div className="divide-y divide-border rounded-md border">
            {rows.map((container) => {
              const latest = container.metrics[container.metrics.length - 1]
              const cpuPct = latest?.cpuPercent ?? 0
              const memPct = latest?.memPercent ?? 0
              return (
                <div
                  key={container.containerId}
                  className={ROW_CLASS}
                  data-testid="busiest-container-row"
                >
                  <span className="min-w-0 flex-1 truncate font-medium" title={container.name}>
                    {container.name}
                  </span>
                  <div className="flex w-28 shrink-0 items-center gap-1.5">
                    <Suspense fallback={<SparklineFallback />}>
                      <Sparkline series={container.metrics.map((m) => m.cpuPercent)} thresholdPercent={cpuPct} width={60} height={24} />
                    </Suspense>
                    <span className={`w-12 text-right text-xs tabular-nums ${colorForThreshold(cpuPct)}`}>{cpuPct.toFixed(1)}%</span>
                  </div>
                  <div className="flex w-36 shrink-0 items-center gap-1.5">
                    <Suspense fallback={<SparklineFallback />}>
                      <Sparkline series={container.metrics.map((m) => m.memPercent)} thresholdPercent={memPct} width={60} height={24} />
                    </Suspense>
                    <span className={`text-right text-xs tabular-nums ${colorForThreshold(memPct)}`}>
                      {formatBytes(latest?.memUsage ?? 0)}
                    </span>
                  </div>
                </div>
              )
            })}
          </div>
        )}
      </div>
    </section>
  )
}
