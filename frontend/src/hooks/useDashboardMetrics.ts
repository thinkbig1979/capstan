import { useMemo } from 'react'
import { useMetricsBase, type ContainerMetric, type ContainerMetricHistory } from './useMetricsBase'

export type { ContainerMetric as DashboardContainerMetric }
export type { ContainerMetricHistory as DashboardContainerMetricHistory }

export interface DashboardAggregateMetrics {
  totalCpuPercent: number
  totalMemUsage: number
  totalMemLimit: number
  totalMemPercent: number
  totalNetRx: number
  totalNetTx: number
  totalBlockRead: number
  totalBlockWrite: number
  totalSwap: number
  totalPids: number
}

export function useDashboardMetrics() {
  const { containers, baseAggregates, latestMetrics, isConnected, ws } = useMetricsBase('/ws/dashboard/metrics')

  const aggregates = useMemo<DashboardAggregateMetrics>(() => {
    const latest = (c: ContainerMetricHistory) => c.metrics[c.metrics.length - 1]
    const totalNetRx = containers.reduce((s, c) => s + (latest(c)?.netRx || 0), 0)
    const totalNetTx = containers.reduce((s, c) => s + (latest(c)?.netTx || 0), 0)
    const totalBlockRead = containers.reduce((s, c) => s + (latest(c)?.blockRead || 0), 0)
    const totalBlockWrite = containers.reduce((s, c) => s + (latest(c)?.blockWrite || 0), 0)
    const totalSwap = containers.reduce((s, c) => s + (latest(c)?.memSwap || 0), 0)
    const totalPids = containers.reduce((s, c) => s + (latest(c)?.pids || 0), 0)

    return {
      ...baseAggregates,
      totalNetRx,
      totalNetTx,
      totalBlockRead,
      totalBlockWrite,
      totalSwap,
      totalPids,
    }
  }, [baseAggregates, containers])

  return { containers, aggregates, latestMetrics, isConnected, ws }
}
