import { useState, useMemo, Suspense, lazy } from 'react'
import { Activity, Cpu, HardDrive, MemoryStick, Network, Database, Layers, Archive, Box, Hash, Folder, Server, ArrowUpDown } from 'lucide-react'
import { Card, CardAction, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Skeleton } from '@/components/ui/skeleton'
import { Button } from '@/components/ui/button'
import type { DashboardStats } from '@/types'
import type { DashboardAggregateMetrics } from '@/hooks/useDashboardMetrics'
import type { ContainerMetricHistory } from '@/hooks/useMetricsBase'
import { formatBytes } from '@/lib/format'

// Lazy: recharts is only needed once the container rows actually render, so
// keeping it off DashboardMetricsTab's static import graph lets the rest of
// this (default) tab paint without waiting on recharts to parse.
const Sparkline = lazy(() =>
  import('@/components/dashboard/Sparkline').then((m) => ({ default: m.Sparkline })),
)

function SparklineFallback({ width, height }: { width: number; height: number }) {
  return <Skeleton style={{ width, height }} />
}

function getColorForThreshold(percent: number): string {
  if (percent >= 80) return 'text-destructive'
  if (percent >= 60) return 'text-warning'
  return 'text-success'
}

type ContainerSortKey = 'cpu' | 'mem'
type SortDir = 'desc' | 'asc'

interface ContainerSparklineListProps {
  containers: ContainerMetricHistory[]
}

function ContainerSparklineList({ containers }: ContainerSparklineListProps) {
  const [sortKey, setSortKey] = useState<ContainerSortKey>('cpu')
  const [sortDir, setSortDir] = useState<SortDir>('desc')

  function handleSort(key: ContainerSortKey) {
    if (sortKey === key) {
      setSortDir((d) => (d === 'desc' ? 'asc' : 'desc'))
    } else {
      setSortKey(key)
      setSortDir('desc')
    }
  }

  const sorted = useMemo(() => {
    if (containers.length === 0) return []
    return [...containers].sort((a, b) => {
      const latest = (c: ContainerMetricHistory) => c.metrics[c.metrics.length - 1]
      const la = latest(a)
      const lb = latest(b)
      const va = sortKey === 'cpu' ? (la?.cpuPercent ?? 0) : (la?.memPercent ?? 0)
      const vb = sortKey === 'cpu' ? (lb?.cpuPercent ?? 0) : (lb?.memPercent ?? 0)
      return sortDir === 'desc' ? vb - va : va - vb
    })
  }, [containers, sortKey, sortDir])

  if (containers.length === 0) {
    return (
      <p className="text-sm text-muted-foreground py-4 text-center">
        No live container metrics yet. Waiting for data...
      </p>
    )
  }

  return (
    <div className="space-y-3">
      {/* Sort controls */}
      <div className="flex items-center gap-2 flex-wrap">
        <ArrowUpDown className="h-4 w-4 text-muted-foreground shrink-0" />
        <span className="text-sm text-muted-foreground shrink-0">Sort:</span>
        <div className="flex gap-1">
          {(['cpu', 'mem'] as ContainerSortKey[]).map((key) => (
            <Button
              key={key}
              variant={sortKey === key ? 'default' : 'ghost'}
              size="sm"
              className="h-7 text-xs"
              onClick={() => handleSort(key)}
              aria-pressed={sortKey === key}
            >
              {key === 'cpu' ? 'CPU%' : 'Mem%'}
              {sortKey === key && (
                <span className="ml-1 text-[10px]">{sortDir === 'desc' ? '↓' : '↑'}</span>
              )}
            </Button>
          ))}
        </div>
        <span className="text-sm text-muted-foreground ml-auto">{containers.length} container{containers.length !== 1 ? 's' : ''}</span>
      </div>

      {/* Container rows */}
      <div className="divide-y divide-border rounded-md border">
        {sorted.map((container) => {
          const latest = container.metrics[container.metrics.length - 1]
          const cpuSeries = container.metrics.map((m) => m.cpuPercent)
          const memSeries = container.metrics.map((m) => m.memPercent)
          const cpuPct = latest?.cpuPercent ?? 0
          const memPct = latest?.memPercent ?? 0
          const memUsage = latest?.memUsage ?? 0

          return (
            <div
              key={container.containerId}
              className="flex items-center gap-3 px-3 py-2 text-sm"
              data-testid="container-sparkline-row"
            >
              {/* Name */}
              <span
                className="truncate font-medium min-w-0 flex-1"
                title={container.name}
              >
                {container.name}
              </span>

              {/* CPU sparkline + value */}
              <div className="flex items-center gap-1.5 shrink-0 w-28">
                <Suspense fallback={<SparklineFallback width={60} height={24} />}>
                  <Sparkline
                    series={cpuSeries}
                    thresholdPercent={cpuPct}
                    width={60}
                    height={24}
                  />
                </Suspense>
                <span className={`text-xs tabular-nums w-12 text-right ${getColorForThreshold(cpuPct)}`}>
                  {cpuPct.toFixed(1)}%
                </span>
              </div>

              {/* Mem sparkline + value */}
              <div className="flex items-center gap-1.5 shrink-0 w-36">
                <Suspense fallback={<SparklineFallback width={60} height={24} />}>
                  <Sparkline
                    series={memSeries}
                    thresholdPercent={memPct}
                    width={60}
                    height={24}
                  />
                </Suspense>
                <span className={`text-xs tabular-nums text-right ${getColorForThreshold(memPct)}`}>
                  {formatBytes(memUsage)}
                </span>
              </div>
            </div>
          )
        })}
      </div>
    </div>
  )
}

