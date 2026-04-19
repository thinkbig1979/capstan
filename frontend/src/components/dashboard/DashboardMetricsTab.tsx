import { Activity, Cpu, HardDrive, MemoryStick, Network, Server, Database, Hash } from 'lucide-react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import type { DashboardStats } from '@/types'
import type { DashboardAggregateMetrics } from '@/hooks/useDashboardMetrics'

function formatBytes(bytes: number): string {
  if (bytes === 0) return '0 B'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return `${(bytes / Math.pow(k, i)).toFixed(1)} ${sizes[i]}`
}

function getColorForThreshold(percent: number): string {
  if (percent >= 80) return 'text-red-500'
  if (percent >= 60) return 'text-yellow-500'
  return 'text-green-500'
}

function getBarColor(percent: number): string {
  if (percent >= 80) return 'bg-red-500'
  if (percent >= 60) return 'bg-yellow-500'
  return 'bg-green-500'
}

interface DashboardMetricsTabProps {
  stats: DashboardStats | undefined
  aggregates: DashboardAggregateMetrics
  isConnected: boolean
}

export function DashboardMetricsTab({ stats, aggregates, isConnected }: DashboardMetricsTabProps) {
  if (!stats) {
    return (
      <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-4">
        {Array.from({ length: 8 }).map((_, i) => (
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
    )
  }

  const cpuColor = getColorForThreshold(aggregates.totalCpuPercent)
  const memColor = getColorForThreshold(aggregates.totalMemPercent)

  return (
    <div className="space-y-6">
      <div className="flex items-center gap-2">
        {isConnected ? (
          <div className="h-2 w-2 rounded-full bg-green-500 animate-pulse" />
        ) : (
          <div className="h-2 w-2 rounded-full bg-muted-foreground" />
        )}
        <span className="text-xs text-muted-foreground">
          {isConnected ? 'Live metrics' : 'Connecting...'}
        </span>
      </div>

      <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-4">
        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium">CPU Load</CardTitle>
            <Cpu className="h-4 w-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className={`text-2xl font-bold ${cpuColor}`}>
              {aggregates.totalCpuPercent.toFixed(1)}%
            </div>
            <div className="mt-2 h-2 w-full rounded-full bg-muted">
              <div
                className={`h-full rounded-full transition-all ${getBarColor(aggregates.totalCpuPercent)}`}
                style={{ width: `${Math.min(aggregates.totalCpuPercent, 100)}%` }}
              />
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium">Memory Used</CardTitle>
            <MemoryStick className="h-4 w-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className={`text-2xl font-bold ${memColor}`}>
              {formatBytes(aggregates.totalMemUsage)}
            </div>
            <p className="text-xs text-muted-foreground mt-1">
              of {formatBytes(aggregates.totalMemLimit)} ({aggregates.totalMemPercent.toFixed(0)}%)
            </p>
            <div className="mt-2 h-2 w-full rounded-full bg-muted">
              <div
                className={`h-full rounded-full transition-all ${getBarColor(aggregates.totalMemPercent)}`}
                style={{ width: `${Math.min(aggregates.totalMemPercent, 100)}%` }}
              />
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium">Image Disk Usage</CardTitle>
            <HardDrive className="h-4 w-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold">
              {formatBytes(stats.imageDiskUsage)}
            </div>
            <p className="text-xs text-muted-foreground mt-1">
              Total across all images
            </p>
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium">Total Processes</CardTitle>
            <Hash className="h-4 w-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold">
              {aggregates.totalPids}
            </div>
            <p className="text-xs text-muted-foreground mt-1">
              Across {stats.runningContainers} containers
            </p>
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium">Network I/O</CardTitle>
            <Network className="h-4 w-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-sm space-y-1">
              <div className="flex justify-between">
                <span className="text-muted-foreground">RX</span>
                <span className="font-medium">{formatBytes(aggregates.totalNetRx)}</span>
              </div>
              <div className="flex justify-between">
                <span className="text-muted-foreground">TX</span>
                <span className="font-medium">{formatBytes(aggregates.totalNetTx)}</span>
              </div>
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium">Block I/O</CardTitle>
            <Activity className="h-4 w-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-sm space-y-1">
              <div className="flex justify-between">
                <span className="text-muted-foreground">Read</span>
                <span className="font-medium">{formatBytes(aggregates.totalBlockRead)}</span>
              </div>
              <div className="flex justify-between">
                <span className="text-muted-foreground">Write</span>
                <span className="font-medium">{formatBytes(aggregates.totalBlockWrite)}</span>
              </div>
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium">Swap Usage</CardTitle>
            <Database className="h-4 w-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold">
              {formatBytes(aggregates.totalSwap)}
            </div>
            <p className="text-xs text-muted-foreground mt-1">
              Container swap memory
            </p>
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium">Containers</CardTitle>
            <Server className="h-4 w-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold">
              {stats.runningContainers}
            </div>
            <p className="text-xs text-muted-foreground mt-1">
              of {stats.totalContainers} total
            </p>
          </CardContent>
        </Card>
      </div>
    </div>
  )
}
