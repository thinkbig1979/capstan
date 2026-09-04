import { useState, useEffect } from 'react'
import { Switch } from '@/components/ui/switch'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'
import { Select, SelectContent, SelectItem, SelectTrigger } from '@/components/ui/select'
import { Lock, CheckCircle2, XCircle, CircleDashed } from 'lucide-react'
import { toast } from 'sonner'
import { useToggleBackup, useBackupPolicies, useBackupStatus } from '@/hooks/useBackup'
import { classifyError } from '@/lib/error-handler'
import type { BackupPolicy } from '@/types'

interface BackupToggleProps {
  stackId: string
}

export function BackupToggle({ stackId }: BackupToggleProps) {
  const { data: statusData } = useBackupStatus()
  const { data: policiesData } = useBackupPolicies()
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

  const engineUnavailable =
    !statusData?.resticAvailable || !statusData?.repositoryInitialized

  const handleToggle = (checked: boolean) => {
    setOptimisticEnabled(checked)
    toggleMutation.mutate(
      { stackId, enabled: checked, stopPolicy: optimisticStopPolicy },
      {
        onError: (err) => {
          setOptimisticEnabled(!checked)
          toast.error(classifyError(err).message || 'Failed to toggle backup')
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
          toast.error(classifyError(err).message || 'Failed to update stop policy')
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
  const lastRunStatus = statusData?.lastRun?.status ?? null

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
                <p>Backup repository not initialised.</p>
                <p>Configure backups in Settings &rarr; Backup.</p>
              </>
            )}
          </TooltipContent>
        </Tooltip>
      </TooltipProvider>
    )
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

      {lastRunStatus === 'success' && (
        <TooltipProvider>
          <Tooltip>
            <TooltipTrigger asChild>
              <CheckCircle2
                className="h-3.5 w-3.5 text-green-500 cursor-help"
                aria-label="Last backup succeeded"
              />
            </TooltipTrigger>
            <TooltipContent>
              <p>Last backup succeeded</p>
            </TooltipContent>
          </Tooltip>
        </TooltipProvider>
      )}

      {lastRunStatus === 'failed' && (
        <TooltipProvider>
          <Tooltip>
            <TooltipTrigger asChild>
              <XCircle
                className="h-3.5 w-3.5 text-destructive cursor-help"
                aria-label="Last backup failed"
              />
            </TooltipTrigger>
            <TooltipContent>
              <p>Last backup failed</p>
            </TooltipContent>
          </Tooltip>
        </TooltipProvider>
      )}

      {lastRunStatus === 'interrupted' && (
        <TooltipProvider>
          <Tooltip>
            <TooltipTrigger asChild>
              {/* Neutral, not destructive-red: the run never reported a real
                  outcome (crash, or a restore from a mid-run snapshot) and
                  may have succeeded on the original instance. */}
              <CircleDashed
                className="h-3.5 w-3.5 text-muted-foreground cursor-help"
                aria-label="Last backup was interrupted"
              />
            </TooltipTrigger>
            <TooltipContent>
              <p>Last backup was interrupted</p>
            </TooltipContent>
          </Tooltip>
        </TooltipProvider>
      )}
    </div>
  )
}
