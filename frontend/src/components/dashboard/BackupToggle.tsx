import { useState, useEffect } from 'react'
import { Switch } from '@/components/ui/switch'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'
import { Select, SelectContent, SelectItem, SelectTrigger } from '@/components/ui/select'
import { Lock, CheckCircle2, XCircle, CircleDashed, AlertTriangle } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { useToggleBackup, useBackupPolicies, useBackupStatus } from '@/hooks/useBackup'
import { presentError } from '@/lib/error-handler'
import type { BackupPolicy, BackupRun } from '@/types'
import { RUN_KIND_LABEL } from './backup-run-kind'

/**
 * The icon and sentence for the last run, or null for none. Names the run's
 * kind because lastRun is the newest run of ANY kind (agent-os-4zx0): a failed
 * restore used to read "Last backup failed".
 */
function lastRunIndicator(run: BackupRun) {
  const kind = RUN_KIND_LABEL[run.kind]
  switch (run.status) {
    case 'success':
      // Same case BackupStatusCard's LastRunBadge renders as "No stacks backed
      // up" (agent-os-a9gi): nothing failed, but nothing was backed up either.
      if (run.kind === 'backup' && run.stacksTotal === 0) {
        return { Icon: CircleDashed, className: 'text-muted-foreground', text: 'Last backup ran with no stacks' }
      }
      return { Icon: CheckCircle2, className: 'text-green-500', text: `Last ${kind} succeeded` }
    case 'failed':
      return { Icon: XCircle, className: 'text-destructive', text: `Last ${kind} failed` }
    case 'partial':
      return {
        Icon: AlertTriangle,
        className: 'text-amber-600 dark:text-amber-400',
        text: `Last ${kind} partly failed`,
      }
    case 'interrupted':
      // Neutral, not destructive-red: the run never reported a real outcome
      // (crash, or a restore from a mid-run snapshot) and may have succeeded on
      // the original instance.
      return { Icon: CircleDashed, className: 'text-muted-foreground', text: `Last ${kind} was interrupted` }
    case 'running':
      return null
  }
}

interface BackupToggleProps {
  stackId: string
  /**
   * Render the last-run status icon. Defaults to true. A multi-row table sets
   * this to false: the underlying value is install-wide, not per-stack (see the
   * comment on lastRunStatus below), so every row would show the same icon and
   * assert an outcome for stacks that have no backup policy at all.
   */
  showLastRunStatus?: boolean
}

