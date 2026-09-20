import { useState } from 'react'
import { toast } from 'sonner'
import { settingsSaveFault } from '@/lib/settings-save-fault'
import { presentFault } from '@/lib/error-handler'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { HelpHint } from '@/components/ui/help-hint'
import { LoadingSpinner } from '@/components/LoadingSkeleton'
import { Badge } from '@/components/ui/badge'
import {
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
} from '@/components/ui/table'
import { formatBytes, formatRelativeTime, formatDateFull } from '@/lib/format'
import {
  useDockerCleanupPolicy,
  useUpdateDockerCleanupPolicy,
  usePreviewDockerCleanup,
  useDockerCleanupHistory,
} from '@/hooks/useResources'
import type { DockerCleanupCandidate, DockerCleanupRun } from '@/types'
import { RefreshFailedNotice } from '@/components/RefreshFailedNotice'

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

/** What one run actually freed. Image bytes and build-cache bytes are recorded
 *  separately, and a run that only cleared cache has bytesReclaimed 0 — reading
 *  that field alone reports "0 B" for work that did happen. */
function runReclaimed(run: DockerCleanupRun): string {
  return formatBytes(run.bytesReclaimed + run.cacheBytesReclaimed)
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
  const { data, isLoading, isError, refetch } = useDockerCleanupPolicy()
  const updatePolicy = useUpdateDockerCleanupPolicy()
  const preview = usePreviewDockerCleanup()
  const history = useDockerCleanupHistory()
  const [draft, setDraft] = useState<PolicyDraft>({})

  if (isLoading) {
    return <div className="py-4"><LoadingSpinner /></div>
  }

  // Refuse a FABRICATED value; retain a REAL one and say the refresh failed —
  // the same rule HistoryRetentionSection follows for retention.
  //
  // agent-os-r1kc is why this branch exists: a fabricated "disabled, 168 hours"
  // is indistinguishable from an operator having chosen it, and this form is a
  // WRITE-BACK surface, so a Save from invented values would persist them over
  // the real policy.
  //
  // agent-os-wczm is why it is now keyed on `!data` alone. A policy the server
  // really sent is not fabricated, so a FAILED REFETCH over a populated form is
  // no reason to blank it — that discarded the operator's unsaved edits too.
  // `!data` keeps every refusal r1kc asked for; the refresh failure is reported
  // beside the Save button instead.
  if (!data) {
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
      // same presenter as HistoryRetentionSection's onError.
      onError: (error) => {
        // presentFault, NOT presentError (agent-os-5g8a): the cause is read by
        // settingsSaveFault, which is CODE-keyed and deliberately not
        // classifyError. The full argument, measured, is in presentFault's
        // docblock; the short form is that swapping the key is not this
        // change's decision to take.
        presentFault('Failed to update the cleanup schedule', settingsSaveFault(error))
      },
    })
  }

  const handlePreview = () => {
    preview.mutate(minAgeHours, {
      // The preview validates the age floor through the same code path as the
      // PUT, so it can carry the same server sentence.
      onError: (error) => {
        // presentFault, NOT presentError (agent-os-5g8a): the cause is read by
        // settingsSaveFault, which is CODE-keyed and deliberately not
        // classifyError. The full argument, measured, is in presentFault's
        // docblock; the short form is that swapping the key is not this
        // change's decision to take.
        presentFault('Failed to preview the cleanup', settingsSaveFault(error))
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

        {isError && (
          <RefreshFailedNotice
            what="the cleanup schedule"
            beforeSave
            onRetry={() => refetch()}
          />
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
          // The age floor only. The preview endpoint validates minAgeHours and
          // nothing else (cleanupMinAgeFromRequest, docker_cleanup.go), so an
          // interval the PUT would reject is no reason to refuse a preview.
          disabled={ageBelowFloor || preview.isPending}
        >
          {preview.isPending ? 'Checking…' : 'Preview'}
        </Button>

        {/* Both branches name the floor the SERVER echoed, never the one in the
            input above. The moment the operator edits that input the two
            diverge, and the list on screen belongs to the floor it was actually
            computed at — a stale list under a new number is a list of images
            the operator did not ask about. */}
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
                {preview.data.candidates.length === 1 ? '' : 's'} created more than{' '}
                {preview.data.minAgeHours} hours ago, reclaiming{' '}
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

      <div className="space-y-3 pt-4 border-t">
        <div>
          <h4 className="text-sm font-medium">Recent runs</h4>
          <p className="text-xs text-muted-foreground">
            What the cleanup has actually removed, newest first.
          </p>
        </div>

        {/* Three states, three sentences, none reusable for another: an
            unreadable history is not an empty one, and an operator's next move
            differs. Neither sentence may contain "could not be read" — that
            belongs to the policy-read failure above and the card must not say
            it twice. */}
        {/* agent-os-wczm: `!history.data` alone. A member-expression guard in a
            ternary, but the same defect — `history.isError` is true for a failed
            REFETCH too, so one 500 replaced a table of real runs with the
            sentence below. `!history.data` still covers the unreadable case. */}
        {history.isError && !!history.data && (
          <RefreshFailedNotice
            what="the cleanup run history"
            // agent-os-6iui: the one run-history table without a way out of a
            // stale view; every other one (backup history, update log, stack
            // updates, audit log, git history) offers a Retry.
            onRetry={() => history.refetch()}
          />
        )}
        {history.isLoading ? (
          <LoadingSpinner />
        ) : !history.data ? (
          <p className="text-sm text-destructive">Run history is unavailable.</p>
        ) : history.data.runs.length === 0 ? (
          <p className="text-sm text-muted-foreground">No cleanup runs yet.</p>
        ) : (
          <Table>
            <TableHeader>
              {/* No Finished and no Duration column, deliberately. finishedAt is
                  recorded but answers nothing an operator acts on, and it is the
                  one optional field left that a date formatter would turn into
                  "Invalid Date" when the wire omits it. */}
              <TableRow>
                <TableHead>Started</TableHead>
                <TableHead>Trigger</TableHead>
                <TableHead>Status</TableHead>
                <TableHead>Images removed</TableHead>
                <TableHead>Reclaimed</TableHead>
                <TableHead>Age floor</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {history.data.runs.map((run) => (
                <TableRow key={run.id}>
                  <TableCell title={formatDateFull(run.startedAt)}>
                    {formatRelativeTime(run.startedAt)}
                  </TableCell>
                  <TableCell>{run.trigger}</TableCell>
                  <TableCell>
                    <Badge variant={run.status === 'failed' ? 'destructive' : 'secondary'}>
                      {run.status}
                    </Badge>
                    {/* Guarded, never interpolated: errorMessage is omitted from
                        every successful run, and `Failed: ${run.errorMessage}`
                        would print "undefined" on each of them. */}
                    {run.errorMessage && (
                      <p className="mt-1 text-xs text-destructive">{run.errorMessage}</p>
                    )}
                  </TableCell>
                  <TableCell>{run.imagesDeleted}</TableCell>
                  <TableCell>{runReclaimed(run)}</TableCell>
                  <TableCell>{run.minAgeHours} h</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </div>
    </div>
  )
}
