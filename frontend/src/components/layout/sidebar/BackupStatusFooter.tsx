import { Database } from 'lucide-react'
import type { BackupStatus } from '@/types'
import { relativeTime, untilTime } from './constants'

interface BackupStatusFooterProps {
  backupStatus?: BackupStatus
}

export function BackupStatusFooter({ backupStatus }: BackupStatusFooterProps) {
  if (!backupStatus?.schedulerRunning) return null

  return (
    <div className="px-3 py-1.5 border-t flex items-center gap-1.5 text-[10px] text-muted-foreground">
      <Database className="h-3 w-3 shrink-0" />
      <span className="truncate">
        {backupStatus.lastRun
          ? `Backup ${relativeTime(backupStatus.lastRun.finishedAt || backupStatus.lastRun.startedAt)}`
          : 'No backups yet'}
        {backupStatus.nextRunAt && ` · next in ${untilTime(backupStatus.nextRunAt)}`}
      </span>
    </div>
  )
}
