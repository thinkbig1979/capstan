import { useState, useEffect } from 'react'
import { Switch } from '@/components/ui/switch'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'
import { AlertTriangle, Lock } from 'lucide-react'
import { useToggleAutoUpdate } from '@/hooks/useResources'

interface AutoUpdateToggleProps {
  targetType: 'container' | 'stack'
  targetId: string
  enabled: boolean
  paused: boolean
  consecutiveFailures: number
  globalDisabled?: boolean
}

export function AutoUpdateToggle({
  targetType,
  targetId,
  enabled,
  paused,
  consecutiveFailures,
  globalDisabled = false,
}: AutoUpdateToggleProps) {
  const [optimisticEnabled, setOptimisticEnabled] = useState(enabled)
  const toggleMutation = useToggleAutoUpdate()

  useEffect(() => {
    setOptimisticEnabled(enabled)
  }, [enabled])

  const handleToggle = (checked: boolean) => {
    setOptimisticEnabled(checked)
    toggleMutation.mutate(
      { targetType, targetId, enabled: checked },
      {
        onError: () => {
          setOptimisticEnabled(!checked)
        },
      },
    )
  }

  const isChecked = optimisticEnabled && !paused

  if (globalDisabled) {
    return (
      <TooltipProvider>
        <Tooltip>
          <TooltipTrigger asChild>
            <div className="flex items-center gap-1.5 cursor-help">
              <Switch
                checked={false}
                disabled
                aria-label={`Auto-update ${targetType} ${targetId}`}
              />
              <Lock className="h-3 w-3 text-muted-foreground" />
            </div>
          </TooltipTrigger>
          <TooltipContent>
            <p>Auto-update is not enabled.</p>
            <p>Enable it in Settings to configure per-container updates.</p>
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
