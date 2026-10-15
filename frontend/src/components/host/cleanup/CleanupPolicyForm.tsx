import { useState } from 'react'
import { toast } from 'sonner'
import { settingsSaveFault } from '@/lib/settings-save-fault'
import { presentFault } from '@/lib/error-handler'
import { Button } from '@/components/ui/button'
import { NumericField } from '@/components/settings/NumericField'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { HelpHint } from '@/components/ui/help-hint'
import { LoadingSpinner } from '@/components/LoadingSkeleton'
import { RefreshFailedNotice } from '@/components/RefreshFailedNotice'
import { useDockerCleanupPolicy, useUpdateDockerCleanupPolicy } from '@/hooks/useResources'
import type { DockerCleanupCategories } from '@/types'
import { CLEANUP_CATEGORY_ORDER, type CleanupCategory } from './categories'

const GIB = 1024 ** 3
/** The PUT refuses a threshold above 1 TiB (spec "Settings keys"). */
const MAX_THRESHOLD_GB = 1024

const SWITCH_LABELS: Record<CleanupCategory, string> = {
  danglingImages: 'Dangling images',
  unusedImages: 'Unused tagged images',
  buildCache: 'Build cache',
  containers: 'Stopped containers outside any stack',
  volumes: 'Unattached volumes',
  networks: 'Unused networks',
}

/** Shown while the switch is on (spec "UI consequences"). */
const SWITCH_WARNINGS: Partial<Record<CleanupCategory, string>> = {
  unusedImages:
    'Removes images no container uses, including the previous version you might roll back to. They can be pulled again if they are in a registry.',
  volumes:
    'Volumes can hold databases or uploads. Automatic removal cannot be undone. Stack volumes are never removed.',
}

/** The edit buffer. A key is absent while untouched (the saved value shows) and
 *  null once the operator has emptied the box (agent-os-qags.4): empty is not a
 *  number, so Save stays off instead of the box turning into a 0 they never typed. */
interface PolicyDraft {
  enabled?: boolean
  minAgeHours?: number | null
  intervalHours?: number | null
  categories?: Partial<DockerCleanupCategories>
  thresholdGb?: number | null
}

/** The wire shape of a save: every field optional, and never null. */
interface PolicyPayload {
  enabled?: boolean
  minAgeHours?: number
  intervalHours?: number
  categories?: Partial<DockerCleanupCategories>
  alertThresholdBytes?: number
}

interface CleanupPolicyFormProps {
  /** Opened from "Turn on": the schedule switch starts on, still unsaved. */
  startEnabled?: boolean
  onSaved?: () => void
  onCancel?: () => void
}

/** The automatic cleanup policy. It is off until an operator turns it on: a
 *  removal is irreversible, so nothing here is opt-out. Rendered inside the
 *  Edit schedule dialog, which is why the volumes confirmation is a step in the
 *  form, as in the Free up space dialog: a confirmation mounted outside the
 *  modal is hidden by it (AutoCleanupPanel.test.tsx, "the volumes confirmation
 *  is clickable inside the dialog", goes red against that mutant). */
