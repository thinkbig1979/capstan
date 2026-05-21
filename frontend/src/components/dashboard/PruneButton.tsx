import { useState, useEffect, useRef } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Button } from '@/components/ui/button'
import { Scissors, CheckCircle2, XCircle, Loader2 } from 'lucide-react'
import { toast } from 'sonner'
import { classifyError } from '@/lib/error-handler'
import { formatBytes } from '@/lib/format'

interface PruneResult {
  deleted?: string[] | null
  spaceReclaimed?: number | null
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
    onError: (err) => {
      setPhase('error')
      toast.error(classifyError(err).message || `Failed to prune ${resourceType}`)
      timerRef.current = setTimeout(() => setPhase('idle'), 4000)
    },
  })

  if (phase === 'confirming') {
    return (
      <div className="flex items-center gap-2 animate-in fade-in duration-150">
        <span className="text-xs text-muted-foreground">{confirmMessage}</span>
        <Button
          size="sm"
          variant="destructive"
          className="h-7 text-xs"
          onClick={() => {
            setPhase('pruning')
            mutation.mutate()
          }}
        >
          Confirm
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
    )
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
    return (
      <div className="flex items-center gap-2 animate-in fade-in duration-150">
        <CheckCircle2 className="h-3.5 w-3.5 text-success" />
        <span className="text-xs text-success">
          Pruned {result.deleted?.length ?? 0} {resourceType}{(result.deleted?.length ?? 0) !== 1 ? 's' : ''}
          {result.spaceReclaimed ? ` (${formatBytes(result.spaceReclaimed)})` : ''}
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
    <Button
      variant="outline"
      size="sm"
      className="h-7 text-xs border-warning/60 text-warning hover:bg-warning/10"
      onClick={() => setPhase('confirming')}
      title={confirmDescription}
    >
      <Scissors className="mr-1 h-3 w-3" />
      {label}
    </Button>
  )
}
