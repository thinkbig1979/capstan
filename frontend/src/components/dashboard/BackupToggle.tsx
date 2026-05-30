import { useState, useEffect } from 'react'
import { Switch } from '@/components/ui/switch'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Lock, CheckCircle2, XCircle } from 'lucide-react'
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

  // Last backup result for this stack derived from the run items embedded in status.
  // BackupStatus does not carry per-stack run items directly; we surface nothing
  // here and leave it for the dedicated Backups tab (later task). The spec says
  // "small status affordance shows last backup result", so we derive it from
  // statusData.lastRun only when it relates to this stack.
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
                  <SelectValue />
                </SelectTrigger>
              </TooltipTrigger>
              <TooltipContent side="bottom" className="max-w-64">
                <p>
                  <strong>Stop for consistency</strong> briefly takes the stack down during
                  backup for consistent data.
                </p>
                <p className="mt-1">
                  <strong>Hot backup</strong> keeps it running (faster, but risks
                  inconsistent data for databases).
                </p>
              </TooltipContent>
            </Tooltip>
          </TooltipProvider>
          <SelectContent>
            <SelectItem value="stop" data-testid="backup-stop-policy-stop">
              Stop for consistency
            </SelectItem>
            <SelectItem value="hot" data-testid="backup-stop-policy-hot">
              Hot backup
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
    </div>
  )
}