export function BackupToggle({ stackId, showLastRunStatus = true }: BackupToggleProps) {
  const { data: statusData } = useBackupStatus()
  const {
    data: policiesData,
    isError: policiesError,
    refetch: refetchPolicies,
  } = useBackupPolicies()
  const toggleMutation = useToggleBackup()

  const policy: BackupPolicy | undefined = policiesData?.policies?.find(
    (p) => p.targetId === stackId,
  )

  const [optimisticEnabled, setOptimisticEnabled] = useState(policy?.enabled ?? false)
  const [optimisticStopPolicy, setOptimisticStopPolicy] = useState<'stop' | 'hot'>(
    policy?.stopPolicy ?? 'stop',
  )

  // Sync optimistic state when the server-side policy changes (e.g. after
  // invalidation). Both values are batched in a single effect to avoid
  // cascading renders.
  useEffect(() => {
    const syncState = () => {
      setOptimisticEnabled(policy?.enabled ?? false)
      setOptimisticStopPolicy(policy?.stopPolicy ?? 'stop')
    }
    syncState()
  }, [policy?.enabled, policy?.stopPolicy])

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

  const handleToggle = (checked: boolean) => {
    setOptimisticEnabled(checked)
    toggleMutation.mutate(
      { stackId, enabled: checked, stopPolicy: optimisticStopPolicy },
      {
        onError: (err) => {
          setOptimisticEnabled(!checked)
          presentError(err, { fallback: 'Failed to toggle backup' })
        },
      },
    )
  }

  const handleStopPolicyChange = (value: 'stop' | 'hot') => {
    setOptimisticStopPolicy(value)
    toggleMutation.mutate(
      { stackId, enabled: optimisticEnabled, stopPolicy: value },
      {
        onError: (err) => {
          setOptimisticStopPolicy(optimisticStopPolicy)
          presentError(err, { fallback: 'Failed to update stop policy' })
        },
      },
    )
  }

  // NOT scoped to this stack. statusData.lastRun is the single most recent run
  // across the whole install, of any kind, so it may be a restore, a prune, or a
  // backup of a different stack. Every stack's BackupToggle therefore renders the
  // same icon below. BackupStatus carries no per-stack outcome at all: the
  // per-stack records are BackupRunItem, which the status payload does not include.
  // Not tested -- inferred from GetBackupRuns (backend/internal/database/backup.go),
  // which selects ORDER BY started_at DESC LIMIT ? with no kind and no stack
  // predicate, and getStatus (backend/internal/handlers/backup.go), which takes
  // runs[0]. A per-stack affordance is unimplemented; see agent-os-26pi.
  const lastRun = showLastRunStatus ? (statusData?.lastRun ?? null) : null
  const indicator = lastRun ? lastRunIndicator(lastRun) : null

  if (engineUnavailable) {
    return (
      <TooltipProvider>
        <Tooltip>
          <TooltipTrigger asChild>
            <div
              className="flex items-center gap-1.5 cursor-help"
              data-testid={`backup-toggle-disabled-${stackId}`}
            >
              <Switch
                checked={false}
                disabled
                aria-label={`Backup stack ${stackId}`}
              />
              <Lock className="h-3 w-3 text-muted-foreground" />
            </div>
          </TooltipTrigger>
          <TooltipContent>
            {!statusData?.resticAvailable ? (
              <>
                <p>restic / rclone not installed.</p>
                <p>Configure backups in Settings &rarr; Backup.</p>
              </>
            ) : (
              <>
                {/* The server's own sentence about the state it actually found.
                    A hardcoded "not initialised" here would repeat this bead's
                    defect in prose: unreachable and settings_unreadable reach
                    this arm too, and a repository that merely went unreadable
                    still holds every backup the user has. */}
                <p>{statusData?.repoStateMessage || 'Backup repository is unavailable.'}</p>
                <p>Configure backups in Settings &rarr; Backup.</p>
              </>
            )}
          </TooltipContent>
        </Tooltip>
      </TooltipProvider>
    )
  }

  // agent-os-r6fx: every write below sends BOTH enabled and stopPolicy, and
  // without the policies they would be the `?? false` / `?? 'stop'` defaults, a
  // policy nobody read. So no switch until the policies have loaded. Compact on
  // purpose: this renders once per row in the stacks and updates tables.
  if (!policiesData) {
    if (policiesError) {
      return (
        <Button
          type="button"
          variant="ghost"
          size="sm"
          className="h-6 gap-1 px-2 text-xs"
          onClick={() => void refetchPolicies()}
          aria-label="Could not load the backup policy. Retry"
          title="Could not load the backup policy. Retry"
          data-testid={`backup-toggle-load-failed-${stackId}`}
        >
          <AlertTriangle className="h-3.5 w-3.5 text-destructive" aria-hidden="true" />
          Retry
        </Button>
      )
    }
    return <Skeleton className="h-5 w-9 rounded-full" data-testid={`backup-toggle-loading-${stackId}`} />
  }

  return (
    <div className="flex items-center gap-1.5" data-testid={`backup-toggle-${stackId}`}>
      <Switch
        checked={optimisticEnabled}
        onCheckedChange={handleToggle}
        disabled={toggleMutation.isPending}
        aria-label={`Backup stack ${stackId}`}
        data-testid={`backup-switch-${stackId}`}
      />

      {optimisticEnabled && (
        <Select
          value={optimisticStopPolicy}
          onValueChange={(v) => handleStopPolicyChange(v as 'stop' | 'hot')}
          disabled={toggleMutation.isPending}
        >
          <TooltipProvider>
            <Tooltip>
              <TooltipTrigger asChild>
                <SelectTrigger
                  className="h-6 w-auto text-xs px-2 gap-1 border-dashed"
                  aria-label="Stop policy for backup"
                  data-testid={`backup-stop-policy-${stackId}`}
                >
                  {/* Compact chip: show a short label, not the full two-line option. */}
                  <span>
                    {optimisticStopPolicy === 'stop' ? 'Stop during backup' : 'Back up live'}
                  </span>
                </SelectTrigger>
              </TooltipTrigger>
              <TooltipContent side="bottom" className="max-w-64">
                <p>
                  <strong>Stop during backup</strong> pauses the stack while it copies, so the
                  backup is a consistent point in time. Causes brief downtime.
                </p>
                <p className="mt-1">
                  <strong>Back up live</strong> copies without stopping the stack, so there is
                  no downtime. A file being written during the copy (a database, for example)
                  may be captured in an inconsistent state.
                </p>
              </TooltipContent>
            </Tooltip>
          </TooltipProvider>
          <SelectContent>
            <SelectItem value="stop" data-testid="backup-stop-policy-stop">
              <div className="flex flex-col">
                <span>Stop during backup</span>
                <span className="text-xs text-muted-foreground">
                  Pauses the stack for a consistent copy. Brief downtime.
                </span>
              </div>
            </SelectItem>
            <SelectItem value="hot" data-testid="backup-stop-policy-hot">
              <div className="flex flex-col">
                <span>Back up live</span>
                <span className="text-xs text-muted-foreground">
                  No downtime, but a database mid-write may be inconsistent.
                </span>
              </div>
            </SelectItem>
          </SelectContent>
        </Select>
      )}

      {indicator && (
        <TooltipProvider>
          <Tooltip>
            <TooltipTrigger asChild>
              <indicator.Icon
                className={`h-3.5 w-3.5 cursor-help ${indicator.className}`}
                aria-label={indicator.text}
              />
            </TooltipTrigger>
            <TooltipContent>
              <p>{indicator.text}</p>
            </TooltipContent>
          </Tooltip>
        </TooltipProvider>
      )}
    </div>
  )
}
