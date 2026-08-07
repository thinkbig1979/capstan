import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { HelpHint } from '@/components/ui/help-hint'
import type { Draft } from './types'

interface RetentionSectionProps {
  draft: Draft
  onChange: <K extends keyof Draft>(key: K, value: Draft[K]) => void
}

const RETENTION_FIELDS = [
  { key: 'keepDaily', label: 'Keep daily', id: 'backup-keep-daily' },
  { key: 'keepWeekly', label: 'Keep weekly', id: 'backup-keep-weekly' },
  { key: 'keepMonthly', label: 'Keep monthly', id: 'backup-keep-monthly' },
  { key: 'keepYearly', label: 'Keep yearly', id: 'backup-keep-yearly' },
] as const

export function RetentionSection({ draft, onChange }: RetentionSectionProps) {
  return (
    <div className="space-y-4 pt-4 border-t">
      <div className="flex items-center gap-1.5">
        <h3 className="text-lg font-medium">Retention</h3>
        <HelpHint
          label="Retention"
          title="Retention"
          side="right"
          href="https://github.com/thinkbig1979/capstan/blob/main/docs/reference/configuration.md#backups--restic"
        >
          <p>
            After a backup, restic can thin out old snapshots, keeping a set number per day,
            week, month, and year. Set a level to 0 to keep none there.
          </p>
          <p>Snapshots are only removed when auto-prune is on or you prune by hand.</p>
        </HelpHint>
      </div>

      <div className="grid grid-cols-2 gap-4 sm:grid-cols-4 max-w-xl">
        {RETENTION_FIELDS.map(({ key, label, id }) => (
          <div key={key} className="space-y-1">
            <Label htmlFor={id}>{label}</Label>
            <Input
              id={id}
              type="number"
              min={0}
              value={draft[key]}
              onChange={(e) => onChange(key, parseInt(e.target.value, 10) || 0)}
              className="w-full"
            />
          </div>
        ))}
      </div>
      <p className="text-xs text-muted-foreground">
        Number of snapshots to keep at each interval. 0 = keep none for that interval.
      </p>

      <div className="flex items-center gap-3">
        <Switch
          id="backup-auto-prune"
          checked={draft.autoPrune}
          onCheckedChange={(v) => onChange('autoPrune', v)}
        />
        <div>
          <Label htmlFor="backup-auto-prune">Auto-prune after backup</Label>
          <p className="text-xs text-muted-foreground">
            Automatically apply the retention policy after each backup run.
          </p>
        </div>
      </div>
    </div>
  )
}
