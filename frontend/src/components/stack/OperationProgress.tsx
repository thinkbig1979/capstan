import { useEffect, useRef } from 'react'
import { CheckCircle, XCircle, Loader2, X } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'
import type { OperationStatus } from '@/hooks/useStreamingOperation'

interface OperationProgressProps {
  status: OperationStatus
  lines: string[]
  action: string
  error: string | null
  onDismiss: () => void
}

const actionLabels: Record<string, string> = {
  pull: 'Pulling images',
  start: 'Starting stack',
  stop: 'Stopping stack',
  restart: 'Restarting stack',
}

export function OperationProgress({ status, lines, action, error, onDismiss }: OperationProgressProps) {
  const scrollRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (scrollRef.current) {
      scrollRef.current.scrollTop = scrollRef.current.scrollHeight
    }
  }, [lines])

  if (status === 'idle') return null

  const isRunning = status === 'running'
  const isDone = status === 'success' || status === 'error'

  return (
    <div className={cn(
      'rounded-lg border overflow-hidden',
      isDone && status === 'success' && 'border-green-500/30',
      isDone && status === 'error' && 'border-red-500/30',
      isRunning && 'border-blue-500/30',
    )}>
      <div className={cn(
        'flex items-center justify-between px-4 py-2 text-sm font-medium',
        isRunning && 'bg-blue-500/10 text-blue-400',
        status === 'success' && 'bg-green-500/10 text-green-400',
        status === 'error' && 'bg-red-500/10 text-red-400',
      )}>
        <div className="flex items-center gap-2">
          {isRunning && <Loader2 className="h-4 w-4 animate-spin" />}
          {status === 'success' && <CheckCircle className="h-4 w-4" />}
          {status === 'error' && <XCircle className="h-4 w-4" />}
          <span>{isRunning ? actionLabels[action] || action : status === 'success' ? 'Operation completed' : 'Operation failed'}</span>
          {isRunning && lines.length > 0 && (
            <span className="text-xs opacity-60">({lines.length} lines)</span>
          )}
        </div>
        {isDone && (
          <Button variant="ghost" size="sm" onClick={onDismiss} className="h-6 px-2">
            <X className="h-3 w-3" />
          </Button>
        )}
      </div>
      <div
        ref={scrollRef}
        className="max-h-64 overflow-y-auto bg-[#1a1a1a] p-3 font-mono text-xs leading-relaxed"
      >
        {lines.map((line, i) => (
          <div key={`${i}-${line.slice(0, 20)}`} className={cn(
            'whitespace-pre-wrap break-all',
            line.startsWith('Error:') && 'text-red-400',
            line.startsWith('---') && 'text-blue-400',
            !line.startsWith('Error:') && !line.startsWith('---') && 'text-zinc-300',
          )}>
            {line}
          </div>
        ))}
        {isRunning && (
          <div className="flex items-center gap-1 text-zinc-500">
            <span className="animate-pulse">_</span>
          </div>
        )}
      </div>
      {error && (
        <div className="px-4 py-2 text-xs text-red-400 bg-red-500/5 border-t border-red-500/20">
          {error}
        </div>
      )}
    </div>
  )
}
