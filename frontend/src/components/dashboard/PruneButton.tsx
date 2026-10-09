import { useState, useEffect, useRef } from 'react'
import type { QueryKey } from '@tanstack/react-query'
import { Button } from '@/components/ui/button'
import { Switch } from '@/components/ui/switch'
import { Label } from '@/components/ui/label'
import { Popover, PopoverTrigger, PopoverContent } from '@/components/ui/popover'
import { Scissors, CheckCircle2, XCircle, Loader2, AlertTriangle } from 'lucide-react'
import { cn } from '@/lib/utils'
import { useActionMutation } from '@/hooks/useActionMutation'
import { formatBytes } from '@/lib/format'
import type { PruneOptions, PruneResult } from '@/lib/api'

/**
 * Extracts count and space from a prune ActionResult. A `no_change` result
 * (honest "nothing to prune") never reaches this: it is shown as info, not as
 * a "Pruned 0" success, because a no-op must not look like success.
 *
 * Backend detail key alignment (resource_mutations.go):
 *  - Image prune: details.imagesDeleted (number)
 *  - Volume/container/build-cache prune: details.deleted (array)
 *  - Network prune: details.deleted (array)
 *  - All: details.spaceReclaimed (number, absent for networks)
 */
function extractPruneMetrics(data: PruneResult): {
  count: number
  spaceReclaimed: number | null
  tagsRemoved: number
} {
  const d = data.details
  return {
    count: d?.imagesDeleted ?? d?.deleted?.length ?? 0,
    spaceReclaimed: d?.spaceReclaimed ?? null,
    tagsRemoved: d?.tagsRemoved ?? 0,
  }
}

// Builds the "Pruned N images[, M tags][, X reclaimed]" summary. Including tags
// avoids the misleading "Pruned 0 images" when only tags were removed (B3).
function buildPruneSummary(
  resourceType: string,
  count: number,
  spaceReclaimed: number | null,
  tagsRemoved: number,
): string {
  const parts = [`Pruned ${count} ${resourceType}${count !== 1 ? 's' : ''}`]
  if (tagsRemoved > 0) parts.push(`${tagsRemoved} tag${tagsRemoved !== 1 ? 's' : ''}`)
  if (spaceReclaimed) parts.push(`${formatBytes(spaceReclaimed)} reclaimed`)
  return parts.join(', ')
}

// Which option controls a given prune surfaces. Docker only supports each flag on
// certain resources, so each tab opts in to just the controls that apply.
/** @knipignore exported for dashboard/__tests__/PruneButton.test.tsx, which types its fixtures with it */
export interface PruneOptionConfig {
  // Show the "remove all unused (not just dangling/anonymous)" toggle, with the
  // given label describing what "all" means for this resource.
  all?: { label: string }
  // Show the "older than" age filter (the `until` flag).
  until?: boolean
}

interface PruneButtonProps {
  label?: string
  resourceType: string
  pruneFn: (opts: PruneOptions) => Promise<PruneResult>
  confirmMessage: string
  confirmDescription: string
  /** Keys to invalidate once the prune settles — build these from `queryKeys`. */
  invalidateKeys: QueryKey[]
  options?: PruneOptionConfig
  onPruneComplete?: () => void
}

// Age presets for the `until` filter. Empty value = no age filter (any age).
const AGE_PRESETS: { label: string; value: string }[] = [
  { label: 'Any age', value: '' },
  { label: '1h', value: '1h' },
  { label: '24h', value: '24h' },
  { label: '7d', value: '168h' },
  { label: '30d', value: '720h' },
]

