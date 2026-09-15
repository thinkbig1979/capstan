import { useState, useCallback } from 'react'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { LoadingSpinner } from '@/components/LoadingSkeleton'
import { EmptyState } from '@/components/EmptyState'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { RunStatusBadge } from '@/components/dashboard/backup-run-status'
import {
  useBackupSnapshots,
  usePreviewSnapshot,
  useRestore,
  useStackBackupRuns,
  useBackupPolicies,
  useBackupStreaming,
  type BackupStreamStatus,
} from '@/hooks/useBackup'
import { queryKeys } from '@/lib/query-keys'
import { useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'
import {
  Archive,
  RotateCcw,
  Eye,
  CheckCircle2,
  XCircle,
  AlertCircle,
  ChevronDown,
  ChevronUp,
  Loader2,
  X,
} from 'lucide-react'
import { cn } from '@/lib/utils'
import type { BackupSnapshot, BackupRun } from '@/types'
import { useTextFilter } from '@/hooks/useTextFilter'
import { TableSearch } from '@/components/ui/table-search'

const SNAPSHOT_SEARCH_FIELDS = [
  (s: BackupSnapshot) => s.shortId,
  (s: BackupSnapshot) => s.id,
  (s: BackupSnapshot) => s.time,
  (s: BackupSnapshot) => s.tags.join(' '),
  (s: BackupSnapshot) => s.paths.join(' '),
]

const RUN_SEARCH_FIELDS = [
  (r: BackupRun) => r.id,
  (r: BackupRun) => r.startedAt,
  (r: BackupRun) => r.kind,
  (r: BackupRun) => r.trigger,
  (r: BackupRun) => r.status,
]

// ─── helpers ─────────────────────────────────────────────────────────────────

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
  return `${(bytes / (1024 * 1024 * 1024)).toFixed(2)} GB`
}

function formatDate(iso: string): string {
  try {
    return new Intl.DateTimeFormat(undefined, {
      dateStyle: 'medium',
      timeStyle: 'short',
    }).format(new Date(iso))
  } catch {
    return iso
  }
}

/**
 * The snapshots query answers a repository fault with one of THREE codes, all
 * minted in backend/internal/handlers/backup.go:
 *
 *   BACKUP_REPO_UNREACHABLE   503, repoFault()         — configured, not readable
 *   BACKUP_REPO_UNINITIALIZED 409, repoUninitialized() — no repository exists yet
 *   BACKUP_UNAVAILABLE        409, listSnapshots       — restic itself is missing
 *
 * The first two carry `details.repoState`, which of the states holds, plus
 * `message`, the cause sentence CheckRepository already computed. The third
 * carries `details.cause` and NO repoState, deliberately: on that path
 * CheckRepository is never called, so no repository state was ever obtained and
 * claiming one would be a fabrication. The axios interceptor in lib/api.ts
 * rejects with the body spread verbatim, so all of it arrives here untouched.
 *
 * Keys on `code` and `repoState`, never on the status: the code is what the
 * backend treats as the client's branch point, and a status check here would go
 * stale the moment the same fault were reported under a different status — which
 * is exactly what happened to the uninitialised state, which used to arrive as a
 * 500 and now arrives as a 409.
 *
 * Deliberately NOT routed through classifyError(). Its 5xx branch discards
 * `message` and answers "503: Something went wrong on the server" — a status
 * code, where an operator needs a cause and a recovery. The recovery is the
 * whole point, and the recoveries here are mutually exclusive: an unreachable
 * repository is fixed by checking the remote or the mount, an uninitialised one
 * by initialising, a password fault by correcting a credential — and offering
 * any of the others for an unreachable repository is destructive-adjacent.
 */
