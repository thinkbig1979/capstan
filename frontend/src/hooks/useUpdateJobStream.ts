import { useCallback, useRef } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { useWebSocketJSON } from './useWebSocket'
import { useUpdateJobStore } from '@/stores/updateJobStore'
import { reconcileOnClose } from '@/lib/ws-reconcile'
import type { UpdateJob, JobLine, UpdateJobStatus, UpdateJobOutcome } from '@/stores/updateJobStore'
import { queryKeys } from '@/lib/query-keys'

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
  outcome?: UpdateJobOutcome
  reason?: string
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

  const { upsertJob, appendLine, setStatus, setOutcome } = useUpdateJobStore.getState()
  const queryClient = useQueryClient()

  // Track whether a terminal 'done' frame was received so reconcileOnClose can
  // refetch instead of asserting failure on a clean close without a done frame.
  const receivedDoneRef = useRef(false)

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
          receivedDoneRef.current = true
          setStatus(jobId, frame.status, frame.error)
          // Store the typed outcome so the cell can render truthfully.
          if (frame.outcome !== undefined) {
            setOutcome(jobId, frame.outcome, frame.reason)
          }
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

  const handleClose = useCallback(() => {
    reconcileOnClose({
      completed: receivedDoneRef.current,
      refetch: () => {
        // Refetch both the job detail and the updates list so the UI converges
        // to server truth on an unexpected close (finding 17 / reconcileOnClose pattern).
        queryClient.invalidateQueries({ queryKey: queryKeys.resources.updateJobs() })
        queryClient.invalidateQueries({ queryKey: queryKeys.resources.updates() })
      },
    })
  }, [queryClient])

  // This used to pass a '/ws/updates/jobs/_noop' sentinel when jobId was null,
  // because `skip` was not a dependency of useWebSocket's connect effect —
  // varying the path was the only way to force it to re-run once a job id
  // arrived. skip is a real dependency now (agent-os-9d5e), so the placeholder
  // path is never connected to and does not need to look like an endpoint.
  const { status } = useWebSocketJSON<JobStreamFrame>(
    jobId ? `/ws/updates/jobs/${jobId}` : '',
    handleFrame,
    { skip, onClose: handleClose },
  )

  return { connected: status === 'connected' }
}
