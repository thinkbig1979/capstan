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
} from 'lucide-react'
import { toast } from 'sonner'
import { useBackupStatus, useRunBackup, useBackupStreaming } from '@/hooks/useBackup'
import { queryKeys } from '@/lib/query-keys'
import { useQueryClient } from '@tanstack/react-query'
import { formatRelativeTime, formatBytes } from '@/lib/format'

function EngineUnavailableBanner({ resticAvailable }: { resticAvailable: boolean }) {
  return (
    <div className="flex items-start gap-3 rounded-md border border-amber-200 bg-amber-50 p-3 dark:border-amber-800 dark:bg-amber-950/30">
      <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-amber-600 dark:text-amber-400" />
      <div className="text-sm">
        <p className="font-medium text-amber-800 dark:text-amber-300">Backup engine unavailable</p>
        <p className="mt-0.5 text-amber-700 dark:text-amber-400">
          {!resticAvailable
            ? 'restic is not installed. Configure backups in Settings → Backup.'
            : 'Backup repository is not initialised. Configure backups in Settings → Backup.'}
        </p>
      </div>
    </div>
  )
}

function LastRunBadge({ status }: { status: string }) {
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
            queryClient.invalidateQueries({ queryKey: queryKeys.backup.history() })
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

  const engineUnavailable =
    !statusData?.resticAvailable || !statusData?.repositoryInitialized

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
          <EngineUnavailableBanner resticAvailable={statusData.resticAvailable} />
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
                  <LastRunBadge status={statusData.lastRun.status} />
                  <span className="text-xs text-muted-foreground">
                    {formatRelativeTime(statusData.lastRun.startedAt)}
                  </span>
                </div>
              ) : (
                <p className="text-muted-foreground">Never</p>
              )}
            </div>

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
      </CardContent>
    </Card>
  )
}