function repoFaultFrom(
  error: unknown,
): { title: string; hint: string; detail: string } | null {
  if (!error || typeof error !== 'object') return null
  const body = error as { code?: string; message?: string; details?: { repoState?: string } }

  const detail = body.message ?? ''

  // Restic itself is missing, so nothing about the repository is known and
  // nothing about it is claimed. Before agent-os-9f5c this path answered 200
  // with an empty array and rendered as the ordinary empty state — including
  // "Run a backup to create the first one", which cannot work when the binary
  // that would run it is absent.
  if (body.code === 'BACKUP_UNAVAILABLE') {
    return {
      title: 'The backup engine is not available.',
      hint: 'Capstan could not find the restic binary, so it could not read the repository or tell you whether any snapshots exist. This is a deployment problem rather than a settings one.',
      detail,
    }
  }

  // A different CODE, not merely a different repoState: this function returned
  // null for anything but BACKUP_REPO_UNREACHABLE, so a new code needs its own
  // arm regardless. Initialising is the correct recovery here and ONLY here.
  if (body.code === 'BACKUP_REPO_UNINITIALIZED') {
    return {
      title: 'The backup repository does not exist yet.',
      hint: 'Nothing has been lost. Initialise the repository in Backup settings, then run a backup.',
      detail,
    }
  }

  if (body.code !== 'BACKUP_REPO_UNREACHABLE') return null

  switch (body.details?.repoState) {
    case 'password_missing':
      return {
        title: 'No restic password is configured.',
        hint: 'The repository was never contacted, so nothing is known to be wrong with it. Set the restic password in Backup settings — the remote and the mount are not the problem.',
        detail,
      }
    case 'wrong_password':
      return {
        title: 'The restic password was rejected.',
        hint: 'The repository answered and your snapshots are not missing; the credential is what is wrong. Correct the restic password in Backup settings — do not initialise a new repository over this one.',
        detail,
      }
    case 'unreachable':
      return {
        title: 'The backup repository could not be reached.',
        hint: 'Your snapshots are not missing. The repository did not answer, so check the remote or the mount — initialising a new repository would not bring them back.',
        detail,
      }
    case 'settings_unreadable':
      return {
        title: 'Backup repository state is unknown.',
        hint: 'Capstan could not load the backup settings, so it cannot tell which repository this stack uses. Check the Backup settings.',
        detail,
      }
    default:
      // Every specific state is a POSITIVE arm and this is the fallback,
      // deliberately that way round. Prescribing a recovery by default means a
      // repoState added backend-side would be answered with advice written for a
      // different fault, silently and with no failing test — tsc cannot help
      // here, because details.repoState is a plain string and this switch has a
      // default, so the compiler sees no missing case however many states the
      // backend mints. Say only what the code itself carries.
      return {
        title: 'The backup repository could not be read.',
        hint: 'Capstan could not determine the cause. Check the Backup settings and the repository.',
        detail,
      }
  }
}

// ─── Preview panel ────────────────────────────────────────────────────────────

function PreviewPanel({ snapshotId, onClose }: { snapshotId: string; onClose: () => void }) {
  const { data, isLoading, isError } = usePreviewSnapshot(snapshotId)

  return (
    <div className="mt-2 rounded-lg border bg-muted/40">
      <div className="flex items-center justify-between px-4 py-2 border-b">
        <span className="text-sm font-medium">Preview — {snapshotId.slice(0, 8)}</span>
        <Button variant="ghost" size="sm" onClick={onClose} className="h-6 w-6 p-0" aria-label="Close preview">
          <X className="h-3.5 w-3.5" />
        </Button>
      </div>
      <div className="max-h-64 overflow-y-auto p-3 font-mono text-xs leading-relaxed">
        {isLoading && (
          <div className="flex items-center gap-2 text-muted-foreground">
            <LoadingSpinner size="small" />
            Loading preview…
          </div>
        )}
        {isError && (
          <div className="flex items-center gap-2 text-destructive">
            <AlertCircle className="h-4 w-4" />
            Failed to load preview.
          </div>
        )}
        {data && data.entries.length === 0 && (
          <span className="text-muted-foreground">No entries found in snapshot.</span>
        )}
        {data && data.entries.map((entry) => (
          <div key={entry} className="whitespace-pre-wrap break-all text-foreground/80">{entry}</div>
        ))}
      </div>
    </div>
  )
}

// ─── Restore progress ─────────────────────────────────────────────────────────

