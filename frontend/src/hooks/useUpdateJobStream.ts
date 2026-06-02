import { useCallback } from 'react'
import { useWebSocketJSON } from './useWebSocket'
import { useUpdateJobStore } from '@/stores/updateJobStore'
import type { UpdateJob, JobLine, UpdateJobStatus } from '@/stores/updateJobStore'

// ── WS frame shapes (per api-contract.md) ────────────────────────────────────

interface SnapshotFrame {
  type: 'snapshot'
  job: UpdateJob
}

interface LineFrame {
  type: 'line'
  line: JobLine
}

interface StatusFrame {
  type: 'status'
  status: UpdateJobStatus
  error?: string
}

interface DoneFrame {
  type: 'done'
  status: 'success' | 'error'
  error?: string
}

interface ErrorFrame {
  type: 'error'
  error: string
}

type JobStreamFrame = SnapshotFrame | LineFrame | StatusFrame | DoneFrame | ErrorFrame

export interface UseUpdateJobStreamReturn {
  connected: boolean
}

export function useUpdateJobStream(
  jobId: string | null,
  opts?: { enabled?: boolean },
): UseUpdateJobStreamReturn {
  const skip = !jobId || opts?.enabled === false

  const { upsertJob, appendLine, setStatus } = useUpdateJobStore.getState()

  const handleFrame = useCallback(
    (frame: JobStreamFrame) => {
      if (!jobId) return
      switch (frame.type) {
        case 'snapshot':
          upsertJob(frame.job)
          break
        case 'line':
          appendLine(jobId, frame.line)
          break
        case 'status':
          setStatus(jobId, frame.status, frame.error)
          break
        case 'done':
          setStatus(jobId, frame.status, frame.error)
          break
        case 'error':
          // Server-side error (e.g. job not found / evicted); mark as error in store
          setStatus(jobId, 'error', frame.error)
          break
      }
    },
    // jobId is stable for a given stream instance; store actions are stable references
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [jobId],
  )

  const { status } = useWebSocketJSON<JobStreamFrame>(
    jobId ? `/ws/updates/jobs/${jobId}` : '/ws/updates/jobs/_noop',
    handleFrame,
    { skip },
  )

  return { connected: status === 'connected' }
}
