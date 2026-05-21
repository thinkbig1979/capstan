import { Activity, Cpu, HardDrive, MemoryStick, Network, Database, Layers, Archive, Box, Hash, Folder, Server } from 'lucide-react'
import { Card, CardAction, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Skeleton } from '@/components/ui/skeleton'
import type { DashboardStats } from '@/types'
import type { DashboardAggregateMetrics } from '@/hooks/useDashboardMetrics'
import { formatBytes } from '@/lib/format'

function getColorForThreshold(percent: number): string {
  if (percent >= 80) return 'text-destructive'
  if (percent >= 60) return 'text-warning'
  return 'text-success'
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
      <div className="grid grid-cols-1 gap-4 *:data-[slot=card]:bg-gradient-to-t *:data-[slot=card]:shadow-xs md:grid-cols-2 lg:grid-cols-4">
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
            <div className="line-clamp-1 flex gap-2 font-medium">
              {totalStacks ? Math.round((runningStacks / totalStacks) * 100) : 0}% uptime
            </div>
            <div className="text-muted-foreground">
              Stack availability
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
              <Badge variant="outline" className="text-destructive border-destructive/30">
                Inactive
              </Badge>
            </CardAction>
          </CardHeader>
          <CardFooter className="flex-col items-start gap-1.5 text-sm">
            <div className="line-clamp-1 flex gap-2 font-medium">
              {stoppedStacks > 0 ? 'Needs attention' : 'All stacks running'}
            </div>
            <div className="text-muted-foreground">
              Stopped or errored stacks
            </div>
          </CardFooter>
        </Card>
        <Card className="@container/card">
          <CardHeader>
            <CardDescription>Containers</CardDescription>
            <CardTitle className="text-2xl font-semibold tabular-nums @[250px]/card:text-3xl">
              {totalContainers}
            </CardTitle>
            <CardAction>
              <Badge variant="outline">
                <Server className="size-3" />
                {runningContainers} running
              </Badge>
            </CardAction>
          </CardHeader>
          <CardFooter className="flex-col items-start gap-1.5 text-sm">
            <div className="line-clamp-1 flex gap-2 font-medium">
              Total container instances
            </div>
            <div className="text-muted-foreground">
              Managed by Docker Compose
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
    </div>
  )
}
