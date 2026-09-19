import { useState } from 'react'
import { toast } from 'sonner'
import { settingsSaveFault } from '@/lib/settings-save-fault'
import { presentFault } from '@/lib/error-handler'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { LoadingSpinner } from '@/components/LoadingSkeleton'
import { HelpHint } from '@/components/ui/help-hint'
import { useRetentionSettings, useUpdateRetentionSettings } from '@/hooks/useResources'
import { RefreshFailedNotice } from '@/components/RefreshFailedNotice'

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
  const { data, isLoading, isError, refetch } = useRetentionSettings()
  const updateRetention = useUpdateRetentionSettings()
  const [draft, setDraft] = useState<Partial<Record<FieldKey, number>>>({})

  if (isLoading) {
    return <div className="py-4"><LoadingSpinner /></div>
  }

  // Refuse a FABRICATED value; retain a REAL one and say the refresh failed.
  //
  // agent-os-r1kc is why this branch exists: the fallback below it used to be a
  // hardcoded `?? 90`, so the page displayed 90 as the configured retention even
  // when the server had just refused to say. That is the same fabricated number
  // the daily prune would have deleted at, from a second independent source, and
  // it left the operator with no way to tell a real 90-day setting from an
  // unreadable one. The form is also a WRITE-BACK surface: a Save from a
  // fabricated form would persist the invented value over the real one.
  //
  // agent-os-wczm is why it is now keyed on `!data` alone. A value the server
  // really sent is not fabricated, so a FAILED REFETCH over a populated form is
  // no reason to blank it — that discarded the operator's unsaved edits as well.
  // `!data` keeps every refusal r1kc asked for: it still covers "the read failed
  // and we have nothing" and "the read succeeded but carried nothing". The
  // refresh failure is reported beside the Save button instead.
  if (!data) {
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
      // Takes the error (agent-os-zlw0): UpdateLogRetention mints three
      // distinct 400s and the zero-arity callback rendered one sentence for all
      // three. Generic sentence stays as the title when there is no cause.
      // presentFault keeps the no-cause path a SINGLE-argument call, which a
      // pre-existing test still pins: see the WHY at UpdateScheduleContent's
      // onError.
      onError: (error) => {
        // presentFault, NOT presentError (agent-os-5g8a): the cause is read by
        // settingsSaveFault, which is CODE-keyed and deliberately not
        // classifyError. The full argument, measured, is in presentFault's
        // docblock; the short form is that swapping the key is not this
        // change's decision to take.
        presentFault('Failed to update retention', settingsSaveFault(error))
      },
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

      {isError && (
        <RefreshFailedNotice
          what="the retention settings"
          beforeSave
          onRetry={() => refetch()}
        />
      )}

      <Button type="submit" disabled={!dirty || belowFloor || updateRetention.isPending}>
        {updateRetention.isPending ? 'Saving…' : 'Save retention'}
      </Button>
    </form>
  )
}
