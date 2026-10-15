import { useEffect, useRef, useState } from 'react'
import { useLocation } from 'react-router'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { LoadFailedNotice } from '@/components/LoadFailedNotice'
import { RefreshFailedNotice } from '@/components/RefreshFailedNotice'
import { useConfirm } from '@/hooks/useConfirm'
import {
  useDockerCleanupHistory,
  useDockerCleanupPolicy,
  useRunDockerCleanup,
  useScheduledCleanupPreview,
} from '@/hooks/useResources'
import { presentError, presentFault } from '@/lib/error-handler'
import { formatBytes, formatDateFull, formatRelativeTime } from '@/lib/format'
import type { DockerCleanupRun } from '@/types'
import { CleanupPolicyForm } from './cleanup/CleanupPolicyForm'
import { CleanupPreviewList } from './cleanup/CleanupPreviewList'
import { CleanupHistoryTable } from './cleanup/CleanupHistoryTable'
import {
  enabledCategoryNames,
  joinNames,
  policySentences,
  runFreedBytes,
  runRemovedSummary,
} from './cleanup/categories'

const AUTO_CLEANUP_ANCHOR = 'automatic-cleanup'

/** "in 5h" for an instant ahead. formatRelativeTime only reads the past. */
function formatTimeUntil(iso: string): string {
  const ms = new Date(iso).getTime() - Date.now()
  if (!Number.isFinite(ms)) return iso
  const minutes = Math.max(0, Math.round(ms / 60_000))
  if (minutes < 1) return 'in under a minute'
  if (minutes < 60) return `in ${minutes}m`
  const hours = Math.round(minutes / 60)
  if (hours < 48) return `in ${hours}h`
  return `in ${Math.round(hours / 24)}d`
}

/** The newest history row, in one line. */
function lastRunText(run: DockerCleanupRun | undefined): string {
  if (!run) return 'Never run'
  const when = formatRelativeTime(run.startedAt)
  if (run.status === 'skipped') return `Last run ${when} skipped: ${run.errorMessage ?? 'no reason recorded'}`
  if (run.status === 'failed') return `Last run ${when} failed: ${run.errorMessage ?? 'no reason recorded'}`
  return `Last run ${when} freed ${formatBytes(runFreedBytes(run))}`
}

function PreviewBody() {
  const preview = useScheduledCleanupPreview()
  if (preview.isPending) return <p className="text-sm text-muted-foreground">Checking...</p>
  if (!preview.data) {
    return <LoadFailedNotice what="what the next run would remove" onRetry={() => void preview.refetch()} />
  }
  return (
    <>
      {preview.isError && (
        <RefreshFailedNotice what="the cleanup preview" onRetry={() => void preview.refetch()} />
      )}
      <CleanupPreviewList preview={preview.data} />
    </>
  )
}

/** The automatic cleanup schedule on the Docker host page: what it removes,
 *  when it last and next runs, and the controls to preview, run and edit it. */