function RestoreProgress({
  status,
  lines,
  error,
  onDismiss,
}: {
  status: BackupStreamStatus
  lines: string[]
  error: string | null
  onDismiss: () => void
}) {
  if (status === 'idle') return null

  const isRunning = status === 'running'
  const isDone = status === 'success' || status === 'partial' || status === 'error' || status === 'unavailable'
  // 'unavailable' is a refused STREAM, not a failed run (agent-os-mjrl): the
  // restore is still going on the server, this viewer was turned away because
  // the run already has its maximum number of live viewers. Neutral styling,
  // and the reason (which names the limit and the action) is shown as a note.
  const isUnavailable = status === 'unavailable'

  function headerLabel() {
    if (isRunning) return 'Restoring…'
    if (status === 'success') return 'Restore completed'
    if (status === 'partial') return 'Restore partially completed'
    if (isUnavailable) return 'Live output unavailable'
    return 'Restore failed'
  }

  return (
    <div className={cn(
      'mt-4 rounded-lg border overflow-hidden',
      status === 'success' && 'border-green-500/30',
      status === 'partial' && 'border-yellow-500/30',
      status === 'error' && 'border-destructive/30',
      isUnavailable && 'border-muted-foreground/30',
      isRunning && 'border-blue-500/30',
    )}>
      <div className={cn(
        'flex items-center justify-between px-4 py-2 text-sm font-medium',
        isRunning && 'bg-blue-500/10 text-blue-700 dark:text-blue-400',
        status === 'success' && 'bg-green-500/10 text-green-700 dark:text-green-400',
        status === 'partial' && 'bg-yellow-500/10 text-yellow-700 dark:text-yellow-400',
        status === 'error' && 'bg-destructive/10 text-destructive',
        isUnavailable && 'bg-muted text-muted-foreground',
      )}>
        <div className="flex items-center gap-2">
          {isRunning && <Loader2 className="h-4 w-4 animate-spin" />}
          {status === 'success' && <CheckCircle2 className="h-4 w-4" />}
          {status === 'partial' && <AlertCircle className="h-4 w-4" />}
          {status === 'error' && <XCircle className="h-4 w-4" />}
          {isUnavailable && <AlertCircle className="h-4 w-4" />}
          <span data-testid="restore-progress-header">{headerLabel()}</span>
          {isRunning && lines.length > 0 && (
            <span className="text-xs opacity-60">({lines.length} lines)</span>
          )}
        </div>
        {isDone && (
          <Button variant="ghost" size="sm" onClick={onDismiss} className="h-6 px-2">
            <X className="h-3 w-3" />
          </Button>
        )}
      </div>
      <div className="max-h-64 overflow-y-auto bg-terminal-background text-terminal-foreground p-3 font-mono text-xs leading-relaxed">
        {lines.map((line, i) => (
          <div
            key={`${i}-${line.slice(0, 20)}`}
            className={cn(
              'whitespace-pre-wrap break-all',
              line.startsWith('Error:') && 'text-destructive',
              line.startsWith('---') && 'text-blue-400',
            )}
          >
            {line}
          </div>
        ))}
        {isRunning && (
          <div className="text-terminal-foreground/60">
            <span className="animate-pulse">_</span>
          </div>
        )}
      </div>
      {error && isUnavailable && (
        <div
          data-testid="restore-progress-unavailable"
          className="px-4 py-2 text-xs text-muted-foreground bg-muted/40 border-t"
        >
          {error} The restore continues on the server; check Recent runs for its result.
        </div>
      )}
      {error && !isUnavailable && (
        <div className="px-4 py-2 text-xs text-destructive bg-destructive/5 border-t border-destructive/20">
          {error}
        </div>
      )}
    </div>
  )
}

// ─── Snapshot row ─────────────────────────────────────────────────────────────

function SnapshotRow({
  snapshot,
  onRestore,
  isRestoring,
}: {
  snapshot: BackupSnapshot
  onRestore: (snapshot: BackupSnapshot) => void
  isRestoring: boolean
}) {
  const [previewOpen, setPreviewOpen] = useState(false)

  return (
    <>
      <tr className="border-b last:border-0 hover:bg-muted/30 transition-colors">
        <td className="py-3 px-4">
          <span className="font-mono text-xs text-foreground">{snapshot.shortId}</span>
        </td>
        <td className="py-3 px-4 text-sm text-muted-foreground">
          {formatDate(snapshot.time)}
        </td>
        <td className="py-3 px-4">
          <div className="flex flex-wrap gap-1">
            {snapshot.tags.length > 0
              ? snapshot.tags.map((tag) => (
                  <Badge key={tag} variant="secondary" className="text-xs font-normal">
                    {tag}
                  </Badge>
                ))
              : <span className="text-xs text-muted-foreground">—</span>
            }
          </div>
        </td>
        <td className="py-3 px-4 text-sm text-muted-foreground">
          {snapshot.sizeBytes != null ? formatBytes(snapshot.sizeBytes) : '—'}
        </td>
        <td className="py-3 px-4">
          <div className="flex items-center gap-2 justify-end">
            <Button
              variant="ghost"
              size="sm"
              onClick={() => setPreviewOpen((v) => !v)}
              className="h-7 px-2 text-xs"
              aria-label={previewOpen ? 'Hide preview' : 'Show preview'}
            >
              <Eye className="mr-1 h-3.5 w-3.5" />
              {previewOpen
                ? <><span>Preview</span> <ChevronUp className="ml-1 h-3 w-3" /></>
                : <><span>Preview</span> <ChevronDown className="ml-1 h-3 w-3" /></>
              }
            </Button>
            <Button
              variant="outline"
              size="sm"
              onClick={() => onRestore(snapshot)}
              disabled={isRestoring}
              className="h-7 px-2 text-xs"
              aria-label={`Restore snapshot ${snapshot.shortId}`}
            >
              <RotateCcw className="mr-1 h-3.5 w-3.5" />
              Restore
            </Button>
          </div>
        </td>
      </tr>
      {previewOpen && (
        <tr>
          <td colSpan={5} className="px-4 pb-3">
            <PreviewPanel snapshotId={snapshot.id} onClose={() => setPreviewOpen(false)} />
          </td>
        </tr>
      )}
    </>
  )
}

