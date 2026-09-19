import { useState } from 'react'
import { toast } from 'sonner'
import { settingsSaveFault } from '@/lib/settings-save-fault'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { HelpHint } from '@/components/ui/help-hint'
import { LoadingSpinner } from '@/components/LoadingSkeleton'
import { formatBytes } from '@/lib/format'
import {
  useDockerCleanupPolicy,
  useUpdateDockerCleanupPolicy,
  usePreviewDockerCleanup,
} from '@/hooks/useResources'
import type { DockerCleanupCandidate } from '@/types'

/** What to show for one preview row.
 *
 *  `repository` is ABSENT for the fully-untagged `<none>:<none>` form, which is
 *  what every locally built superseded image becomes — the common case, not the
 *  edge one. Rendering `repository` alone leaves a blank row for most of a real
 *  candidate list, so the id is the fallback, formatted the way the rest of the
 *  app formats one (ImagesTab.tsx:171): drop the algorithm prefix FIRST, then
 *  truncate. A raw slice(0, 12) of `sha256:abc…` yields `sha256:abcde`, which is
 *  mostly prefix and does not distinguish two images. */
function candidateLabel(candidate: DockerCleanupCandidate): string {
  return candidate.repository ?? candidate.id.replace('sha256:', '').substring(0, 19)
}

interface PolicyDraft {
  enabled?: boolean
  minAgeHours?: number
  intervalHours?: number
}

/** The scheduled Docker cleanup policy, plus a preview of what a run would
 *  remove. The schedule is off until an operator turns it on: a prune is
 *  irreversible, so nothing here is opt-out. */