export function PruneButton({
  label = 'Prune',
  resourceType,
  pruneFn,
  confirmMessage,
  confirmDescription,
  invalidateKeys,
  options,
  onPruneComplete,
}: PruneButtonProps) {
  const [phase, setPhase] = useState<'idle' | 'pruning' | 'done' | 'error'>('idle')
  const [open, setOpen] = useState(false)
  const [all, setAll] = useState(false)
  const [until, setUntil] = useState('')
  const [result, setResult] = useState<PruneResult | null>(null)
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  useEffect(() => {
    return () => {
      if (timerRef.current) clearTimeout(timerRef.current)
    }
  }, [])

  const mutation = useActionMutation<PruneOptions, PruneResult>({
    mutationFn: pruneFn,
    invalidate: invalidateKeys,
    // Only a `success` outcome takes this title. A `no_change` ("nothing to
    // prune") stays an info toast with the backend's reason, so a no-op never
    // reads as a "Pruned 0" success.
    successTitle: (data) => {
      const { count, spaceReclaimed, tagsRemoved } = extractPruneMetrics(data)
      return buildPruneSummary(resourceType, count, spaceReclaimed, tagsRemoved)
    },
    errorTitle: `Failed to prune ${resourceType}`,
    onResult: (data) => {
      setResult(data)
      setPhase('done')
      onPruneComplete?.()
      timerRef.current = setTimeout(() => {
        setPhase('idle')
        setResult(null)
      }, 5000)
    },
    onError: () => {
      setPhase('error')
      timerRef.current = setTimeout(() => setPhase('idle'), 4000)
    },
  })

  const handleConfirm = () => {
    setOpen(false)
    setPhase('pruning')
    mutation.mutate({ all, until: until || undefined })
  }

  if (phase === 'pruning') {
    return (
      <div className="flex items-center gap-2">
        <Loader2 className="h-3.5 w-3.5 animate-spin text-muted-foreground" />
        <span className="text-xs text-muted-foreground">Pruning...</span>
      </div>
    )
  }

  if (phase === 'done' && result) {
    // no_change: show info indicator (not the green checkmark)
    if (result.outcome === 'no_change') {
      return (
        <div className="flex items-center gap-2 animate-in fade-in duration-150">
          <CheckCircle2 className="h-3.5 w-3.5 text-muted-foreground" />
          <span className="text-xs text-muted-foreground">
            {result.reason || `Nothing to prune`}
          </span>
        </div>
      )
    }

    const { count, spaceReclaimed, tagsRemoved } = extractPruneMetrics(result)
    // A partial prune removed some resources and failed on others; the inline
    // indicator must agree with the warning toast, not read as full success.
    const partial = result.outcome === 'partial'
    const Icon = partial ? AlertTriangle : CheckCircle2
    const tone = partial ? 'text-warning' : 'text-success'
    return (
      <div className="flex items-center gap-2 animate-in fade-in duration-150">
        <Icon className={cn('h-3.5 w-3.5', tone)} />
        <span className={cn('text-xs', tone)}>
          {partial ? 'Partly pruned' : 'Pruned'} {count} {resourceType}{count !== 1 ? 's' : ''}
          {tagsRemoved > 0 ? `, ${tagsRemoved} tag${tagsRemoved !== 1 ? 's' : ''}` : ''}
          {spaceReclaimed ? ` (${formatBytes(spaceReclaimed)})` : ''}
        </span>
      </div>
    )
  }

  if (phase === 'error') {
    return (
      <div className="flex items-center gap-2 animate-in fade-in duration-150">
        <XCircle className="h-3.5 w-3.5 text-destructive" />
        <span className="text-xs text-destructive">Prune failed</span>
      </div>
    )
  }

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button
          variant="outline"
          size="sm"
          className="h-7 text-xs border-warning/60 text-warning hover:bg-warning/10"
        >
          <Scissors className="mr-1 h-3 w-3" />
          {label}
        </Button>
      </PopoverTrigger>
      <PopoverContent align="end" className="w-80">
        <div className="space-y-4">
          <div className="space-y-1">
            <p className="text-sm font-semibold">{confirmMessage}</p>
            <p className="text-xs text-muted-foreground">{confirmDescription}</p>
          </div>

          {options?.all && (
            <div className="flex items-start justify-between gap-3">
              <Label htmlFor="prune-all" className="text-xs font-normal leading-snug text-foreground">
                {options.all.label}
              </Label>
              <Switch id="prune-all" checked={all} onCheckedChange={setAll} />
            </div>
          )}

          {options?.until && (
            <div className="space-y-1.5">
              <p className="text-xs font-medium">Only older than</p>
              <div className="flex flex-wrap gap-1">
                {AGE_PRESETS.map((preset) => (
                  <button
                    key={preset.value || 'any'}
                    type="button"
                    onClick={() => setUntil(preset.value)}
                    className={cn(
                      'rounded border px-2 py-0.5 text-xs transition-colors',
                      until === preset.value
                        ? 'border-primary bg-primary text-primary-foreground'
                        : 'border-input bg-background hover:bg-accent',
                    )}
                  >
                    {preset.label}
                  </button>
                ))}
              </div>
            </div>
          )}

          <div className="flex justify-end gap-2 pt-1">
            <Button size="sm" variant="ghost" className="h-7 text-xs" onClick={() => setOpen(false)}>
              Cancel
            </Button>
            <Button size="sm" variant="destructive" className="h-7 text-xs" onClick={handleConfirm}>
              Prune
            </Button>
          </div>
        </div>
      </PopoverContent>
    </Popover>
  )
}
