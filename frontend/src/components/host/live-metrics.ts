import type { ContainerMetricHistory } from '@/hooks/useMetricsBase'

const latestCpu = (c: ContainerMetricHistory) => c.metrics[c.metrics.length - 1]?.cpuPercent || 0

/**
 * The host's total CPU per WebSocket tick, oldest first, summed the way
 * useMetricsBase sums totalCpuPercent (each container's value, missing as 0),
 * so the last point is the Live tile's number.
 *
 * Aligned from the newest sample, not from index 0: useMetricsBase appends one
 * sample per container per frame and deletes a container that a frame leaves
 * out, so every history is unbroken up to the latest tick. A container that
 * started later has a shorter history and counts as 0 on the ticks before it.
 */
export function cpuTotalSeries(containers: ContainerMetricHistory[]): number[] {
  const ticks = Math.max(0, ...containers.map((c) => c.metrics.length))
  return Array.from({ length: ticks }, (_, i) =>
    containers.reduce((sum, c) => sum + (c.metrics[c.metrics.length - ticks + i]?.cpuPercent || 0), 0),
  )
}

/** The `limit` containers with the highest latest CPU, highest first. */
export function busiestByCpu(containers: ContainerMetricHistory[], limit: number): ContainerMetricHistory[] {
  return [...containers].sort((a, b) => latestCpu(b) - latestCpu(a)).slice(0, limit)
}

export function colorForThreshold(percent: number): string {
  if (percent >= 80) return 'text-destructive'
  if (percent >= 60) return 'text-warning'
  return 'text-success'
}
