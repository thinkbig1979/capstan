import type { LogTimeRange } from '@/stores/uiStore'

export interface LogMessage {
  container: string
  timestamp: string
  message: string
}

// LogMessage as received over the socket carries no per-line identity, but the
// buffer is filtered live (search, container, time range, errors-only), so the
// position of a line within the rendered list is not stable across renders.
// Each line gets a synthetic, monotonically increasing id at ingestion time so
// it keeps a stable React key regardless of which filtered subset it lands in.
export interface DisplayLogMessage extends LogMessage {
  id: number
}

export interface TimeRangeConfig {
  label: string
  value: LogTimeRange
  getStartTime: () => Date | null
}

export interface LogViewerProps {
  stackId: string
  initialContainer?: string
  hasRunningContainers?: boolean
}

export type LogLevel = 'error' | 'warn' | 'other'