// ─── Main component ───────────────────────────────────────────────────────────

interface BackupsTabProps {
  stackId: string
}

export function BackupsTab({ stackId }: BackupsTabProps) {
  const queryClient = useQueryClient()

  // Backup enabled check
  const { data: policiesData } = useBackupPolicies()
  const policy = policiesData?.policies?.find((p) => p.targetId === stackId)
  const backupEnabled = policy?.enabled ?? false

  // Snapshots
  const {
    data: snapshots,
    isLoading: snapshotsLoading,
    isError: snapshotsError,
    error: snapshotsErrorCause,
  } = useBackupSnapshots(stackId)
  const repoFault = repoFaultFrom(snapshotsErrorCause)

  // Recent runs (global history — all runs are relevant when backup is enabled)
  const { runs, isLoading: runsLoading } = useStackBackupRuns(stackId, 20)

  // Restore mutation
  const restoreMutation = useRestore()

  // Streaming output for restore progress
  const stream = useBackupStreaming()

  // Text filters
  const { query: snapshotQuery, setQuery: setSnapshotQuery, filtered: filteredSnapshots } =
    useTextFilter(snapshots ?? [], SNAPSHOT_SEARCH_FIELDS)
  const { query: runQuery, setQuery: setRunQuery, filtered: filteredRuns } =
    useTextFilter(runs, RUN_SEARCH_FIELDS)

  // Confirm dialog state
  const [confirmOpen, setConfirmOpen] = useState(false)
  const [pendingSnapshot, setPendingSnapshot] = useState<BackupSnapshot | null>(null)

  const handleRestoreRequest = useCallback((snapshot: BackupSnapshot) => {
    setPendingSnapshot(snapshot)
    setConfirmOpen(true)
  }, [])

  const handleRestoreConfirm = useCallback(() => {
    if (!pendingSnapshot) return
    const snapshot = pendingSnapshot
    setPendingSnapshot(null)

    restoreMutation.mutate(
      { stackId, snapshotId: snapshot.id },
      {
        onSuccess: (data) => {
          // Strip /api/v1 prefix if present — WSClient prepends it
          const wsPath = data.wsUrl.startsWith('/api/v1')
            ? data.wsUrl.slice('/api/v1'.length)
            : data.wsUrl

          stream.connect(wsPath, (finalStatus) => {
            queryClient.invalidateQueries({ queryKey: queryKeys.backup.snapshots(stackId) })
            queryClient.invalidateQueries({ queryKey: queryKeys.backup.status() })
            queryClient.invalidateQueries({ queryKey: queryKeys.backup.historyAll() })
            queryClient.invalidateQueries({ queryKey: queryKeys.stack.detail(stackId) })
            // Derive the toast from the real terminal outcome — a failed or
            // partial restore must not be reported as a green success.
            if (finalStatus === 'success') {
              toast.success('Restore completed')
            } else if (finalStatus === 'partial') {
              toast.warning('Restore partially completed — check the log')
            } else if (finalStatus === 'unavailable') {
              // Refused stream, not a failed run (agent-os-mjrl).
              toast.info('Restore is still running; live output is unavailable')
            } else {
              toast.error('Restore failed — check the log for details')
            }
          })
        },
        onError: (err) => {
          const message = err instanceof Error ? err.message : 'Failed to start restore'
          toast.error(`Restore failed: ${message}`)
        },
      },
    )
  }, [pendingSnapshot, stackId, restoreMutation, stream, queryClient])

  const isRestoring = stream.status === 'running' || restoreMutation.isPending

  // Reconcile the two panels: if backups have succeeded but no snapshots are listed, the empty
  // state must not imply nothing has happened (the ST-3 contradiction).
  const hasSuccessfulBackupRuns = runs.some(
    (r) => r.kind === 'backup' && (r.status === 'success' || r.status === 'partial') && r.stacksOk > 0,
  )

  // ── Render ──────────────────────────────────────────────────────────────────

  return (
    <div className="space-y-8">

      {/* ── Backup not enabled notice ────────────────────────────────────────── */}
      {!backupEnabled && (
        <div className="flex items-start gap-3 rounded-lg border border-yellow-500/30 bg-yellow-500/10 p-4">
          <AlertCircle className="h-5 w-5 text-yellow-600 dark:text-yellow-400 shrink-0 mt-0.5" />
          <p className="text-sm text-yellow-700 dark:text-yellow-300">
            Backup is not enabled for this stack. Enable it from the Overview tab.
          </p>
        </div>
      )}

      {/* ── Snapshots ────────────────────────────────────────────────────────── */}
      <section>
        <h3 className="mb-3 text-base font-semibold flex items-center gap-2">
          <Archive className="h-4 w-4 text-muted-foreground" />
          Snapshots
        </h3>

        {snapshotsLoading && (
          <div className="flex items-center gap-2 text-sm text-muted-foreground py-4">
            <LoadingSpinner size="small" />
            Loading snapshots…
          </div>
        )}

        {snapshotsError && (
          repoFault ? (
            <div className="flex items-start gap-2 text-sm py-4">
              <AlertCircle className="h-4 w-4 shrink-0 mt-0.5 text-destructive" />
              <div>
                <p className="text-destructive font-medium">{repoFault.title}</p>
                <p className="text-muted-foreground mt-1 max-w-prose">{repoFault.hint}</p>
                {repoFault.detail && (
                  <p className="text-muted-foreground mt-1 break-all font-mono text-xs">
                    {repoFault.detail}
                  </p>
                )}
              </div>
            </div>
          ) : (
            <div className="flex items-center gap-2 text-sm text-destructive py-4">
              <AlertCircle className="h-4 w-4" />
              Failed to load snapshots.
            </div>
          )
        )}

        {!snapshotsLoading && !snapshotsError && (!snapshots || snapshots.length === 0) && (
          hasSuccessfulBackupRuns ? (
            /* The hedge is GONE, and removing it is the point rather than a
               tidy-up. agent-os-eo4u tried this and reverted, correctly at the
               time: listSnapshots still answered 200 with an EMPTY ARRAY when
               restic was absent, from an early return sitting BEFORE
               CheckRepository, so that path landed here with availability
               genuinely unknown and "the repository answered" would have been
               false on it.

               agent-os-9f5c deleted that path. Reaching this branch now requires
               a 200, and the handler emits one only after CheckRepository
               returned RepoStateOK and restic listed successfully — every other
               state is a named 409 or 503 into the error branch above, and
               useBackupSnapshots has no error fallback that could turn a
               rejection into an empty array. So the repository demonstrably
               answered, and the remaining explanations are retention, a reset or
               re-initialised repository, or runs that wrote to a different
               repository than the one configured now. Saying "may be
               unavailable" here would be the wrong-fault-named defect this wave
               exists to remove, pointing an operator at a repository that is
               working. */
            <EmptyState
              title="No snapshots listed"
              description="Recent runs report success and the repository answered, but it holds no snapshots for this stack. They may have been pruned by retention, or the repository may have been reset or repointed since those runs."
            />
          ) : (
            <EmptyState
              title="No snapshots yet"
              description="No restic snapshots found for this stack. Run a backup to create the first one."
            />
          )
        )}

        {snapshots && snapshots.length > 0 && (
          <>
            <div className="mb-3 flex items-center gap-3">
              <TableSearch
                value={snapshotQuery}
                onChange={setSnapshotQuery}
                placeholder="Filter snapshots…"
                className="w-full sm:w-56"
              />
              {snapshotQuery && filteredSnapshots.length === 0 && (
                <span className="text-sm text-muted-foreground">No snapshots match.</span>
              )}
            </div>
            <div className="rounded-lg border overflow-hidden">
              <table className="w-full text-sm">
                <thead>
                  <tr className="border-b bg-muted/50">
                    <th className="py-2 px-4 text-left font-medium text-muted-foreground">ID</th>
                    <th className="py-2 px-4 text-left font-medium text-muted-foreground">Time</th>
                    <th className="py-2 px-4 text-left font-medium text-muted-foreground">Tags</th>
                    <th className="py-2 px-4 text-left font-medium text-muted-foreground">Size</th>
                    <th className="py-2 px-4 text-right font-medium text-muted-foreground">Actions</th>
                  </tr>
                </thead>
                <tbody>
                  {filteredSnapshots.map((snapshot) => (
                    <SnapshotRow
                      key={snapshot.id}
                      snapshot={snapshot}
                      onRestore={handleRestoreRequest}
                      isRestoring={isRestoring}
                    />
                  ))}
                </tbody>
              </table>
            </div>
          </>
        )}

        {/* Restore streaming output */}
        <RestoreProgress
          status={stream.status}
          lines={stream.lines}
          error={stream.error}
          onDismiss={stream.reset}
        />
      </section>

      {/* ── Recent runs ──────────────────────────────────────────────────────── */}
      <section>
        <h3 className="mb-3 text-base font-semibold flex items-center gap-2">
          <RotateCcw className="h-4 w-4 text-muted-foreground" />
          Recent runs
        </h3>

        {runsLoading && (
          <div className="flex items-center gap-2 text-sm text-muted-foreground py-4">
            <LoadingSpinner size="small" />
            Loading runs…
          </div>
        )}

        {!runsLoading && runs.length === 0 && (
          <EmptyState
            title="No backup runs yet"
            description="Backup run history will appear here once backups have been executed."
          />
        )}

        {runs.length > 0 && (
          <>
            <div className="mb-3 flex items-center gap-3">
              <TableSearch
                value={runQuery}
                onChange={setRunQuery}
                placeholder="Filter runs…"
                className="w-full sm:w-56"
              />
              {runQuery && filteredRuns.length === 0 && (
                <span className="text-sm text-muted-foreground">No runs match.</span>
              )}
            </div>
            <div className="rounded-lg border overflow-hidden">
              <table className="w-full text-sm">
                <thead>
                  <tr className="border-b bg-muted/50">
                    <th className="py-2 px-4 text-left font-medium text-muted-foreground">Started</th>
                    <th className="py-2 px-4 text-left font-medium text-muted-foreground">Kind</th>
                    <th className="py-2 px-4 text-left font-medium text-muted-foreground">Trigger</th>
                    <th className="py-2 px-4 text-left font-medium text-muted-foreground">Status</th>
                    <th className="py-2 px-4 text-left font-medium text-muted-foreground">
                      <span title="New data written to the repository. restic deduplicates, so an unchanged backup adds little or nothing.">New data</span>
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {filteredRuns.map((run) => (
                    <tr key={run.id} className="border-b last:border-0 hover:bg-muted/30 transition-colors">
                      <td className="py-3 px-4 text-muted-foreground">{formatDate(run.startedAt)}</td>
                      <td className="py-3 px-4 capitalize">{run.kind}</td>
                      <td className="py-3 px-4 capitalize text-muted-foreground">{run.trigger}</td>
                      <td className="py-3 px-4">
                        <RunStatusBadge status={run.status} />
                      </td>
                      <td className="py-3 px-4 text-muted-foreground">
                        {run.bytesAdded == null ? (
                          <span title="Not recorded">—</span>
                        ) : run.bytesAdded === 0 ? (
                          'No change'
                        ) : (
                          formatBytes(run.bytesAdded)
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </>
        )}
      </section>

      {/* ── Restore confirm dialog ────────────────────────────────────────────── */}
      <ConfirmDialog
        open={confirmOpen}
        onOpenChange={setConfirmOpen}
        title="Restore snapshot"
        description={
          pendingSnapshot
            ? `Restore snapshot ${pendingSnapshot.shortId} (${formatDate(pendingSnapshot.time)})? This will overwrite the current stack data with the snapshot contents. The stack will be stopped during restore.`
            : 'Restore this snapshot? This operation is destructive and cannot be undone.'
        }
        confirmText="Restore"
        onConfirm={handleRestoreConfirm}
        isDangerous
      />
    </div>
  )
}
