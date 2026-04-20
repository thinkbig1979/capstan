import { useState, useEffect, useRef } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Button } from '@/components/ui/button'
import { Scissors, CheckCircle2, XCircle, Loader2 } from 'lucide-react'
import { toast } from 'sonner'
import { formatBytes } from '@/lib/format'

interface PruneResult {
  deleted: string[]
  spaceReclaimed?: number
}

interface PruneButtonProps {
  label?: string
  resourceType: string
  pruneFn: () => Promise<PruneResult>
  confirmMessage: string
  confirmDescription: string
  invalidateKeys: string[][]
  onPruneComplete?: () => void
}

export function PruneButton({
  label = 'Prune',
  resourceType,
  pruneFn,
  confirmMessage,
  confirmDescription,
  invalidateKeys,
  onPruneComplete,
}: PruneButtonProps) {
  const queryClient = useQueryClient()
  const [phase, setPhase] = useState<'idle' | 'confirming' | 'pruning' | 'done' | 'error'>('idle')
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
      setPhase('done')
      const count = data.deleted?.length || 0
      const space = data.spaceReclaimed ? formatBytes(data.spaceReclaimed) : null
      const parts = [`Pruned ${count} ${resourceType}${count !== 1 ? 's' : ''}`]
      if (space) parts.push(`${space} reclaimed`)
      toast.success(parts.join(', '))
      for (const key of invalidateKeys) {
        queryClient.invalidateQueries({ queryKey: key })
      }
      onPruneComplete?.()
      timerRef.current = setTimeout(() => {
        setPhase('idle')
        setResult(null)
      }, 5000)
    },
    onError: () => {
      setPhase('error')
      toast.error(`Failed to prune ${resourceType}`)
      timerRef.current = setTimeout(() => setPhase('idle'), 4000)
    },
  })

  const isPruning = phase === 'confirming' || phase === 'pruning'

  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center gap-2">
        <Button
          variant="outline"
          size="sm"
          className="h-7 text-xs"
          onClick={() => setPhase('confirming')}
          disabled={isPruning}
          title={confirmDescription}
        >
          {phase === 'pruning' ? (
            <Scissors className="mr-1 h-3 w-3 animate-spin" />
          ) : (
            <Scissors className="mr-1 h-3 w-3" />
          )}
          {phase === 'pruning' ? 'Pruning...' : label}
        </Button>
      </div>

      {phase === 'confirming' && (
        <div className="rounded-lg border border-yellow-500/30 bg-yellow-500/5 p-3 animate-in fade-in slide-in-from-top-1 duration-200">
          <p className="text-sm font-medium">{confirmMessage}</p>
          <p className="text-xs text-muted-foreground mt-1">{confirmDescription}</p>
          <div className="flex items-center gap-2 mt-3">
            <Button
              size="sm"
              variant="destructive"
              className="h-7 text-xs"
              onClick={() => {
                setPhase('pruning')
                mutation.mutate()
              }}
            >
              Confirm Prune
            </Button>
            <Button
              size="sm"
              variant="ghost"
              className="h-7 text-xs"
              onClick={() => setPhase('idle')}
            >
              Cancel
            </Button>
          </div>
        </div>
      )}

      {phase === 'pruning' && (
        <div className="rounded-lg border bg-muted/30 p-3 overflow-hidden">
          <div className="flex items-center gap-2">
            <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" />
            <span className="text-sm text-muted-foreground">Pruning {resourceType}s...</span>
          </div>
          <div className="mt-2 h-1.5 w-full rounded-full bg-muted overflow-hidden">
            <div className="h-full rounded-full bg-primary animate-prune-progress" />
          </div>
        </div>
      )}

      {phase === 'done' && result && (
        <div className="rounded-lg border border-green-500/30 bg-green-500/5 p-3 animate-in fade-in slide-in-from-top-1 duration-200">
          <div className="flex items-start gap-2">
            <CheckCircle2 className="h-4 w-4 text-green-600 mt-0.5 shrink-0" />
            <div className="flex-1 min-w-0">
              <p className="text-sm font-medium text-green-700 dark:text-green-400">
                Pruned {result.deleted.length} {resourceType}{result.deleted.length !== 1 ? 's' : ''}
                {result.spaceReclaimed ? ` (${formatBytes(result.spaceReclaimed)} reclaimed)` : ''}
              </p>
              {result.deleted.length > 0 && (
                <div className="mt-1.5 flex flex-wrap gap-1">
                  {result.deleted.slice(0, 8).map((id) => (
                    <span
                      key={id}
                      className="inline-block text-xs font-mono bg-muted px-1.5 py-0.5 rounded truncate max-w-[160px]"
                      title={id}
                    >
                      {id.length > 24 ? `${id.substring(0, 12)}...${id.substring(id.length - 12)}` : id}
                    </span>
                  ))}
                  {result.deleted.length > 8 && (
                    <span className="text-xs text-muted-foreground">
                      +{result.deleted.length - 8} more
                    </span>
                  )}
                </div>
              )}
            </div>
          </div>
        </div>
      )}

      {phase === 'error' && (
        <div className="rounded-lg border border-red-500/30 bg-red-500/5 p-3 animate-in fade-in slide-in-from-top-1 duration-200">
          <div className="flex items-center gap-2">
            <XCircle className="h-4 w-4 text-red-600" />
            <span className="text-sm text-red-700 dark:text-red-400">
              Failed to prune {resourceType}s. Please try again.
            </span>
          </div>
        </div>
      )}
    </div>
  )
}
