import { useState, useEffect } from 'react'
import { Switch } from '@/components/ui/switch'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { AlertTriangle } from 'lucide-react'
import { useToggleAutoUpdate } from '@/hooks/useResources'

interface AutoUpdateToggleProps {
  targetType: 'container' | 'stack'
  targetId: string
  enabled: boolean
  paused: boolean
  consecutiveFailures: number
}

export function AutoUpdateToggle({
  targetType,
  targetId,
  enabled,
  paused,
  consecutiveFailures,
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

  return (
    <div className="flex items-center gap-1.5">
      <Switch
        checked={isChecked}
        onCheckedChange={handleToggle}
        disabled={toggleMutation.isPending}
        aria-label={`Auto-update ${targetType} ${targetId}`}
      />
      {paused && (
        <Tooltip>
          <TooltipTrigger asChild>
            <AlertTriangle className="h-3.5 w-3.5 text-orange-500 cursor-help" />
          </TooltipTrigger>
          <TooltipContent>
            <p>Auto-update paused after 3 consecutive failures.</p>
            <p>Toggle to re-enable.</p>
          </TooltipContent>
        </Tooltip>
      )}
      {!paused && consecutiveFailures > 0 && (
        <span className="text-xs font-medium text-yellow-600 dark:text-yellow-400">
          {consecutiveFailures}f
        </span>
      )}
    </div>
  )
}
