import { useRef, useEffect } from 'react'
import { cn } from '@/lib/utils'
import { useUpdateJobStream } from '@/hooks/useUpdateJobStream'
import type { UpdateJob, JobLine } from '@/stores/updateJobStore'

interface UpdateJobLogProps {
  job: UpdateJob
  /**
   * Opens the job WebSocket only while true. The parent controls whether the
   * panel is rendered, so this typically mirrors the expanded state.
   */
  enabled?: boolean
  className?: string
}

/**
 * Full-width terminal-style log for an update job's streamed output. Streams
 * `docker pull` / recreate lines over /ws/updates/jobs/:id while `enabled`, and
 * auto-scrolls as new lines arrive. Reusable across every place an update can be
 * actioned (per-container in the Updates tab, per-stack from the stack header).
 */
export function UpdateJobLog({ job, enabled = true, className }: UpdateJobLogProps) {
  const scrollRef = useRef<HTMLDivElement>(null)

  useUpdateJobStream(job.id, { enabled })

  useEffect(() => {
    if (scrollRef.current) {
      scrollRef.current.scrollTop = scrollRef.current.scrollHeight
    }
  }, [job.lines])

  const isRunning = job.status === 'queued' || job.status === 'pulling' || job.status === 'recreating'
  const isEmpty = job.lines.length === 0

  return (
    <div className={cn('overflow-hidden rounded-lg border', className)}>
      <div
        ref={scrollRef}
        className="min-h-32 max-h-72 overflow-y-auto bg-terminal-background p-3 font-mono text-xs leading-relaxed text-terminal-foreground"
      >
        {isEmpty && isRunning && (
          <span className="italic text-terminal-foreground/60">Waiting for output…</span>
        )}
        {isEmpty && !isRunning && (
          <span className="italic text-terminal-foreground/50">No output was captured for this update.</span>
        )}
        {job.lines.map((line: JobLine, i: number) => (
          <div
            key={`${i}-${line.ts}`}
            className={cn(
              'whitespace-pre-wrap break-all',
              line.stream === 'stderr' && 'text-destructive',
              line.stream === 'status' && 'text-info',
              line.stream === 'stdout' && 'text-terminal-foreground',
            )}
          >
            {line.text}
          </div>
        ))}
        {isRunning && (
          <div className="flex items-center gap-1 text-terminal-foreground/60">
            <span className="animate-pulse">_</span>
          </div>
        )}
      </div>
      {job.error && (
        <div className="border-t border-destructive/20 bg-destructive/5 px-4 py-2 text-xs text-destructive">
          {job.error}
        </div>
      )}
    </div>
  )
}