export function DockerCleanupCard() {
  const { data, isLoading, isError } = useDockerCleanupPolicy()
  const updatePolicy = useUpdateDockerCleanupPolicy()
  const preview = usePreviewDockerCleanup()
  const [draft, setDraft] = useState<PolicyDraft>({})

  if (isLoading) {
    return <div className="py-4"><LoadingSpinner /></div>
  }

  // Refuse rather than render a schedule nobody configured — the same rule
  // HistoryRetentionSection.tsx:46-66 follows for retention. A fabricated
  // "disabled, 168 hours" is indistinguishable from an operator having chosen
  // it, and this form is a WRITE-BACK surface: a Save from invented values
  // would persist them over the real policy.
  if (isError || !data) {
    return (
      <div className="space-y-2">
        <h3 className="text-lg font-medium">Docker cleanup</h3>
        <p className="text-sm text-destructive">
          The cleanup schedule could not be read, so it is not shown here. A scheduled run
          skips a policy it cannot read rather than pruning at a default.
        </p>
      </div>
    )
  }

  const enabled = draft.enabled ?? data.enabled
  const minAgeHours = draft.minAgeHours ?? data.minAgeHours
  const intervalHours = draft.intervalHours ?? data.intervalHours

  const ageBelowFloor = minAgeHours < data.minAllowedAgeHours
  const intervalBelowFloor = intervalHours < data.minAllowedIntervalHours
  const belowFloor = ageBelowFloor || intervalBelowFloor

  const dirty =
    enabled !== data.enabled ||
    minAgeHours !== data.minAgeHours ||
    intervalHours !== data.intervalHours

  const handleSave = () => {
    // Only what changed: every field of the PUT is optional, and sending an
    // unchanged interval re-arms the scheduler's ticker for no reason.
    const payload: PolicyDraft = {}
    if (enabled !== data.enabled) payload.enabled = enabled
    if (minAgeHours !== data.minAgeHours) payload.minAgeHours = minAgeHours
    if (intervalHours !== data.intervalHours) payload.intervalHours = intervalHours

    updatePolicy.mutate(payload, {
      onSuccess: () => {
        toast.success('Cleanup schedule updated')
        setDraft({})
      },
      // Takes the error: the PUT mints a distinct 400 per floor ("minAgeHours
      // must be at least N", "intervalHours must be at least N"), and a
      // zero-arity callback would render one sentence for both. The generic
      // sentence stays as the title when the server said nothing usable —
      // same branch shape as HistoryRetentionSection's onError.
      onError: (error) => {
        const cause = settingsSaveFault(error)
        if (cause) toast.error('Failed to update the cleanup schedule', { description: cause })
        else toast.error('Failed to update the cleanup schedule')
      },
    })
  }

  const handlePreview = () => {
    preview.mutate(minAgeHours, {
      // The preview validates the age floor through the same code path as the
      // PUT, so it can carry the same server sentence.
      onError: (error) => {
        const cause = settingsSaveFault(error)
        if (cause) toast.error('Failed to preview the cleanup', { description: cause })
        else toast.error('Failed to preview the cleanup')
      },
    })
  }

  return (
    <div className="space-y-6">
      <form onSubmit={(e) => { e.preventDefault(); handleSave() }} className="space-y-4">
        <div className="flex items-center gap-1.5">
          <h3 className="text-lg font-medium">Docker cleanup</h3>
          <HelpHint label="Docker cleanup" title="Docker cleanup" side="right">
            <p>
              Removes dangling images and build cache on a schedule. A dangling image is one
              no tag points at any more, usually a build that a newer build replaced.
            </p>
            <p>
              An image is removed only if it was created more than the age floor ago. The
              filter keys on creation time, not on when the image was last used.
            </p>
            <p className="text-xs">
              Minimum {data.minAllowedAgeHours} hours. Removed images cannot be recovered.
            </p>
          </HelpHint>
        </div>

        <div className="flex items-center gap-3">
          <Switch
            id="docker-cleanup-enabled"
            checked={enabled}
            onCheckedChange={(checked) => setDraft((d) => ({ ...d, enabled: checked }))}
          />
          <Label htmlFor="docker-cleanup-enabled">Run cleanup on a schedule</Label>
        </div>

        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 max-w-xl">
          <div className="space-y-1">
            <Label htmlFor="docker-cleanup-min-age">Age floor (hours)</Label>
            <Input
              id="docker-cleanup-min-age"
              type="number"
              min={data.minAllowedAgeHours}
              value={minAgeHours}
              onChange={(e) =>
                setDraft((d) => ({ ...d, minAgeHours: parseInt(e.target.value, 10) || 0 }))
              }
            />
            <p className="text-xs text-muted-foreground">
              Only images created more than this many hours ago are removed.
            </p>
          </div>

          <div className="space-y-1">
            <Label htmlFor="docker-cleanup-interval">Run every (hours)</Label>
            <Input
              id="docker-cleanup-interval"
              type="number"
              min={data.minAllowedIntervalHours}
              value={intervalHours}
              onChange={(e) =>
                setDraft((d) => ({ ...d, intervalHours: parseInt(e.target.value, 10) || 0 }))
              }
            />
            <p className="text-xs text-muted-foreground">
              How often the scheduled cleanup runs.
            </p>
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

        <Button type="submit" disabled={!dirty || belowFloor || updatePolicy.isPending}>
          {updatePolicy.isPending ? 'Saving…' : 'Save cleanup schedule'}
        </Button>
      </form>

      <div className="space-y-3 pt-4 border-t">
        <div>
          <h4 className="text-sm font-medium">Preview</h4>
          <p className="text-xs text-muted-foreground">
            What a run would remove at the age floor above. This removes nothing.
          </p>
        </div>

        <Button
          type="button"
          variant="outline"
          onClick={handlePreview}
          disabled={belowFloor || preview.isPending}
        >
          {preview.isPending ? 'Checking…' : 'Preview'}
        </Button>

        {preview.data &&
          (preview.data.candidates.length === 0 ? (
            <p className="text-sm text-muted-foreground">
              Nothing to remove: no dangling image was created more than{' '}
              {preview.data.minAgeHours} hours ago.
            </p>
          ) : (
            <div className="space-y-2">
              <p className="text-sm">
                {preview.data.candidates.length} image
                {preview.data.candidates.length === 1 ? '' : 's'}, reclaiming{' '}
                {formatBytes(preview.data.reclaimableBytes)}.
              </p>
              <ul className="space-y-1">
                {preview.data.candidates.map((candidate) => (
                  <li
                    key={candidate.id}
                    className="flex items-center justify-between gap-4 font-mono text-xs"
                  >
                    <span className="truncate">{candidateLabel(candidate)}</span>
                    <span className="shrink-0 text-muted-foreground">
                      {formatBytes(candidate.size)}
                    </span>
                  </li>
                ))}
              </ul>
            </div>
          ))}
      </div>
    </div>
  )
}
