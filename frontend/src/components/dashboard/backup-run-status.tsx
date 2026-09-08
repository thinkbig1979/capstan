import { Loader2 } from 'lucide-react'
import { cn } from '@/lib/utils'
import type { BackupRun } from '@/types'

/**
 * The tone mapping for a backup run's status, shared by the per-stack Backups
 * tab and the dashboard's install-wide backup history. It lives here rather
 * than in either consumer so the two surfaces cannot drift apart on what a
 * status means. It covers the in-progress `running` state as well as the four
 * terminal ones — `running` is the entry that renders the spinner below.
 *
 * Deliberately not exported: RunStatusBadge is its only consumer, and
 * `react-refresh/only-export-components` (an error, not a warning, outside
 * components/ui) rejects a .tsx file that exports both a component and a
 * constant. If a caller ever needs the raw tones, move the map to a sibling
 * .ts module rather than re-exporting it from here.
 */
const RUN_STATUS_VARIANTS: Record<BackupRun['status'], { label: string; className: string }> = {
  success: { label: 'Success', className: 'bg-green-500/15 text-green-700 dark:text-green-400 border-green-500/30' },
  partial: { label: 'Partial', className: 'bg-yellow-500/15 text-yellow-700 dark:text-yellow-400 border-yellow-500/30' },
  failed: { label: 'Failed', className: 'bg-destructive/15 text-destructive border-destructive/30' },
  running: { label: 'Running', className: 'bg-blue-500/15 text-blue-700 dark:text-blue-400 border-blue-500/30' },
  // Neutral/warning-toned, not destructive-red: the run never reported a real
  // outcome (crash or a restore from a mid-run snapshot) and may have
  // succeeded on the original instance, so "Failed" styling would mislead.
  interrupted: { label: 'Interrupted', className: 'bg-slate-500/15 text-slate-700 dark:text-slate-400 border-slate-500/30' },
}

export function RunStatusBadge({ status }: { status: BackupRun['status'] }) {
  const v = RUN_STATUS_VARIANTS[status]
  return (
    <span className={cn('inline-flex items-center rounded-full border px-2 py-0.5 text-xs font-medium', v.className)}>
      {status === 'running' && <Loader2 className="mr-1 h-3 w-3 animate-spin" />}
      {v.label}
    </span>
  )
}
