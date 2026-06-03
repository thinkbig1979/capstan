import { useMemo, useSyncExternalStore } from 'react'
import { AreaChart, Area, YAxis, ResponsiveContainer } from 'recharts'
import { Cpu, MemoryStick, Network, HardDrive, Activity, ArrowDown, ArrowUp } from 'lucide-react'
import { useMetricsBase, type ContainerMetricHistory } from '@/hooks/useMetricsBase'
import { Card, CardContent } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { formatBytes } from '@/lib/format'

interface MetricsPanelProps {
  stackId: string
}

// Shared column template so the table header and every row line up.
const GRID_COLS =
  'minmax(140px,1.6fr) minmax(120px,1fr) minmax(170px,1.6fr) minmax(110px,1fr) minmax(110px,1fr) 56px'

function formatRate(bytesPerSecond: number): string {
  return `${formatBytes(bytesPerSecond)}/s`
}

function thresholdBarClass(percent: number): string {
  if (percent >= 80) return 'bg-destructive'
  if (percent >= 60) return 'bg-warning'
  return 'bg-success'
}

// Resolve the CSS custom property to an actual color string for recharts,
// which sets SVG presentation attributes (where var(--x) is not honoured).
function useThresholdChartColor(percent: number): string {
  // Re-evaluate on dark-mode class flip so the chart redraws after theme change.
  const isDark = useSyncExternalStore(
    (cb) => {
      if (typeof document === 'undefined') return () => {}
      const obs = new MutationObserver(cb)
      obs.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] })
      return () => obs.disconnect()
    },
    () => (typeof document !== 'undefined' ? document.documentElement.classList.contains('dark') : false),
    () => false,
  )
  return useMemo(() => {
    if (typeof window === 'undefined') return '#22c55e'
    const cs = getComputedStyle(document.documentElement)
    const varName = percent >= 80 ? '--destructive' : percent >= 60 ? '--warning' : '--success'
    return cs.getPropertyValue(varName).trim() || '#22c55e'
    // `isDark` is intentionally part of the dep set even though it isn't read directly —
    // it forces the memo to re-compute when the theme class flips.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [percent, isDark])
}

function Sparkline({ data, color }: { data: Array<{ i: number; cpu: number }>; color: string }) {
  return (
    <ResponsiveContainer width="100%" height="100%">
      <AreaChart data={data} margin={{ top: 2, right: 0, bottom: 2, left: 0 }}>
        <YAxis domain={[0, 'auto']} hide />
        <Area
          type="monotone"
          dataKey="cpu"
          stroke={color}
          fill={color}
          fillOpacity={0.2}
          strokeWidth={1.5}
          dot={false}
          isAnimationActive={false}
        />
      </AreaChart>
    </ResponsiveContainer>
  )
}

function ThinBar({ percent }: { percent: number }) {
  return (
    <div className="h-1.5 w-full rounded-full bg-muted">
      <div
        className={`h-full rounded-full transition-all ${thresholdBarClass(percent)}`}
        style={{ width: `${Math.min(Math.max(percent, 0), 100)}%` }}
      />
    </div>
  )
}

function StatTile({
  icon: Icon,
  label,
  children,
}: {
  icon: typeof Cpu
  label: string
  children: React.ReactNode
}) {
  return (
    <Card>
      <CardContent className="space-y-1.5 p-4">
        <div className="flex items-center gap-2 text-xs font-medium uppercase tracking-wide text-muted-foreground">
          <Icon className="h-3.5 w-3.5" />
          {label}
        </div>
        {children}
      </CardContent>
    </Card>
  )
}

function RateStack({ down, up }: { down: number; up: number }) {
  return (
    <div className="flex flex-col gap-0.5 text-xs tabular-nums text-muted-foreground">
      <span className="flex items-center gap-1">
        <ArrowDown className="h-3 w-3 text-muted-foreground/70" />
        {formatRate(down)}
      </span>
      <span className="flex items-center gap-1">
        <ArrowUp className="h-3 w-3 text-muted-foreground/70" />
        {formatRate(up)}
      </span>
    </div>
  )
}

function SummaryBar({
  aggregates,
  totals,
  cpuSeries,
}: {
  aggregates: { totalCpuPercent: number; totalMemUsage: number; totalMemLimit: number; totalMemPercent: number }
  totals: { netRx: number; netTx: number; blockRead: number; blockWrite: number }
  cpuSeries: Array<{ i: number; cpu: number }>
}) {
  const cpuColor = useThresholdChartColor(aggregates.totalCpuPercent)

  return (
    <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
      <StatTile icon={Cpu} label="CPU">
        <div className="text-2xl font-semibold tabular-nums">{aggregates.totalCpuPercent.toFixed(1)}%</div>
        <div className="h-9 w-full">
          <Sparkline data={cpuSeries} color={cpuColor} />
        </div>
      </StatTile>

      <StatTile icon={MemoryStick} label="Memory">
        <div className="text-2xl font-semibold tabular-nums">{formatBytes(aggregates.totalMemUsage)}</div>
        <div className="text-xs text-muted-foreground">
          of {formatBytes(aggregates.totalMemLimit)} · {aggregates.totalMemPercent.toFixed(0)}%
        </div>
        <ThinBar percent={aggregates.totalMemPercent} />
      </StatTile>

      <StatTile icon={Network} label="Network">
        <div className="space-y-1 pt-0.5 text-base font-medium tabular-nums">
          <div className="flex items-center gap-1.5">
            <ArrowDown className="h-3.5 w-3.5 text-muted-foreground" />
            {formatRate(totals.netRx)}
          </div>
          <div className="flex items-center gap-1.5">
            <ArrowUp className="h-3.5 w-3.5 text-muted-foreground" />
            {formatRate(totals.netTx)}
          </div>
        </div>
      </StatTile>

      <StatTile icon={HardDrive} label="Disk I/O">
        <div className="space-y-1 pt-0.5 text-base font-medium tabular-nums">
          <div className="flex items-center gap-1.5">
            <ArrowDown className="h-3.5 w-3.5 text-muted-foreground" />
            {formatRate(totals.blockRead)}
          </div>
          <div className="flex items-center gap-1.5">
            <ArrowUp className="h-3.5 w-3.5 text-muted-foreground" />
            {formatRate(totals.blockWrite)}
          </div>
        </div>
      </StatTile>
    </div>
  )
}

function ContainerRow({ container }: { container: ContainerMetricHistory }) {
  const latest = container.metrics[container.metrics.length - 1]
  const cpuColor = useThresholdChartColor(latest?.cpuPercent ?? 0)

  const sparkData = useMemo(
    () => container.metrics.map((m, i) => ({ i, cpu: m.cpuPercent })),
    [container.metrics],
  )

  if (!latest) {
    return (
      <div
        className="grid items-center gap-4 border-t border-border/50 px-3 py-2.5 text-sm"
        style={{ gridTemplateColumns: GRID_COLS }}
      >
        <div className="flex items-center gap-2 min-w-0">
          <span className="h-2 w-2 shrink-0 rounded-full bg-muted-foreground/40" />
          <span className="truncate font-medium">{container.name}</span>
        </div>
        <div className="col-span-5 text-xs text-muted-foreground">Waiting for metrics…</div>
      </div>
    )
  }

  return (
    <div
      className="grid items-center gap-4 border-t border-border/50 px-3 py-2.5 text-sm transition-colors hover:bg-muted/40"
      style={{ gridTemplateColumns: GRID_COLS }}
    >
      <div className="flex items-center gap-2 min-w-0">
        <span className="h-2 w-2 shrink-0 rounded-full bg-success" />
        <span className="truncate font-medium">{container.name}</span>
      </div>

      <div className="flex items-center gap-2">
        <div className="h-7 w-16 shrink-0">
          <Sparkline data={sparkData} color={cpuColor} />
        </div>
        <span className="tabular-nums text-muted-foreground">{latest.cpuPercent.toFixed(1)}%</span>
      </div>

      <div className="flex items-center gap-2 min-w-0">
        <div className="w-16 shrink-0">
          <ThinBar percent={latest.memPercent} />
        </div>
        <span className="truncate text-xs tabular-nums text-muted-foreground">
          {formatBytes(latest.memUsage)} / {formatBytes(latest.memLimit)}
          {latest.memSwap > 0 && (
            <span className="text-muted-foreground/70"> · swap {formatBytes(latest.memSwap)}</span>
          )}
        </span>
      </div>

      <RateStack down={latest.netRx} up={latest.netTx} />
      <RateStack down={latest.blockRead} up={latest.blockWrite} />

      <div className="text-right tabular-nums text-muted-foreground">{latest.pids}</div>
    </div>
  )
}

function ContainerTable({ containers }: { containers: ContainerMetricHistory[] }) {
  return (
    <Card>
      <div className="overflow-x-auto">
        <div className="min-w-[700px]">
          <div
            className="grid items-center gap-4 px-3 py-2 text-xs font-medium uppercase tracking-wide text-muted-foreground"
            style={{ gridTemplateColumns: GRID_COLS }}
          >
            <span>Container</span>
            <span>CPU</span>
            <span>Memory</span>
            <span>Network</span>
            <span>Disk I/O</span>
            <span className="text-right">PIDs</span>
          </div>
          {containers.map((container) => (
            <ContainerRow key={container.containerId} container={container} />
          ))}
        </div>
      </div>
    </Card>
  )
}

export function MetricsPanel({ stackId }: MetricsPanelProps) {
  const { containers, baseAggregates: aggregates, isConnected, ws } = useMetricsBase(`/ws/metrics/${stackId}`)

  // Stack-wide throughput totals (rates are per-container, sum the latest sample).
  const totals = useMemo(() => {
    let netRx = 0,
      netTx = 0,
      blockRead = 0,
      blockWrite = 0
    for (const c of containers) {
      const m = c.metrics[c.metrics.length - 1]
      if (!m) continue
      netRx += m.netRx
      netTx += m.netTx
      blockRead += m.blockRead
      blockWrite += m.blockWrite
    }
    return { netRx, netTx, blockRead, blockWrite }
  }, [containers])

  // Aggregate CPU series for the summary sparkline: sum each container's
  // sample at the same age (counting back from the most recent frame).
  const cpuSeries = useMemo(() => {
    const maxLen = containers.reduce((n, c) => Math.max(n, c.metrics.length), 0)
    const series: Array<{ i: number; cpu: number }> = []
    for (let pos = maxLen - 1; pos >= 0; pos--) {
      let sum = 0
      for (const c of containers) {
        const idx = c.metrics.length - 1 - pos
        if (idx >= 0) sum += c.metrics[idx].cpuPercent
      }
      series.push({ i: maxLen - 1 - pos, cpu: sum })
    }
    return series
  }, [containers])

  if (!isConnected && ws.status === 'connecting') {
    return (
      <div className="space-y-4">
        <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
          {[1, 2, 3, 4].map((i) => (
            <Card key={i}>
              <CardContent className="space-y-2 p-4">
                <Skeleton className="h-3 w-16" />
                <Skeleton className="h-7 w-20" />
                <Skeleton className="h-2 w-full" />
              </CardContent>
            </Card>
          ))}
        </div>
        <Card>
          <CardContent className="space-y-3 p-4">
            {[1, 2, 3].map((i) => (
              <Skeleton key={i} className="h-8 w-full" />
            ))}
          </CardContent>
        </Card>
      </div>
    )
  }

  if (!isConnected && ws.status === 'disconnected') {
    return (
      <Card>
        <CardContent className="flex flex-col items-center justify-center py-12">
          <Activity className="mb-4 h-12 w-12 text-muted-foreground" />
          <p className="mb-2 text-lg font-semibold">Connection Lost</p>
          <p className="mb-4 text-sm text-muted-foreground">
            Unable to connect to metrics stream. Attempting to reconnect...
          </p>
          {ws.reconnectAttempts > 0 && (
            <p className="text-xs text-muted-foreground">
              Reconnect attempt {ws.reconnectAttempts}/5
            </p>
          )}
        </CardContent>
      </Card>
    )
  }

  if (containers.length === 0) {
    return (
      <Card>
        <CardContent className="flex flex-col items-center justify-center py-12">
          <Activity className="mb-4 h-12 w-12 text-muted-foreground" />
          <p className="text-lg font-semibold">No Running Containers</p>
          <p className="text-sm text-muted-foreground">
            Start containers to view real-time metrics
          </p>
        </CardContent>
      </Card>
    )
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h3 className="text-lg font-semibold">Live Metrics</h3>
        <span className="text-xs text-muted-foreground">Live since page opened, not stored</span>
      </div>
      <SummaryBar aggregates={aggregates} totals={totals} cpuSeries={cpuSeries} />
      <ContainerTable containers={containers} />
    </div>
  )
}
