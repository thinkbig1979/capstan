import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { NumericField } from '../NumericField'
import { ScheduleModeFields } from '../ScheduleModeFields'
import type { Draft } from './types'

interface ScheduleSectionProps {
  draft: Draft
  onChange: <K extends keyof Draft>(key: K, value: Draft[K]) => void
}

export function ScheduleSection({ draft, onChange }: ScheduleSectionProps) {
  return (
    <div className="space-y-4 pt-4 border-t">
      <h3 className="text-lg font-medium">Backup schedule</h3>

      <ScheduleModeFields
        idPrefix="backup-schedule"
        mode={draft.scheduleMode}
        onModeChange={(mode) => onChange('scheduleMode', mode)}
        time={draft.scheduleTime}
        onTimeChange={(time) => onChange('scheduleTime', time)}
        days={draft.scheduleDays}
        onDaysChange={(days) => onChange('scheduleDays', days)}
        serverTimezone={draft.serverTimezone}
        serverTimeOffset={draft.serverTimeOffset}
        intervalLabel="Interval (minutes)"
        intervalControl={
          <>
            {/* id must match `${idPrefix}-interval`, the htmlFor of intervalLabel. */}
            <NumericField
              id="backup-schedule-interval"
              min={0}
              value={draft.scheduleIntervalMinutes}
              onValueChange={(v) => onChange('scheduleIntervalMinutes', v)}
              className="max-w-xs"
            />
            <p className="text-xs text-muted-foreground">
              How often to run scheduled backups. <strong>0</strong> disables scheduled backups.
            </p>
          </>
        }
      />

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

      <div className="flex items-center gap-3">
        <Switch
          id="backup-verify-weekly"
          checked={draft.verifyWeekly}
          onCheckedChange={(v) => onChange('verifyWeekly', v)}
        />
        <div>
          <Label htmlFor="backup-verify-weekly">Check the repository weekly</Label>
          <p className="text-xs text-muted-foreground">
            After a scheduled backup, once a week, read 5% of the backup data to confirm it can
            be restored. A failed check shows a warning; backups keep running.
          </p>
        </div>
      </div>
    </div>
  )
}