interface DashboardMetricsTabProps {
  stats: DashboardStats | undefined
  aggregates: DashboardAggregateMetrics
  isConnected: boolean
  totalStacks: number
  runningStacks: number
  stoppedStacks: number
  totalContainers: number
  runningContainers: number
  directoryCount: number
  containers: ContainerMetricHistory[]
}

export function DashboardMetricsTab({
  stats,
  aggregates,
  isConnected,
  totalStacks,
  runningStacks,
  stoppedStacks,
  totalContainers,
  runningContainers,
  directoryCount,
  containers,
}: DashboardMetricsTabProps) {
  if (!stats) {
    return (
      <div className="space-y-6">
        <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-4">
          {Array.from({ length: 4 }).map((_, i) => (
            <Card key={i}>
              <CardHeader>
                <Skeleton className="h-4 w-20" />
                <Skeleton className="h-8 w-16" />
              </CardHeader>
            </Card>
          ))}
        </div>
        <div className="grid gap-4 md:grid-cols-2">
          {Array.from({ length: 2 }).map((_, i) => (
            <Card key={i}>
              <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
                <Skeleton className="h-4 w-24" />
                <Skeleton className="h-4 w-4" />
              </CardHeader>
              <CardContent>
                <Skeleton className="h-8 w-20" />
              </CardContent>
            </Card>
          ))}
        </div>
      </div>
    )
  }

  const cpuColor = getColorForThreshold(aggregates.totalCpuPercent)
  const memColor = getColorForThreshold(aggregates.totalMemPercent)

  return (
    <div className="space-y-6">
      <div className="grid grid-cols-1 gap-4 *:data-[slot=card]:bg-linear-to-t *:data-[slot=card]:shadow-xs md:grid-cols-2 lg:grid-cols-4">
        <Card className="@container/card">
          <CardHeader>
            <CardDescription>Total Stacks</CardDescription>
            <CardTitle className="text-2xl font-semibold tabular-nums @[250px]/card:text-3xl">
              {totalStacks}
            </CardTitle>
            <CardAction>
              <Badge variant="outline">
                <Folder className="size-3" />
                {directoryCount} dirs
              </Badge>
            </CardAction>
          </CardHeader>
          <CardFooter className="flex-col items-start gap-1.5 text-sm">
            <div className="line-clamp-1 flex gap-2 font-medium">
              {runningStacks} running stacks
            </div>
            <div className="text-muted-foreground">
              Across {directoryCount} directories
            </div>
          </CardFooter>
        </Card>
        <Card className="@container/card">
          <CardHeader>
            <CardDescription>Running</CardDescription>
            <CardTitle className="text-2xl font-semibold tabular-nums @[250px]/card:text-3xl">
              {runningStacks}
            </CardTitle>
            <CardAction>
              <Badge variant="outline" className="text-success border-success/30">
                Active
              </Badge>
            </CardAction>
          </CardHeader>
          <CardFooter className="flex-col items-start gap-1.5 text-sm">
            <div className="line-clamp-1 flex gap-2 font-medium tabular-nums">
              {runningStacks} of {totalStacks} stacks up
            </div>
            <div className="text-muted-foreground">
              Right now, not uptime over time
            </div>
          </CardFooter>
        </Card>
        <Card className="@container/card">
          <CardHeader>
            <CardDescription>Stopped</CardDescription>
            <CardTitle className="text-2xl font-semibold tabular-nums @[250px]/card:text-3xl">
              {stoppedStacks}
            </CardTitle>
            <CardAction>
              <Badge variant="outline">
                Inactive
              </Badge>
            </CardAction>
          </CardHeader>
          <CardFooter className="flex-col items-start gap-1.5 text-sm">
            <div className="line-clamp-1 flex gap-2 font-medium">
              {stoppedStacks > 0
                ? 'Stopped by choice or by error'
                : totalStacks > 0
                  ? 'All stacks running'
                  : 'No stacks yet'}
            </div>
            <div className="text-muted-foreground">
              Stopped or errored stacks
            </div>
          </CardFooter>
        </Card>
        <Card className="@container/card">
          <CardHeader>
            <CardDescription>Stack Containers</CardDescription>
            <CardTitle className="text-2xl font-semibold tabular-nums @[250px]/card:text-3xl">
              {totalContainers}
            </CardTitle>
            <CardAction>
              <Badge variant="outline">
                <Server className="size-3" />
                {runningContainers} on host
              </Badge>
            </CardAction>
          </CardHeader>
          <CardFooter className="flex-col items-start gap-1.5 text-sm">
            <div className="line-clamp-1 flex gap-2 font-medium">
              Containers in managed stacks
            </div>
            <div className="text-muted-foreground">
              Badge counts all running containers on the host
            </div>
          </CardFooter>
        </Card>
      </div>

      <div className="flex items-center gap-2" role="status">
        {isConnected ? (
          <div className="h-2 w-2 rounded-full bg-success animate-pulse" aria-hidden="true" />
        ) : (
          <div className="h-2 w-2 rounded-full bg-muted-foreground" aria-hidden="true" />
        )}
        <span className="text-xs text-muted-foreground">
          {isConnected ? 'Live metrics' : 'Connecting...'}
        </span>
      </div>

      <div className="grid gap-4 md:grid-cols-2">
        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium">Performance</CardTitle>
            <Cpu className="h-4 w-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-sm space-y-1.5">
              <div className="flex justify-between">
                <span className="text-muted-foreground flex items-center gap-1"><Cpu className="h-3 w-3" />CPU</span>
                <span className={`font-medium ${cpuColor}`}>{aggregates.totalCpuPercent.toFixed(1)}%</span>
              </div>
              <div className="flex justify-between">
                <span className="text-muted-foreground flex items-center gap-1"><MemoryStick className="h-3 w-3" />Memory</span>
                <span className={`font-medium ${memColor}`}>{formatBytes(aggregates.totalMemUsage)} of {formatBytes(aggregates.totalMemLimit)} ({aggregates.totalMemPercent.toFixed(0)}%)</span>
              </div>
              <div className="flex justify-between">
                <span className="text-muted-foreground flex items-center gap-1"><Network className="h-3 w-3" />Network I/O</span>
                <span className="font-medium">↓ {formatBytes(aggregates.totalNetRx)} ↑ {formatBytes(aggregates.totalNetTx)}</span>
              </div>
              <div className="flex justify-between">
                <span className="text-muted-foreground flex items-center gap-1"><Hash className="h-3 w-3" />Processes</span>
                <span className="font-medium">{aggregates.totalPids} across {stats.runningContainers} containers</span>
              </div>
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium">Disk Usage</CardTitle>
            <HardDrive className="h-4 w-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="space-y-4">
              <div className="text-2xl font-bold">
                {formatBytes(stats.diskUsage?.total ?? 0)}
              </div>
              <div className="text-sm space-y-1.5">
                <div className="flex justify-between">
                  <span className="text-muted-foreground flex items-center gap-1"><Layers className="h-3 w-3" />Images</span>
                  <span className="font-medium">{formatBytes(stats.diskUsage?.images ?? 0)}</span>
                </div>
                <div className="flex justify-between">
                  <span className="text-muted-foreground flex items-center gap-1"><Box className="h-3 w-3" />Containers</span>
                  <span className="font-medium">{formatBytes(stats.diskUsage?.containers ?? 0)}</span>
                </div>
                <div className="flex justify-between">
                  <span className="text-muted-foreground flex items-center gap-1"><Database className="h-3 w-3" />Volumes</span>
                  <span className="font-medium">{formatBytes(stats.diskUsage?.volumes ?? 0)}</span>
                </div>
                <div className="flex justify-between">
                  <span className="text-muted-foreground flex items-center gap-1"><Archive className="h-3 w-3" />Build Cache</span>
                  <span className="font-medium">{formatBytes(stats.diskUsage?.buildCache ?? 0)}</span>
                </div>
                <div className="flex justify-between">
                  <span className="text-muted-foreground flex items-center gap-1"><Activity className="h-3 w-3" />Swap</span>
                  <span className="font-medium">{formatBytes(aggregates.totalSwap)}</span>
                </div>
              </div>
            </div>
          </CardContent>
        </Card>
      </div>

      {/* Per-container sparklines */}
      <Card>
        <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
          <CardTitle className="text-sm font-medium">Container Metrics</CardTitle>
          <Cpu className="h-4 w-4 text-muted-foreground" />
        </CardHeader>
        <CardContent>
          <ContainerSparklineList containers={containers} />
        </CardContent>
      </Card>
    </div>
  )
}