export function CleanupPolicyForm({ startEnabled, onSaved, onCancel }: CleanupPolicyFormProps) {
  const { data, isPending, isError, refetch } = useDockerCleanupPolicy()
  const updatePolicy = useUpdateDockerCleanupPolicy()
  const [draft, setDraft] = useState<PolicyDraft>(startEnabled ? { enabled: true } : {})
  const [confirmingVolumes, setConfirmingVolumes] = useState(false)

  // isPending, not isLoading: offline the first fetch is paused, isLoading is false
  // with no data yet, and the branch below would fire (agent-os-cg26).
  if (isPending) {
    return <div className="py-4"><LoadingSpinner /></div>
  }

  // agent-os-r1kc: a fabricated "disabled, 168 hours" is indistinguishable from
  // an operator having chosen it, and this form is a WRITE-BACK surface, so a
  // Save from invented values would persist them over the real policy.
  // agent-os-wczm: keyed on `!data` alone. A failed REFETCH over a populated
  // form is reported beside Save instead of discarding the operator's edits.
  if (!data) {
    return (
      <p className="text-sm text-destructive">
        The cleanup schedule could not be read, so it is not shown here. A scheduled run
        skips a policy it cannot read rather than removing anything at a default.
      </p>
    )
  }

  const enabled = draft.enabled ?? data.enabled
  // `!== undefined`, not `??`: null is "cleared", and `??` would turn it back
  // into the saved value, so the box would snap back as the operator cleared it.
  const minAgeHours = draft.minAgeHours !== undefined ? draft.minAgeHours : data.minAgeHours
  const intervalHours = draft.intervalHours !== undefined ? draft.intervalHours : data.intervalHours
  const savedThresholdGb = Math.round(data.alertThresholdBytes / GIB)
  const thresholdGb = draft.thresholdGb !== undefined ? draft.thresholdGb : savedThresholdGb
  const categories: DockerCleanupCategories = { ...data.categories, ...draft.categories }

  const ageBelowFloor = minAgeHours !== null && minAgeHours < data.minAllowedAgeHours
  const intervalBelowFloor = intervalHours !== null && intervalHours < data.minAllowedIntervalHours
  const thresholdTooHigh = thresholdGb !== null && thresholdGb > MAX_THRESHOLD_GB
  const noCategory = enabled && CLEANUP_CATEGORY_ORDER.every((c) => !categories[c])
  const invalid =
    minAgeHours === null || intervalHours === null || thresholdGb === null ||
    ageBelowFloor || intervalBelowFloor || thresholdTooHigh || noCategory

  const changedCategories = CLEANUP_CATEGORY_ORDER.filter((c) => categories[c] !== data.categories[c])
  const thresholdChanged = draft.thresholdGb !== undefined && thresholdGb !== savedThresholdGb
  const dirty =
    enabled !== data.enabled ||
    minAgeHours !== data.minAgeHours ||
    intervalHours !== data.intervalHours ||
    changedCategories.length > 0 ||
    thresholdChanged
  const volumesNewlyOn = categories.volumes && !data.categories.volumes

  const save = () => {
    // Only what changed: every field of the PUT is optional (the server merges
    // a partial categories object), and sending an unchanged interval re-arms
    // the scheduler's ticker for no reason.
    if (minAgeHours === null || intervalHours === null || thresholdGb === null) return
    const payload: PolicyPayload = {}
    if (enabled !== data.enabled) payload.enabled = enabled
    if (minAgeHours !== data.minAgeHours) payload.minAgeHours = minAgeHours
    if (intervalHours !== data.intervalHours) payload.intervalHours = intervalHours
    if (changedCategories.length > 0) {
      payload.categories = Object.fromEntries(changedCategories.map((c) => [c, categories[c]]))
    }
    if (thresholdChanged) payload.alertThresholdBytes = thresholdGb * GIB

    updatePolicy.mutate(payload, {
      onSuccess: () => {
        toast.success('Cleanup schedule updated')
        setDraft({})
        setConfirmingVolumes(false)
        onSaved?.()
      },
      // presentFault, NOT presentError (agent-os-5g8a): the cause is read by
      // settingsSaveFault, which is CODE-keyed, so each server 400 ("minAgeHours
      // must be at least N", the all-off refusal) keeps its own sentence.
      onError: (error) => {
        setConfirmingVolumes(false)
        presentFault('Failed to update the cleanup schedule', settingsSaveFault(error))
      },
    })
  }

  const handleSubmit = () => {
    if (volumesNewlyOn) {
      setConfirmingVolumes(true)
      return
    }
    save()
  }

  const setCategory = (c: CleanupCategory, on: boolean) => {
    setConfirmingVolumes(false)
    setDraft((d) => ({ ...d, categories: { ...d.categories, [c]: on } }))
  }

  return (
    <form onSubmit={(e) => { e.preventDefault(); handleSubmit() }} className="space-y-4">
      <div className="flex items-center gap-3">
        <Switch
          id="docker-cleanup-enabled"
          checked={enabled}
          onCheckedChange={(checked) => setDraft((d) => ({ ...d, enabled: checked }))}
        />
        <Label htmlFor="docker-cleanup-enabled">Run cleanup on a schedule</Label>
      </div>

      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <div className="space-y-1">
          <div className="flex items-center gap-1.5">
            <Label htmlFor="docker-cleanup-min-age">Age floor (hours)</Label>
            <HelpHint label="Age floor" topic="docker-cleanup.min-age">
              <p>Anything younger than this is kept, which protects fresh builds.</p>
              <p>Age counts from when the object was created, not from when it was last used.</p>
            </HelpHint>
          </div>
          <NumericField
            id="docker-cleanup-min-age"
            min={data.minAllowedAgeHours}
            value={minAgeHours}
            onValueChange={(v) => setDraft((d) => ({ ...d, minAgeHours: v }))}
          />
          <p className="text-xs text-muted-foreground">
            Only what was created more than this many hours ago is removed.
          </p>
        </div>

        <div className="space-y-1">
          <Label htmlFor="docker-cleanup-interval">Run every (hours)</Label>
          <NumericField
            id="docker-cleanup-interval"
            min={data.minAllowedIntervalHours}
            value={intervalHours}
            onValueChange={(v) => setDraft((d) => ({ ...d, intervalHours: v }))}
          />
          <p className="text-xs text-muted-foreground">How often the scheduled cleanup runs.</p>
        </div>
      </div>

      {ageBelowFloor && (
        <p className="text-xs text-destructive">
          The age floor must be at least {data.minAllowedAgeHours} hours.
        </p>
      )}
      {intervalBelowFloor && (
        <p className="text-xs text-destructive">
          The interval must be at least {data.minAllowedIntervalHours} hours.
        </p>
      )}

      <fieldset className="space-y-2">
        <legend className="text-sm font-medium">What it removes</legend>
        {CLEANUP_CATEGORY_ORDER.map((c) => (
          <div key={c} className="space-y-1">
            <div className="flex items-center gap-3">
              <Switch
                id={`docker-cleanup-category-${c}`}
                checked={categories[c]}
                onCheckedChange={(on) => setCategory(c, on)}
              />
              <Label htmlFor={`docker-cleanup-category-${c}`}>{SWITCH_LABELS[c]}</Label>
            </div>
            {categories[c] && SWITCH_WARNINGS[c] && (
              <p data-testid={`cleanup-warning-${c}`} className="pl-14 text-xs text-warning">
                {SWITCH_WARNINGS[c]}
              </p>
            )}
          </div>
        ))}
        {noCategory && (
          <p className="text-xs text-destructive">
            Turn on at least one category, or turn automatic cleanup off.
          </p>
        )}
      </fieldset>

      <div className="space-y-1">
        <Label htmlFor="docker-cleanup-threshold">
          Warn on the dashboard when more than this many GB can be freed
        </Label>
        <NumericField
          id="docker-cleanup-threshold"
          className="max-w-32"
          min={0}
          max={MAX_THRESHOLD_GB}
          value={thresholdGb}
          onValueChange={(v) => setDraft((d) => ({ ...d, thresholdGb: v }))}
        />
        <p className="text-xs text-muted-foreground">0 turns the warning off.</p>
        {thresholdTooHigh && (
          <p className="text-xs text-destructive">The limit is {MAX_THRESHOLD_GB} GB.</p>
        )}
      </div>

      {isError && (
        <RefreshFailedNotice what="the cleanup schedule" beforeSave onRetry={() => refetch()} />
      )}

      {confirmingVolumes ? (
        <div
          role="alert"
          data-testid="cleanup-volumes-confirm"
          className="space-y-3 rounded-md border border-destructive/50 p-3 text-sm"
        >
          <p>
            Automatic cleanup will remove unattached volumes older than {minAgeHours} h, every{' '}
            {intervalHours} h. {SWITCH_WARNINGS.volumes}
          </p>
          <div className="flex justify-end gap-2">
            <Button type="button" variant="outline" onClick={() => setConfirmingVolumes(false)}>
              Go back
            </Button>
            <Button type="button" variant="destructive" disabled={updatePolicy.isPending} onClick={save}>
              Turn on volume removal
            </Button>
          </div>
        </div>
      ) : (
        <div className="flex justify-end gap-2">
          {onCancel && (
            <Button type="button" variant="outline" onClick={onCancel}>
              Cancel
            </Button>
          )}
          <Button type="submit" disabled={!dirty || invalid || updatePolicy.isPending}>
            {updatePolicy.isPending ? 'Saving…' : 'Save cleanup schedule'}
          </Button>
        </div>
      )}
    </form>
  )
}