export function AutoCleanupPanel() {
  const policy = useDockerCleanupPolicy()
  const history = useDockerCleanupHistory()
  const runCleanup = useRunDockerCleanup()
  const { confirm, ConfirmComponent } = useConfirm()
  const [editOpen, setEditOpen] = useState<false | 'edit' | 'turn-on'>(false)
  const [previewOpen, setPreviewOpen] = useState(false)
  const sectionRef = useRef<HTMLElement>(null)
  const { hash } = useLocation()
  const hasPolicy = !!policy.data

  // /host#automatic-cleanup (the Free up space dialog's "Change timing", the
  // Settings notice) lands on this panel. The page scrolls inside <main>, and
  // scrollIntoView would drag the document too (eslint no-restricted-properties).
  useEffect(() => {
    const el = sectionRef.current
    if (hash !== `#${AUTO_CLEANUP_ANCHOR}` || !el || !hasPolicy) return
    const scroller = el.closest('main')
    if (scroller) scroller.scrollTop += el.getBoundingClientRect().top - scroller.getBoundingClientRect().top
  }, [hash, hasPolicy])

  const handleRun = async () => {
    const data = policy.data
    // Nothing to remove, so nothing to confirm: the button is disabled then.
    if (!data || enabledCategoryNames(data.categories).length === 0) return
    // Built from the stored policy, which is what the server runs, and the
    // floor named here is the floor sent.
    const confirmed = await confirm(
      'Run Docker cleanup now?',
      `This removes ${joinNames(enabledCategoryNames(data.categories))} older than ${data.minAgeHours} h. Removed items cannot be recovered. It runs even if the schedule is off.`,
      { confirmText: 'Run cleanup', isDangerous: true },
    )
    if (!confirmed) return
    runCleanup.mutate(data.minAgeHours, {
      onSuccess: (run) => {
        // A failed run normally arrives as a rejection (5xx), but the row's own
        // status is read too, so a failed or skipped row is never announced as
        // a success.
        if (run.status === 'failed') {
          presentFault('Cleanup failed', run.errorMessage ?? null)
          return
        }
        if (run.status === 'skipped') {
          presentFault('Cleanup skipped, nothing was removed', run.errorMessage ?? null)
          return
        }
        const removed = runRemovedSummary(run)
        toast.success(
          removed
            ? `Cleanup finished: ${removed} removed, ${formatBytes(runFreedBytes(run))} reclaimed`
            : `Cleanup finished: nothing removed, ${formatBytes(runFreedBytes(run))} reclaimed`,
        )
      },
      // A 409 CLEANUP_IN_PROGRESS carries the server's sentence; presentError keeps it.
      onError: (error) => presentError(error, { fallback: 'Failed to run the cleanup' }),
    })
  }

  let body
  if (policy.isPending) {
    body = <p className="text-sm text-muted-foreground">Loading...</p>
  } else if (!policy.data) {
    // No made-up schedule: a policy the server could not send is not "Off".
    body = (
      <LoadFailedNotice
        what="the cleanup schedule"
        consequence="Its controls are disabled until it loads."
        onRetry={() => void policy.refetch()}
      />
    )
  } else {
    const data = policy.data
    const copy = policySentences(data)
    const nothingOn = enabledCategoryNames(data.categories).length === 0
    body = (
      <div className="space-y-3">
        {policy.isError && (
          <RefreshFailedNotice what="the cleanup schedule" onRetry={() => void policy.refetch()} />
        )}
        {data.enabled ? (
          <div className="space-y-1 text-sm" data-testid="auto-cleanup-copy">
            <p>{copy.removes}</p>
            {copy.never && <p className="text-muted-foreground">{copy.never}</p>}
          </div>
        ) : (
          <p className="text-sm text-muted-foreground">
            Nothing is removed on a schedule until you turn it on.
          </p>
        )}
        <p className="text-sm text-muted-foreground" data-testid="auto-cleanup-runs">
          {history.isPending ? (
            'Checking the last run...'
          ) : !history.data ? (
            'The last run could not be read.'
          ) : (
            <span title={history.data.runs[0] ? formatDateFull(history.data.runs[0].startedAt) : undefined}>
              {lastRunText(history.data.runs[0])}
            </span>
          )}
          {data.enabled && data.nextRunAt && (
            <span title={formatDateFull(data.nextRunAt)}> · next run {formatTimeUntil(data.nextRunAt)}</span>
          )}
        </p>
        <div className="flex flex-wrap gap-2">
          <Button type="button" variant="outline" onClick={() => setPreviewOpen(true)}>
            Preview next run
          </Button>
          <Button
            type="button"
            variant="destructive"
            onClick={() => void handleRun()}
            disabled={runCleanup.isPending || nothingOn}
          >
            {runCleanup.isPending ? 'Running…' : 'Run now'}
          </Button>
          <Button type="button" variant="outline" onClick={() => setEditOpen('edit')}>
            Edit schedule
          </Button>
        </div>
        {nothingOn && (
          <p className="text-xs text-muted-foreground" data-testid="auto-cleanup-nothing-on">
            No category is switched on, so Run now has nothing to remove. Switch one on in Edit schedule.
          </p>
        )}
      </div>
    )
  }

  return (
    <section
      id={AUTO_CLEANUP_ANCHOR}
      ref={sectionRef}
      aria-labelledby="auto-cleanup-title"
      className="space-y-3 rounded-lg border bg-card p-4"
    >
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <h2 id="auto-cleanup-title" className="text-base font-semibold">Automatic cleanup</h2>
          {policy.data && (
            <Badge variant={policy.data.enabled ? 'default' : 'secondary'}>
              {policy.data.enabled ? 'On' : 'Off'}
            </Badge>
          )}
        </div>
        {policy.data && !policy.data.enabled && (
          <Button type="button" size="sm" onClick={() => setEditOpen('turn-on')}>
            Turn on
          </Button>
        )}
      </div>
      {body}
      <details className="text-sm">
        <summary className="cursor-pointer text-muted-foreground">Run history</summary>
        <div className="pt-3">
          <CleanupHistoryTable />
        </div>
      </details>

      <Dialog open={editOpen !== false} onOpenChange={(open) => { if (!open) setEditOpen(false) }}>
        <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-xl">
          <DialogHeader>
            <DialogTitle>Edit schedule</DialogTitle>
            <DialogDescription>What automatic cleanup removes, and how often.</DialogDescription>
          </DialogHeader>
          {editOpen !== false && (
            <CleanupPolicyForm
              startEnabled={editOpen === 'turn-on'}
              onSaved={() => setEditOpen(false)}
              onCancel={() => setEditOpen(false)}
            />
          )}
        </DialogContent>
      </Dialog>

      <Dialog open={previewOpen} onOpenChange={setPreviewOpen}>
        <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-xl">
          <DialogHeader>
            <DialogTitle>Preview next run</DialogTitle>
            <DialogDescription>
              What the next run would remove with the saved schedule. This removes nothing.
            </DialogDescription>
          </DialogHeader>
          {previewOpen && <PreviewBody />}
        </DialogContent>
      </Dialog>
      <ConfirmComponent />
    </section>
  )
}
