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

export interface MetricsContainerFrame {
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
}

export interface MetricsMessage {
  timestamp: string
  // Nullable on purpose. A Go nil slice marshals to JSON `null`, not `[]`, so
  // `null` is a shape this socket can genuinely deliver (agent-os-5scv). The
  // backend no longer sends it, but the type is the wire contract and it has
  // to stay honest about what the wire can carry: declaring this non-nullable
  // is what let an unguarded `.forEach` past the compiler in the first place.
  containers: MetricsContainerFrame[] | null
}

export interface MetricsBaseOptions {
  historySize?: number
  onOpen?: () => void
  onClose?: () => void
}

const DEFAULT_HISTORY_SIZE = 60

export function useMetricsBase(path: string, options: MetricsBaseOptions = {}) {
  const historySize = options.historySize ?? DEFAULT_HISTORY_SIZE
  const [containers, setContainers] = useState<Record<string, ContainerMetricHistory>>({})
  const [isConnected, setIsConnected] = useState(false)

  const handleMessage = useCallback((message: MetricsMessage) => {
    setIsConnected(true)
    setContainers((prev) => {
      const next = { ...prev }
      const activeIds = new Set<string>()

      // The backend's declared type says this is never null, but the wire
      // format cannot enforce that: a nil Go slice marshals to JSON `null`
      // (agent-os-5scv), and this updater runs during React's render phase,
      // outside the WS layer's try/catch, so an unguarded dereference here
      // takes down the whole app. Guard it regardless of what the backend
      // currently sends.
      const incomingContainers = message.containers ?? []
      incomingContainers.forEach((container) => {
        activeIds.add(container.containerId)
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
        const trimmed = currentMetrics.length > historySize
          ? currentMetrics.slice(-historySize)
          : currentMetrics

        next[container.containerId] = {
          containerId: container.containerId,
          name: existing?.name ?? container.name,
          metrics: trimmed,
        }
      })

      for (const id of Object.keys(next)) {
        if (!activeIds.has(id)) {
          delete next[id]
        }
      }

      return next
    })
  }, [historySize])

  const ws = useWebSocketJSON<MetricsMessage>(path, handleMessage, {
    onOpen: () => setIsConnected(true),
    onClose: () => setIsConnected(false),
  })

  const containerList = useMemo(() => Object.values(containers), [containers])

  const baseAggregates = useMemo(() => {
    if (containerList.length === 0) {
      return { totalCpuPercent: 0, totalMemUsage: 0, totalMemLimit: 0, totalMemPercent: 0 }
    }
    const latest = (c: ContainerMetricHistory) => c.metrics[c.metrics.length - 1]
    const totalCpuPercent = containerList.reduce((s, c) => s + (latest(c)?.cpuPercent || 0), 0)
    const totalMemUsage = containerList.reduce((s, c) => s + (latest(c)?.memUsage || 0), 0)
    const totalMemLimit = containerList.reduce((s, c) => s + (latest(c)?.memLimit || 0), 0)
    return {
      totalCpuPercent,
      totalMemUsage,
      totalMemLimit,
      totalMemPercent: totalMemLimit > 0 ? (totalMemUsage / totalMemLimit) * 100 : 0,
    }
  }, [containerList])

  const latestMetrics = useMemo(() => {
    const result: Record<string, ContainerMetric> = {}
    for (const [id, history] of Object.entries(containers)) {
      const m = history.metrics[history.metrics.length - 1]
      if (m) result[id] = m
    }
    return result
  }, [containers])

  return { containers: containerList, baseAggregates, latestMetrics, isConnected, ws }
}
