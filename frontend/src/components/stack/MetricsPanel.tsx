import { useMemo } from 'react'
import { AreaChart, Area, ResponsiveContainer, XAxis, YAxis } from 'recharts'
import { Cpu, HardDrive, Network, Activity } from 'lucide-react'
import { useMonitoring, type AggregateMetrics } from '@/hooks/useMonitoring'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'

interface MetricsPanelProps {
  stackId: string
}

function formatBytes(bytes: number): string {
  if (bytes === 0) return '0 B'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return `${(bytes / Math.pow(k, i)).toFixed(1)} ${sizes[i]}`
}

function formatRate(bytesPerSecond: number): string {
  return `${formatBytes(bytesPerSecond)}/s`
}

function getColorForThreshold(percent: number): string {
  if (percent >= 80) return 'bg-red-500'
  if (percent >= 60) return 'bg-yellow-500'
  return 'bg-green-500'
}

function getChartColor(percent: number): string {
  if (percent >= 80) return '#ef4444'
  if (percent >= 60) return '#eab308'
  return '#22c55e'
}

function AggregateRow({ aggregates }: { aggregates: AggregateMetrics }) {
  const cpuColor = getColorForThreshold(aggregates.totalCpuPercent)
  const memColor = getColorForThreshold(aggregates.totalMemPercent)

  return (
    <Card className="mb-6">
      <CardHeader>
        <CardTitle className="text-base">Aggregate Totals</CardTitle>
      </CardHeader>
      <CardContent>
        <div className="grid grid-cols-2 gap-4">
          <div className="space-y-2">
            <div className="flex items-center justify-between text-sm">
              <span className="font-medium">Total CPU</span>
              <span>{aggregates.totalCpuPercent.toFixed(1)}%</span>
            </div>
            <div className="h-2 w-full rounded-full bg-muted">
              <div
                className={`h-full rounded-full transition-all ${cpuColor}`}
                style={{ width: `${Math.min(aggregates.totalCpuPercent, 100)}%` }}
              />
            </div>
          </div>
          <div className="space-y-2">
            <div className="flex items-center justify-between text-sm">
              <span className="font-medium">Total Memory</span>
              <span>
                {formatBytes(aggregates.totalMemUsage)} / {formatBytes(aggregates.totalMemLimit)} ({aggregates.totalMemPercent.toFixed(0)}%)
              </span>
            </div>
            <div className="h-2 w-full rounded-full bg-muted">
              <div
                className={`h-full rounded-full transition-all ${memColor}`}
                style={{ width: `${Math.min(aggregates.totalMemPercent, 100)}%` }}
              />
            </div>
          </div>
        </div>
      </CardContent>
    </Card>
  )
}

function ContainerCard({
  container,
}: {
  container: ReturnType<typeof useMonitoring>['containers'][number]
}) {
  const latestMetric = container.metrics[container.metrics.length - 1]
  const cpuColor = getChartColor(latestMetric?.cpuPercent || 0)
  const memColor = getColorForThreshold(latestMetric?.memPercent || 0)

  const chartData = useMemo(() => {
    return container.metrics.map((metric, index) => ({
      index,
      cpu: metric.cpuPercent,
    }))
  }, [container.metrics])

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-sm">{container.name}</CardTitle>
      </CardHeader>
      <CardContent className="space-y-4">
        {container.metrics.length > 0 && latestMetric ? (
          <>
            <div className="space-y-2">
              <div className="flex items-center gap-2 text-xs text-muted-foreground">
                <Cpu className="h-3 w-3" />
                <span>CPU History (1 min)</span>
              </div>
              <div className="h-16">
                <ResponsiveContainer width="100%" height="100%">
                  <AreaChart data={chartData}>
                    <XAxis dataKey="index" hide />
                    <YAxis domain={[0, 100]} hide />
                    <Area
                      type="monotone"
                      dataKey="cpu"
                      stroke={cpuColor}
                      fill={cpuColor}
                      fillOpacity={0.3}
                    />
                  </AreaChart>
                </ResponsiveContainer>
              </div>
            </div>

            <div className="space-y-2">
              <div className="flex items-center justify-between text-xs">
                <span className="text-muted-foreground">Memory</span>
                <span>
                  {formatBytes(latestMetric.memUsage)} / {formatBytes(latestMetric.memLimit)} ({latestMetric.memPercent.toFixed(0)}%)
                </span>
              </div>
              <div className="h-2 w-full rounded-full bg-muted">
                <div
                  className={`h-full rounded-full transition-all ${memColor}`}
                  style={{ width: `${Math.min(latestMetric.memPercent, 100)}%` }}
                />
              </div>
            </div>

            <div className="grid grid-cols-2 gap-2">
              <div className="space-y-1">
                <div className="flex items-center gap-1 text-xs text-muted-foreground">
                  <Network className="h-3 w-3" />
                  <span>Network</span>
                </div>
                <div className="space-y-0.5 text-xs">
                  <div>
                    <span className="text-muted-foreground">rx:</span>{' '}
                    {formatRate(latestMetric.netRx)}
                  </div>
                  <div>
                    <span className="text-muted-foreground">tx:</span>{' '}
                    {formatRate(latestMetric.netTx)}
                  </div>
                </div>
              </div>

              <div className="space-y-1">
                <div className="flex items-center gap-1 text-xs text-muted-foreground">
                  <HardDrive className="h-3 w-3" />
                  <span>Block I/O</span>
                </div>
                <div className="space-y-0.5 text-xs">
                  <div>
                    <span className="text-muted-foreground">read:</span>{' '}
                    {formatRate(latestMetric.blockRead)}
                  </div>
                  <div>
                    <span className="text-muted-foreground">write:</span>{' '}
                    {formatRate(latestMetric.blockWrite)}
                  </div>
                </div>
              </div>
            </div>
          </>
        ) : (
          <div className="py-4 text-center text-sm text-muted-foreground">
            Waiting for metrics...
          </div>
        )}
      </CardContent>
    </Card>
  )
}

export function MetricsPanel({ stackId }: MetricsPanelProps) {
  const { containers, aggregates, isConnected, ws } = useMonitoring(stackId)

  if (!isConnected && ws.status === 'connecting') {
    return (
      <div className="space-y-4">
        <Card>
          <CardHeader>
            <Skeleton className="h-6 w-32" />
          </CardHeader>
          <CardContent>
            <div className="grid grid-cols-2 gap-4">
              <Skeleton className="h-8" />
              <Skeleton className="h-8" />
            </div>
          </CardContent>
        </Card>
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {[1, 2, 3].map((i) => (
            <Card key={i}>
              <CardHeader>
                <Skeleton className="h-5 w-24" />
              </CardHeader>
              <CardContent>
                <Skeleton className="mb-4 h-16 w-full" />
                <Skeleton className="mb-2 h-2 w-full" />
                <Skeleton className="h-2 w-3/4" />
              </CardContent>
            </Card>
          ))}
        </div>
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
      <AggregateRow aggregates={aggregates} />
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        {containers.map((container) => (
          <ContainerCard key={container.containerId} container={container} />
        ))}
      </div>
    </div>
  )
}
