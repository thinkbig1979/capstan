import { useState, useCallback, useMemo } from 'react'
import { useWebSocketJSON } from './useWebSocket'

export interface DashboardContainerMetric {
  cpuPercent: number
  memUsage: number
  memLimit: number
  memPercent: number
  netRx: number
  netTx: number
  blockRead: number
  blockWrite: number
  memSwap: number
  pids: number
}

export interface DashboardContainerMetricHistory {
  containerId: string
  name: string
  metrics: DashboardContainerMetric[]
}

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

export interface DashboardMetricsMessage {
  timestamp: string
  containers: Array<{
    containerId: string
    name: string
    cpuPercent: number
    memUsage: number
    memLimit: number
    memPercent: number
    netRx: number
    netTx: number
    blockRead: number
    blockWrite: number
    memSwap: number
    pids: number
  }>
}

const HISTORY_SIZE = 60

export function useDashboardMetrics() {
  const [containers, setContainers] = useState<Record<string, DashboardContainerMetricHistory>>({})
  const [isConnected, setIsConnected] = useState(false)

  const handleMessage = useCallback((message: DashboardMetricsMessage) => {
    setIsConnected(true)
    setContainers((prev) => {
      const next = { ...prev }

      message.containers.forEach((container) => {
        const metric: DashboardContainerMetric = {
          cpuPercent: container.cpuPercent,
          memUsage: container.memUsage,
          memLimit: container.memLimit,
          memPercent: container.memPercent,
          netRx: container.netRx,
          netTx: container.netTx,
          blockRead: container.blockRead,
          blockWrite: container.blockWrite,
          memSwap: container.memSwap,
          pids: container.pids,
        }

        const existing = next[container.containerId]
        const currentMetrics = existing ? [...existing.metrics] : []
        currentMetrics.push(metric)
        const trimmed = currentMetrics.length > HISTORY_SIZE
          ? currentMetrics.slice(-HISTORY_SIZE)
          : currentMetrics

        next[container.containerId] = {
          containerId: container.containerId,
          name: existing?.name ?? container.name,
          metrics: trimmed,
        }
      })

      return next
    })
  }, [])

  const ws = useWebSocketJSON<DashboardMetricsMessage>(
    '/ws/dashboard/metrics',
    handleMessage,
    {
      onOpen: () => setIsConnected(true),
      onClose: () => setIsConnected(false),
    }
  )

  const aggregates = useMemo(() => {
    const containerList = Object.values(containers)
    if (containerList.length === 0) {
      return {
        totalCpuPercent: 0,
        totalMemUsage: 0,
        totalMemLimit: 0,
        totalMemPercent: 0,
        totalNetRx: 0,
        totalNetTx: 0,
        totalBlockRead: 0,
        totalBlockWrite: 0,
        totalSwap: 0,
        totalPids: 0,
      }
    }

    const totalCpuPercent = containerList.reduce(
      (sum, c) => sum + (c.metrics[c.metrics.length - 1]?.cpuPercent || 0),
      0
    )
    const totalMemUsage = containerList.reduce(
      (sum, c) => sum + (c.metrics[c.metrics.length - 1]?.memUsage || 0),
      0
    )
    const totalMemLimit = containerList.reduce(
      (sum, c) => sum + (c.metrics[c.metrics.length - 1]?.memLimit || 0),
      0
    )
    const totalNetRx = containerList.reduce(
      (sum, c) => sum + (c.metrics[c.metrics.length - 1]?.netRx || 0),
      0
    )
    const totalNetTx = containerList.reduce(
      (sum, c) => sum + (c.metrics[c.metrics.length - 1]?.netTx || 0),
      0
    )
    const totalBlockRead = containerList.reduce(
      (sum, c) => sum + (c.metrics[c.metrics.length - 1]?.blockRead || 0),
      0
    )
    const totalBlockWrite = containerList.reduce(
      (sum, c) => sum + (c.metrics[c.metrics.length - 1]?.blockWrite || 0),
      0
    )
    const totalSwap = containerList.reduce(
      (sum, c) => sum + (c.metrics[c.metrics.length - 1]?.memSwap || 0),
      0
    )
    const totalPids = containerList.reduce(
      (sum, c) => sum + (c.metrics[c.metrics.length - 1]?.pids || 0),
      0
    )

    return {
      totalCpuPercent,
      totalMemUsage,
      totalMemLimit,
      totalMemPercent: totalMemLimit > 0 ? (totalMemUsage / totalMemLimit) * 100 : 0,
      totalNetRx,
      totalNetTx,
      totalBlockRead,
      totalBlockWrite,
      totalSwap,
      totalPids,
    }
  }, [containers])

  const latestMetrics = useMemo(() => {
    const result: Record<string, DashboardContainerMetric> = {}
    for (const [id, history] of Object.entries(containers)) {
      const latest = history.metrics[history.metrics.length - 1]
      if (latest) {
        result[id] = latest
      }
    }
    return result
  }, [containers])

  return {
    containers: Object.values(containers),
    aggregates,
    latestMetrics,
    isConnected,
    ws,
  }
}
