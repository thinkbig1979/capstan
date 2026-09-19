import { useRef, useEffect } from 'react'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { ScrollArea } from '@/components/ui/scroll-area'
import {
  DatabaseBackup,
  RefreshCw,
  CheckCircle2,
  XCircle,
  AlertTriangle,
  Clock,
  HardDrive,
  CircleDashed,
} from 'lucide-react'
import { toast } from 'sonner'
import { useBackupStatus, useRunBackup, useBackupStreaming } from '@/hooks/useBackup'
import { queryKeys } from '@/lib/query-keys'
import { useQueryClient } from '@tanstack/react-query'
import { formatRelativeTime, formatBytes } from '@/lib/format'

function EngineUnavailableBanner({
  resticAvailable,
  repoStateMessage,
}: {
  resticAvailable: boolean
  repoStateMessage: string
}) {
  return (
    <div className="flex items-start gap-3 rounded-md border border-amber-200 bg-amber-50 p-3 dark:border-amber-800 dark:bg-amber-950/30">
      <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-amber-600 dark:text-amber-400" />
      <div className="text-sm">
        <p className="font-medium text-amber-800 dark:text-amber-300">Backup engine unavailable</p>
        <p className="mt-0.5 text-amber-700 dark:text-amber-400">
          {/* The server's own sentence, for the reason given in BackupToggle:
              this arm covers unreachable and settings_unreadable as well, and
              naming only "not initialised" would invite the one recovery that
              destroys data when the repository is merely unreadable. */}
          {!resticAvailable
            ? 'restic is not installed. Configure backups in Settings → Backup.'
            : `${repoStateMessage || 'Backup repository is unavailable.'} Configure backups in Settings → Backup.`}
        </p>
      </div>
    </div>
  )
}

function VerifyFailedBanner({
  startedAt,
  errorMessage,
}: {
  startedAt: string
  errorMessage?: string
}) {
  return (
    <div
      data-testid="verify-failed-banner"
      className="flex items-start gap-3 rounded-md border border-red-300 bg-red-50 p-3 dark:border-red-800 dark:bg-red-950/30"
    >
      <XCircle className="mt-0.5 h-4 w-4 shrink-0 text-red-600 dark:text-red-400" />
      <div className="text-sm">
        <p className="font-medium text-red-800 dark:text-red-300">
          Repository integrity check failed
        </p>
        <p className="mt-0.5 text-red-700 dark:text-red-400">
          Last checked {formatRelativeTime(startedAt)}.{errorMessage ? ` ${errorMessage}.` : ''}{' '}
          Backups may not be restorable until the repository is repaired.
        </p>
      </div>
    </div>
  )
}

function LastRunBadge({
  kind,
  status,
  stacksTotal,
}: {
  kind: string
  status: string
  stacksTotal: number
}) {
  if (kind === 'backup' && status === 'success' && stacksTotal === 0) {
    // agent-os-a9gi: a zero-policy backup run persists status="success" by
    // design (see TestRunBackup_NoRequestedIDsWithNoPoliciesIsUnchanged in
    // backend/internal/services/backup_test.go) -- nothing the operator asked
    // for was withheld, but rendering it as a green "Success" would mislead
    // an operator into believing backups actually ran. Same neutral/slate
    // treatment as the `interrupted` branch below: not a failure, just not
    // a meaningful success either.
    return (
      <Badge variant="outline" className="gap-1 border-slate-300 text-slate-600 dark:text-slate-400">
        <CircleDashed className="h-3 w-3" />
        No stacks backed up
      </Badge>
    )
  }
  if (status === 'success') {
    return (
      <Badge variant="outline" className="gap-1 border-green-300 text-green-700 dark:text-green-400">
        <CheckCircle2 className="h-3 w-3" />
        Success
      </Badge>
    )
  }
  if (status === 'failed') {
    return (
      <Badge variant="outline" className="gap-1 border-red-300 text-red-700 dark:text-red-400">
        <XCircle className="h-3 w-3" />
        Failed
      </Badge>
    )
  }
  if (status === 'partial') {
    return (
      <Badge variant="outline" className="gap-1 border-amber-300 text-amber-700 dark:text-amber-400">
        <AlertTriangle className="h-3 w-3" />
        Partial
      </Badge>
    )
  }
  if (status === 'running') {
    return (
      <Badge variant="outline" className="gap-1">
        <RefreshCw className="h-3 w-3 animate-spin" />
        Running
      </Badge>
    )
  }
  if (status === 'interrupted') {
    // Neutral/slate, not destructive-red: the run never reported a real
    // outcome (crash, or a restore from a mid-run snapshot) and may have
    // succeeded on the original instance, so "Failed" styling would mislead
    // an operator reading this right after recovering from an outage.
    return (
      <Badge variant="outline" className="gap-1 border-slate-300 text-slate-600 dark:text-slate-400">
        <CircleDashed className="h-3 w-3" />
        Interrupted
      </Badge>
    )
  }
  return null
}

