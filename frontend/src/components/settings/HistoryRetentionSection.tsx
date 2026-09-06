import { useState } from 'react'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { LoadingSpinner } from '@/components/LoadingSkeleton'
import { HelpHint } from '@/components/ui/help-hint'
import { useRetentionSettings, useUpdateRetentionSettings } from '@/hooks/useResources'

type FieldKey = 'retentionDays' | 'updateHistoryRetentionDays' | 'backupHistoryRetentionDays'

const FIELDS: { key: FieldKey; label: string; id: string; hint: string }[] = [
  {
    key: 'retentionDays',
    label: 'Audit log',
    id: 'retention-audit-log',
    hint: 'Who did what, and when.',
  },
  {
    key: 'updateHistoryRetentionDays',
    label: 'Update history',
    id: 'retention-update-history',
    hint: 'One row per container image update.',
  },
  {
    key: 'backupHistoryRetentionDays',
    label: 'Backup history',
    id: 'retention-backup-history',
    hint: 'One row per backup run, plus one per stack in it.',
  },
]

/** History retention for the three tables that are pruned on a daily pass.
 *  Before this existed the retention endpoint had no caller in the UI at all,
 *  so the only way to change it was to edit the settings row by hand. */
export function HistoryRetentionSection() {
  const { data, isLoading, isError } = useRetentionSettings()
  const updateRetention = useUpdateRetentionSettings()
  const [draft, setDraft] = useState<Partial<Record<FieldKey, number>>>({})

  if (isLoading) {
    return <div className="py-4"><LoadingSpinner /></div>
  }

  // Refuse rather than fill in a number nobody configured (agent-os-r1kc).
  // The endpoint now 500s when the retention settings cannot be READ, but the
  // fallback below it used to be a hardcoded `?? 90` — so the page went on
  // displaying 90 as the configured retention even when the server had just
  // refused to say. That is the same fabricated number the daily prune would
  // have deleted at, from a second independent source, and it left the operator
  // with no way to tell a real 90-day setting from an unreadable one. The form
  // is also a WRITE-BACK surface: a Save from a fabricated form would persist
  // the invented value over the real one.
  if (isError || !data) {
    return (
      <div className="space-y-2 pt-4 border-t">
        <h3 className="text-lg font-medium">History retention</h3>
        <p className="text-sm text-destructive">
          The configured retention could not be read, so it is not shown here. The daily
          cleanup pass skips any history whose retention it cannot read, rather than
          deleting at a default.
        </p>
      </div>
    )
  }

  const min = data.minRetentionDays
  const valueOf = (key: FieldKey) => draft[key] ?? data[key]
  const dirty = FIELDS.some(({ key }) => draft[key] !== undefined && draft[key] !== data[key])
  const belowFloor = FIELDS.some(({ key }) => valueOf(key) < min)

  const handleSave = () => {
    const payload: Partial<Record<FieldKey, number>> = {}
    for (const { key } of FIELDS) {
      if (draft[key] !== undefined && draft[key] !== data[key]) payload[key] = draft[key]
    }
    updateRetention.mutate(payload, {
      onSuccess: () => {
        toast.success('Retention updated')
        setDraft({})
      },
      onError: () => toast.error('Failed to update retention'),
    })
  }

  return (
    <form
      onSubmit={(e) => { e.preventDefault(); handleSave() }}
      className="space-y-4 pt-4 border-t"
    >
      <div className="flex items-center gap-1.5">
        <h3 className="text-lg font-medium">History retention</h3>
        <HelpHint label="History retention" title="History retention" side="right">
          <p>
            How many days of history to keep. A cleanup pass runs shortly after startup and
            once a day after that, deleting anything older.
          </p>
          <p>
            Removing a backup run also removes its per-stack rows. This prunes Capstan&apos;s
            own records only — it never touches your restic snapshots.
          </p>
          <p className="text-xs">Minimum {min} days. Deleted history cannot be recovered.</p>
        </HelpHint>
      </div>

      <div className="grid grid-cols-1 gap-4 sm:grid-cols-3 max-w-xl">
        {FIELDS.map(({ key, label, id, hint }) => (
          <div key={key} className="space-y-1">
            <Label htmlFor={id}>{label}</Label>
            <Input
              id={id}
              type="number"
              min={min}
              value={valueOf(key)}
              onChange={(e) =>
                setDraft((d) => ({ ...d, [key]: parseInt(e.target.value, 10) || 0 }))
              }
            />
            <p className="text-xs text-muted-foreground">{hint}</p>
          </div>
        ))}
      </div>

      {belowFloor && (
        <p className="text-xs text-destructive">
          Retention must be at least {min} days.
        </p>
      )}

      <Button type="submit" disabled={!dirty || belowFloor || updateRetention.isPending}>
        {updateRetention.isPending ? 'Saving…' : 'Save retention'}
      </Button>
    </form>
  )
}
