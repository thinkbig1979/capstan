import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import type { Draft } from './types'

interface ScheduleSectionProps {
  draft: Draft
  onChange: <K extends keyof Draft>(key: K, value: Draft[K]) => void
}

export function ScheduleSection({ draft, onChange }: ScheduleSectionProps) {
  return (
    <div className="space-y-4 pt-4 border-t">
      <h3 className="text-lg font-medium">Schedule</h3>

      <div className="space-y-2">
        <Label htmlFor="backup-interval">Interval (minutes)</Label>
        <Input
          id="backup-interval"
          type="number"
          min={0}
          value={draft.scheduleIntervalMinutes}
          onChange={(e) => onChange('scheduleIntervalMinutes', parseInt(e.target.value, 10) || 0)}
          className="max-w-xs"
        />
        <p className="text-xs text-muted-foreground">
          How often to run scheduled backups. <strong>0</strong> disables scheduled backups.
        </p>
      </div>

      <div className="flex items-center gap-3">
        <Switch
          id="backup-sync-after"
          checked={draft.syncAfterBackup}
          onCheckedChange={(v) => onChange('syncAfterBackup', v)}
        />
        <div>
          <Label htmlFor="backup-sync-after">Sync to cloud after each backup</Label>
          <p className="text-xs text-muted-foreground">
            Run rclone sync automatically after every successful backup.
          </p>
        </div>
      </div>
    </div>
  )
}
