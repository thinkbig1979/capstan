import { useMemo } from 'react'
import { useMetricsBase, type ContainerMetricHistory, type ContainerMetric, type MetricsMessage } from './useMetricsBase'

export type { ContainerMetric, ContainerMetricHistory, MetricsMessage }

export interface AggregateMetrics {
  totalCpuPercent: number
  totalMemUsage: number
  totalMemLimit: number
  totalMemPercent: number
}

export function useMonitoring(stackId: string) {
  const { containers, baseAggregates, isConnected, ws } = useMetricsBase(`/ws/metrics/${stackId}`)

  const aggregates = useMemo<AggregateMetrics>(() => ({
    totalCpuPercent: baseAggregates.totalCpuPercent,
    totalMemUsage: baseAggregates.totalMemUsage,
    totalMemLimit: baseAggregates.totalMemLimit,
    totalMemPercent: baseAggregates.totalMemPercent,
  }), [baseAggregates])

  return { containers, aggregates, isConnected, ws }
}
