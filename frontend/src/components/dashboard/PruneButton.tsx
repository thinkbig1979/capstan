import { useState, useEffect, useRef } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Button } from '@/components/ui/button'
import { Scissors, CheckCircle2, XCircle, Loader2 } from 'lucide-react'
import { toast } from 'sonner'
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
    onError: () => {
      setPhase('error')
      toast.error(`Failed to prune ${resourceType}`)
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
        <CheckCircle2 className="h-3.5 w-3.5 text-green-600" />
        <span className="text-xs text-green-700 dark:text-green-400">
          Pruned {result.deleted?.length ?? 0} {resourceType}{(result.deleted?.length ?? 0) !== 1 ? 's' : ''}
          {result.spaceReclaimed ? ` (${formatBytes(result.spaceReclaimed)})` : ''}
        </span>
      </div>
    )
  }

  if (phase === 'error') {
    return (
      <div className="flex items-center gap-2 animate-in fade-in duration-150">
        <XCircle className="h-3.5 w-3.5 text-red-600" />
        <span className="text-xs text-red-700 dark:text-red-400">Prune failed</span>
      </div>
    )
  }

  return (
    <Button
      variant="outline"
      size="sm"
      className="h-7 text-xs border-orange-500/60 text-orange-600 hover:bg-orange-50 hover:text-orange-700 dark:border-orange-500/40 dark:text-orange-400 dark:hover:bg-orange-950 dark:hover:text-orange-300"
      onClick={() => setPhase('confirming')}
      title={confirmDescription}
    >
      <Scissors className="mr-1 h-3 w-3" />
      {label}
    </Button>
  )
}
