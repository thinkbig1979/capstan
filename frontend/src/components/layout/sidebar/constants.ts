import type { StackStatus } from '@/types'

// Filled status dot per stack status (a pip, not the old outlined Square icon which read as a
// selection checkbox). Vocabulary matches the Running/Stopped/Error legend.
export const statusDotColor: Record<StackStatus, string> = {
  running: 'bg-success',
  partial: 'bg-warning',
  stopped: 'bg-muted-foreground',
  error: 'bg-destructive',
  unknown: 'bg-muted-foreground',
}

export type BulkAction = 'start' | 'stop' | 'restart' | 'pull'

export const BULK_LABELS: Record<BulkAction, string> = {
  start: 'Started',
  stop: 'Stopped',
  restart: 'Restarted',
  pull: 'Pulled',
}

// Compact "x ago" for a past timestamp.
export function relativeTime(iso: string): string {
  const diff = Date.now() - new Date(iso).getTime()
  const mins = Math.round(diff / 60000)
  if (mins < 1) return 'just now'
  if (mins < 60) return `${mins}m ago`
  const hrs = Math.round(mins / 60)
  if (hrs < 24) return `${hrs}h ago`
  return `${Math.round(hrs / 24)}d ago`
}

// Compact "in x" for a future timestamp.
export function untilTime(iso: string): string {
  const diff = new Date(iso).getTime() - Date.now()
  if (diff <= 0) return 'due'
  const mins = Math.round(diff / 60000)
  if (mins < 60) return `${mins}m`
  const hrs = Math.floor(mins / 60)
  return `${hrs}h ${mins % 60}m`
}