export function BackupStatusCard() {
  const { data: statusData, isLoading } = useBackupStatus()
  const runBackupMutation = useRunBackup()
  const streaming = useBackupStreaming()
  const queryClient = useQueryClient()
  const logEndRef = useRef<HTMLDivElement>(null)

  const isBusy = runBackupMutation.isPending || streaming.status === 'running'

  // Auto-scroll log to bottom as new lines arrive
  useEffect(() => {
    logEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [streaming.lines])

  const handleBackUpNow = () => {
    streaming.reset()
    runBackupMutation.mutate(undefined, {
      onSuccess: (result) => {
        if (result.wsUrl) {
          streaming.connect(result.wsUrl, () => {
            // Invalidate after stream completes so status card refreshes
            queryClient.invalidateQueries({ queryKey: queryKeys.backup.status() })
            queryClient.invalidateQueries({ queryKey: queryKeys.backup.historyAll() })
          })
        } else {
          toast.success('Backup started')
          queryClient.invalidateQueries({ queryKey: queryKeys.backup.status() })
        }
      },
      onError: (err) => {
        const message = err instanceof Error ? err.message : 'Failed to start backup'
        toast.error(message)
      },
    })
  }

  if (isLoading) {
    return (
      <Card>
        <CardHeader className="pb-3">
          <CardTitle className="flex items-center gap-2 text-sm font-semibold">
            <DatabaseBackup className="h-4 w-4" />
            Backup Status
          </CardTitle>
        </CardHeader>
        <CardContent>
          <div className="space-y-2">
            <div className="h-4 w-2/3 animate-pulse rounded bg-muted" />
            <div className="h-4 w-1/2 animate-pulse rounded bg-muted" />
          </div>
        </CardContent>
      </Card>
    )
  }

  // "Can we back up RIGHT NOW", so `ok` is the only acceptable state: every
  // other one -- uninitialized, unreachable, settings_unreadable,
  // password_missing, wrong_password, and the empty "not probed" -- means a
  // backup would fail. Undefined statusData falls to unavailable, which is the
  // safe direction.
  //
  // Deliberately a NEGATIVE test against `ok` rather than a list of the failing
  // states, which is why agent-os-l04z's two new states needed no edit here: a
  // state added backend-side is refused automatically. The enumeration above is
  // documentation and must be kept honest, but nothing branches on it.
  const engineUnavailable =
    !statusData?.resticAvailable || statusData?.repoState !== 'ok'

  return (
    <Card>
      <CardHeader className="pb-3">
        <div className="flex items-center justify-between">
          <CardTitle className="flex items-center gap-2 text-sm font-semibold">
            <DatabaseBackup className="h-4 w-4" />
            Backup Status
          </CardTitle>
          <Button
            size="sm"
            variant="outline"
            className="h-7 text-xs"
            onClick={handleBackUpNow}
            disabled={isBusy || engineUnavailable}
            aria-label="Back up now"
          >
            {isBusy ? (
              <>
                <RefreshCw className="mr-1 h-3 w-3 animate-spin" />
                Running...
              </>
            ) : (
              <>
                <DatabaseBackup className="mr-1 h-3 w-3" />
                Back up now
              </>
            )}
          </Button>
        </div>
      </CardHeader>

      <CardContent className="space-y-4">
        {engineUnavailable && statusData && (
          <EngineUnavailableBanner
            resticAvailable={statusData.resticAvailable}
            repoStateMessage={statusData.repoStateMessage}
          />
        )}

        {/* Reads lastVerify, NEVER lastRun (agent-os-j1jw, agent-os-5lpz).
            lastRun is the newest run of ANY kind, so the next backup that
            succeeds would hide a failed verification behind it -- which is why
            the backend reports the two separately, pinned by
            TestGetStatus_LastVerifySurfacesFailure in
            backend/internal/handlers/backup_verify_test.go. Deliberately
            OUTSIDE the !engineUnavailable grid as well: the defect this warns
            about is a repository that answers the reachability probe while its
            data is gone, so neither a later success nor an `ok` probe may mask
            it. Only `failed` shows here; a verify run can also end
            `interrupted` (crash recovery), which is not evidence the
            repository is bad and would cry wolf after any outage. */}
        {statusData?.lastVerify?.status === 'failed' && (
          <VerifyFailedBanner
            startedAt={statusData.lastVerify.startedAt}
            errorMessage={statusData.lastVerify.errorMessage}
          />
        )}

        {/* Status grid */}
        {statusData && !engineUnavailable && (
          <div className="grid grid-cols-2 gap-3 text-sm sm:grid-cols-3">
            {/* Enabled stacks */}
            <div className="space-y-0.5">
              <p className="text-xs text-muted-foreground">Stacks enabled</p>
              <p className="font-medium">{statusData.enabledStackCount}</p>
            </div>

            {/* Last run */}
            <div className="space-y-0.5">
              <p className="text-xs text-muted-foreground">Last run</p>
              {statusData.lastRun ? (
                <div className="flex flex-col gap-0.5">
                  <LastRunBadge
                    kind={statusData.lastRun.kind}
                    status={statusData.lastRun.status}
                    stacksTotal={statusData.lastRun.stacksTotal}
                  />
                  <span className="text-xs text-muted-foreground">
                    {formatRelativeTime(statusData.lastRun.startedAt)}
                  </span>
                </div>
              ) : (
                <p className="text-muted-foreground">Never</p>
              )}
            </div>

            {/* Last check. Rendered only when a verify has actually run: there
                is no trigger UI yet, so a "Never" cell would be noise the
                operator cannot act on. */}
            {statusData.lastVerify && (
              <div className="space-y-0.5">
                <p className="text-xs text-muted-foreground">Last check</p>
                <div className="flex flex-col gap-0.5">
                  <LastRunBadge
                    kind={statusData.lastVerify.kind}
                    status={statusData.lastVerify.status}
                    stacksTotal={statusData.lastVerify.stacksTotal}
                  />
                  <span className="text-xs text-muted-foreground">
                    {formatRelativeTime(statusData.lastVerify.startedAt)}
                  </span>
                </div>
              </div>
            )}

            {/* Next scheduled run */}
            {statusData.nextRunAt && (
              <div className="space-y-0.5">
                <p className="text-xs text-muted-foreground">Next run</p>
                <div className="flex items-center gap-1">
                  <Clock className="h-3 w-3 text-muted-foreground" />
                  <span className="text-xs">{formatRelativeTime(statusData.nextRunAt)}</span>
                </div>
              </div>
            )}

            {/* Repo size */}
            {statusData.repoSizeBytes != null && (
              <div className="space-y-0.5">
                <p className="text-xs text-muted-foreground">Repo size</p>
                <div className="flex items-center gap-1">
                  <HardDrive className="h-3 w-3 text-muted-foreground" />
                  <span className="text-xs">{formatBytes(statusData.repoSizeBytes)}</span>
                </div>
              </div>
            )}

            {/* Scheduler */}
            <div className="space-y-0.5">
              <p className="text-xs text-muted-foreground">Scheduler</p>
              <Badge
                variant="outline"
                className={
                  statusData.schedulerRunning
                    ? 'gap-1 border-green-300 text-green-700 dark:text-green-400'
                    : 'gap-1 text-muted-foreground'
                }
              >
                {statusData.schedulerRunning ? (
                  <>
                    <CheckCircle2 className="h-3 w-3" />
                    Active
                  </>
                ) : (
                  'Off'
                )}
              </Badge>
            </div>
          </div>
        )}

        {/* Live streaming output */}
        {streaming.lines.length > 0 && (
          <div className="space-y-1">
            <p className="text-xs font-medium text-muted-foreground">Live output</p>
            <ScrollArea className="h-40 rounded-md border bg-muted/30">
              <div className="p-2 font-mono text-xs">
                {streaming.lines.map((line, i) => (
                  <div key={i} className="leading-relaxed whitespace-pre-wrap break-all">
                    {line}
                  </div>
                ))}
                <div ref={logEndRef} />
              </div>
            </ScrollArea>
            {streaming.status !== 'running' && (
              <Button
                variant="ghost"
                size="sm"
                className="h-6 text-xs text-muted-foreground"
                onClick={streaming.reset}
              >
                Clear
              </Button>
            )}
          </div>
        )}

        {/* Error summary when no lines yet */}
        {streaming.status === 'error' && streaming.error && streaming.lines.length === 0 && (
          <p className="text-xs text-destructive">{streaming.error}</p>
        )}

        {/* Refused stream, not a failed run (agent-os-mjrl): the backup is
            still going, this viewer was turned away at the per-run limit. */}
        {streaming.status === 'unavailable' && streaming.error && (
          <p className="text-xs text-muted-foreground">
            Live output unavailable: {streaming.error} The backup continues on the server.
          </p>
        )}
      </CardContent>
    </Card>
  )
}
