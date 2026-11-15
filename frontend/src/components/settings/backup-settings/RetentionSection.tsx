import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { HelpHint } from '@/components/ui/help-hint'
import { NumericField } from '../NumericField'
import type { Draft } from './types'
import { LOCKED_BACKUP_TITLE } from '@/lib/locked-copy'

interface RetentionSectionProps {
  draft: Draft
  onChange: <K extends keyof Draft>(key: K, value: Draft[K]) => void
  /** Env values are locked: the server refuses retention changes (agent-os-h71i). */
  locked: boolean
}

const RETENTION_FIELDS = [
  { key: 'keepDaily', label: 'Keep daily', id: 'backup-keep-daily' },
  { key: 'keepWeekly', label: 'Keep weekly', id: 'backup-keep-weekly' },
  { key: 'keepMonthly', label: 'Keep monthly', id: 'backup-keep-monthly' },
  { key: 'keepYearly', label: 'Keep yearly', id: 'backup-keep-yearly' },
] as const

export function RetentionSection({ draft, onChange, locked }: RetentionSectionProps) {
  const lockedTitle = locked ? LOCKED_BACKUP_TITLE : undefined
  return (
    <div className="space-y-4 pt-4 border-t">
      <div className="flex items-center gap-1.5">
        <h3 className="text-lg font-medium">Retention</h3>
        <HelpHint
          label="Retention"
          title="Retention"
          side="right"
          topic="backup.retention"
        >
          <p>
            After every backup, old snapshots are removed from the list, keeping a set number
            per day, week, month, and year. A level set to 0 is switched off. If all four are
            0, nothing is removed.
          </p>
          <p>Disk space is freed only when auto-prune is on or you prune by hand.</p>
        </HelpHint>
      </div>

      <div className="grid grid-cols-2 gap-4 sm:grid-cols-4 max-w-xl">
        {RETENTION_FIELDS.map(({ key, label, id }) => (
          <div key={key} className="space-y-1">
            <Label htmlFor={id}>{label}</Label>
            <NumericField
              id={id}
              min={0}
              value={draft[key]}
              onValueChange={(v) => onChange(key, v)}
              className="w-full"
              disabled={locked}
              title={lockedTitle}
            />
          </div>
        ))}
      </div>
      <p className="text-xs text-muted-foreground">
        Number of snapshots to keep at each interval. 0 switches that interval off; with all four at 0, nothing is removed.
      </p>

      <div className="flex items-center gap-3">
        <Switch
          id="backup-auto-prune"
          checked={draft.autoPrune}
          onCheckedChange={(v) => onChange('autoPrune', v)}
          disabled={locked}
          title={lockedTitle}
        />
        <div>
          <Label htmlFor="backup-auto-prune">Auto-prune after backup</Label>
          <p className="text-xs text-muted-foreground">
            Free the disk space of removed snapshots after each backup. Off: they leave the list, but space is only freed by a manual prune.
          </p>
        </div>
      </div>
    </div>
  )
}
