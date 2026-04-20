import { useState, useCallback, useMemo } from 'react'
import { useWebSocketJSON } from './useWebSocket'

export interface ContainerMetric {
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

export interface ContainerMetricHistory {
  containerId: string
  name: string
  metrics: ContainerMetric[]
}

export interface AggregateMetrics {
  totalCpuPercent: number
  totalMemUsage: number
  totalMemLimit: number
  totalMemPercent: number
}

export interface MetricsMessage {
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

export function useMonitoring(stackId: string) {
  const [containers, setContainers] = useState<Record<string, ContainerMetricHistory>>({})
  const [isConnected, setIsConnected] = useState(false)

  const handleMessage = useCallback((message: MetricsMessage) => {
    setIsConnected(true)
    setContainers((prev) => {
      const next = { ...prev }

      message.containers.forEach((container) => {
        const metric: ContainerMetric = {
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

  const ws = useWebSocketJSON<MetricsMessage>(
    `/ws/metrics/${stackId}`,
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

    return {
      totalCpuPercent,
      totalMemUsage,
      totalMemLimit,
      totalMemPercent: totalMemLimit > 0 ? (totalMemUsage / totalMemLimit) * 100 : 0,
    }
  }, [containers])

  return {
    containers: Object.values(containers),
    aggregates,
    isConnected,
    ws,
  }
}
