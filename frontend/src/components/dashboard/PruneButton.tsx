import { useState, useEffect, useRef } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Button } from '@/components/ui/button'
import { Switch } from '@/components/ui/switch'
import { Label } from '@/components/ui/label'
import { Popover, PopoverTrigger, PopoverContent } from '@/components/ui/popover'
import { Scissors, CheckCircle2, XCircle, Loader2 } from 'lucide-react'
import { toast } from 'sonner'
import { cn } from '@/lib/utils'
import { classifyError } from '@/lib/error-handler'
import { formatBytes } from '@/lib/format'
import { isActionResult, toastForResult } from '@/lib/action-result'
import type { PruneOptions } from '@/lib/api'

/**
 * PruneResult accepts both the legacy shape and the future ActionResult shape:
 *
 * Legacy (current backend):
 *   { deleted?: string[] | null; spaceReclaimed?: number | null }
 *
 * Action Truth Contract (post-B3 migration):
 *   { outcome, reason, details?: { deletedCount?, deleted?, spaceReclaimed? } }
 *
 * The `no_change` outcome (honest "nothing to prune") is explicitly shown as
 * info, not as a "Pruned 0" success — a no-op must not look like success.
 */
interface PruneResult {
  deleted?: string[] | null
  spaceReclaimed?: number | null
  // ActionResult fields (present after backend migration)
  outcome?: string
  reason?: string
  details?: {
    deletedCount?: number
    deleted?: string[]
    spaceReclaimed?: number
    tagsRemoved?: number
  }
}

/**
 * Extracts count and space from either a legacy or ActionResult prune response.
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
  if (isActionResult(data)) {
    const d = data.details as {
      imagesDeleted?: number
      deleted?: string[]
      spaceReclaimed?: number
      tagsRemoved?: number
    } | undefined
    return {
      count: d?.imagesDeleted ?? d?.deleted?.length ?? 0,
      spaceReclaimed: d?.spaceReclaimed ?? null,
      tagsRemoved: d?.tagsRemoved ?? 0,
    }
  }
  return {
    count: data.deleted?.length ?? 0,
    spaceReclaimed: data.spaceReclaimed ?? null,
    tagsRemoved: 0,
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
  invalidateKeys: string[][]
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
  const queryClient = useQueryClient()
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

  const mutation = useMutation({
    mutationFn: pruneFn,
    onSuccess: (data) => {
      setResult(data)

      if (isActionResult(data)) {
        if (data.outcome === 'no_change') {
          // Honest: nothing was pruned. Show info, NOT success.
          setPhase('done')
          toast.info(data.reason || `No ${resourceType}s to prune`)
        } else {
          setPhase('done')
          const { count, spaceReclaimed, tagsRemoved } = extractPruneMetrics(data)
          toastForResult(data, {
            successTitle: buildPruneSummary(resourceType, count, spaceReclaimed, tagsRemoved),
          })
        }
      } else {
        setPhase('done')
        const { count, spaceReclaimed, tagsRemoved } = extractPruneMetrics(data)
        toast.success(buildPruneSummary(resourceType, count, spaceReclaimed, tagsRemoved))
      }

      for (const key of invalidateKeys) {
        queryClient.invalidateQueries({ queryKey: key })
      }
      onPruneComplete?.()
      timerRef.current = setTimeout(() => {
        setPhase('idle')
        setResult(null)
      }, 5000)
    },
    onError: (err) => {
      setPhase('error')
      if (isActionResult(err)) {
        toastForResult(err)
      } else {
        toast.error(classifyError(err).message || `Failed to prune ${resourceType}`)
      }
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
    if (isActionResult(result) && result.outcome === 'no_change') {
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
    return (
      <div className="flex items-center gap-2 animate-in fade-in duration-150">
        <CheckCircle2 className="h-3.5 w-3.5 text-success" />
        <span className="text-xs text-success">
          Pruned {count} {resourceType}{count !== 1 ? 's' : ''}
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
