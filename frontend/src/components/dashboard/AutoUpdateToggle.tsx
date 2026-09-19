import { useState } from 'react'
import { Switch } from '@/components/ui/switch'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'
import { AlertTriangle, Lock } from 'lucide-react'
import { useToggleAutoUpdate } from '@/hooks/useResources'
import { presentError } from '@/lib/error-handler'

import type { GlobalAutoUpdateState } from '@/components/dashboard/auto-update-state'

/** Why the toggle is locked, in the words the operator needs. */
const LOCK_REASON: Record<
  Exclude<GlobalAutoUpdateState, 'enabled'>,
  { label: string; explain: (targetType: string) => string }
> = {
  disabled: {
    label: 'global auto-update is off',
    explain: (targetType) =>
      `The global master switch is off, enable it in Settings to unlock per-${targetType} toggles.`,
  },
  loading: {
    label: 'checking global auto-update',
    explain: () => 'Checking whether auto-update is switched on globally.',
  },
  unavailable: {
    label: 'auto-update policy state unavailable',
    // Deliberately prescribes nothing: the global switch may well be on, and
    // sending the operator to Settings over a failed request is the bug.
    explain: () => 'The auto-update policy state could not be loaded, so the toggle stays locked.',
  },
}

interface AutoUpdateToggleProps {
  targetType: 'container' | 'stack'
  targetId: string
  enabled: boolean
  paused: boolean
  consecutiveFailures: number
  /** Required, not defaulted: a call site that forgets it is a `tsc` error
   *  rather than a toggle that silently claims the wrong reason. */
  globalState: GlobalAutoUpdateState
}

export function AutoUpdateToggle({
  targetType,
  targetId,
  enabled,
  paused,
  consecutiveFailures,
  globalState,
}: AutoUpdateToggleProps) {
  const [optimisticEnabled, setOptimisticEnabled] = useState(enabled)
  const toggleMutation = useToggleAutoUpdate()

  // Re-sync the optimistic local copy whenever the `enabled` prop changes
  // (e.g. another tab toggled it, or a query refetch confirmed/reverted an
  // in-flight change). Adjusted during render (rather than in an effect) by
  // comparing against the previous render's `enabled` value, so there is no
  // stale first-render flicker — see
  // https://react.dev/learn/you-might-not-need-an-effect.
  const [prevEnabledProp, setPrevEnabledProp] = useState(enabled)
  if (enabled !== prevEnabledProp) {
    setPrevEnabledProp(enabled)
    setOptimisticEnabled(enabled)
  }

  const handleToggle = (checked: boolean) => {
    setOptimisticEnabled(checked)
    toggleMutation.mutate(
      { targetType, targetId, enabled: checked },
      {
        onError: (err) => {
          setOptimisticEnabled(!checked)
          presentError(err, { fallback: 'Failed to toggle auto-update' })
        },
      },
    )
  }

  const isChecked = optimisticEnabled && !paused

  if (globalState !== 'enabled') {
    const reason = LOCK_REASON[globalState]
    return (
      <TooltipProvider>
        <Tooltip>
          <TooltipTrigger asChild>
            <div className="flex items-center gap-1.5 cursor-help">
              <Switch
                checked={false}
                disabled
                aria-label={`Auto-update ${targetType} ${targetId} (locked, ${reason.label})`}
              />
              <Lock className="h-3 w-3 text-muted-foreground" />
            </div>
          </TooltipTrigger>
          <TooltipContent>
            <p>Auto-update is locked.</p>
            <p>{reason.explain(targetType)}</p>
          </TooltipContent>
        </Tooltip>
      </TooltipProvider>
    )
  }

  return (
    <div className="flex items-center gap-1.5">
      <Switch
        checked={isChecked}
        onCheckedChange={handleToggle}
        disabled={toggleMutation.isPending}
        aria-label={`Auto-update ${targetType} ${targetId}`}
      />
      {paused && (
        <TooltipProvider>
          <Tooltip>
            <TooltipTrigger asChild>
              <AlertTriangle className="h-3.5 w-3.5 text-warning cursor-help" />
            </TooltipTrigger>
            <TooltipContent>
              <p>Auto-update paused after 3 consecutive failures.</p>
              <p>Toggle to re-enable.</p>
            </TooltipContent>
          </Tooltip>
        </TooltipProvider>
      )}
      {!paused && consecutiveFailures > 0 && (
        <span className="text-xs font-medium text-warning">
          {consecutiveFailures}f
        </span>
      )}
    </div>
  )
}
