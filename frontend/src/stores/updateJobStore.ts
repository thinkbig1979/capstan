import { create } from 'zustand'

export type UpdateJobStatus = 'queued' | 'pulling' | 'recreating' | 'success' | 'error'

/** Typed outcome from the backend job — present once the job reaches a terminal state. */
export type UpdateJobOutcome = 'success' | 'no_change' | 'failed'

export interface JobLine {
  ts: string
  text: string
  stream: 'stdout' | 'stderr' | 'status'
}

export interface UpdateJob {
  id: string
  targetType: 'container' | 'stack'
  targetId: string
  name: string
  stackId: string
  status: UpdateJobStatus
  lines: JobLine[]
  error?: string
  /** Typed outcome: 'success' (image advanced), 'no_change' (already up to date), 'failed'. Present only when terminal. */
  outcome?: UpdateJobOutcome
  /** Human-readable reason accompanying the outcome. */
  reason?: string
  createdAt: string
  startedAt?: string
  finishedAt?: string
}

// Payloads from the global stack-events bus
export interface UpdateJobProgressEvent {
  jobId: string
  targetType: 'container' | 'stack'
  targetId: string
  stackId: string
  name: string
  status: UpdateJobStatus
}

export interface UpdateJobCompleteEvent {
  jobId: string
  targetType: 'container' | 'stack'
  targetId: string
  stackId: string
  name: string
  status: UpdateJobStatus
  error?: string
  outcome?: UpdateJobOutcome
  reason?: string
}

interface UpdateJobState {
  jobs: Record<string, UpdateJob>

  // Upsert a full job object (from snapshot or hydration)
  upsertJob: (job: UpdateJob) => void

  // Apply a progress event from the global bus; creates a shell job if unseen
  applyProgress: (evt: UpdateJobProgressEvent) => void

  // Apply a complete event from the global bus
  applyComplete: (evt: UpdateJobCompleteEvent) => void

  // Append a single log line to an existing job
  appendLine: (jobId: string, line: JobLine) => void

  // Update job status (and optional error) without replacing the whole job
  setStatus: (jobId: string, status: UpdateJobStatus, error?: string) => void

  // Set the typed outcome and reason on a terminal job (called from WS 'done' frame)
  setOutcome: (jobId: string, outcome: UpdateJobOutcome, reason?: string) => void

  // Replace store contents with a fresh list (called on mount hydration)
  hydrate: (jobs: UpdateJob[]) => void

  // Remove evicted job ids from the store
  clearEvicted: (ids: string[]) => void

  // Derived getters
  jobForContainer: (containerId: string) => UpdateJob | undefined
  jobsForStack: (stackId: string) => UpdateJob[]
}

export const useUpdateJobStore = create<UpdateJobState>()((set, get) => ({
  jobs: {},

  upsertJob: (job) => {
    set((state) => ({
      jobs: {
        ...state.jobs,
        [job.id]: job,
      },
    }))
  },

  applyProgress: (evt) => {
    set((state) => {
      const existing = state.jobs[evt.jobId]
      if (existing) {
        return {
          jobs: {
            ...state.jobs,
            [evt.jobId]: {
              ...existing,
              status: evt.status,
              name: evt.name,
              targetType: evt.targetType,
              targetId: evt.targetId,
              stackId: evt.stackId,
            },
          },
        }
      }
      // Create a shell job so any view that mounts during a running job has state
      const shell: UpdateJob = {
        id: evt.jobId,
        targetType: evt.targetType,
        targetId: evt.targetId,
        name: evt.name,
        stackId: evt.stackId,
        status: evt.status,
        lines: [],
        createdAt: new Date().toISOString(),
      }
      return {
        jobs: {
          ...state.jobs,
          [evt.jobId]: shell,
        },
      }
    })
  },

  applyComplete: (evt) => {
    set((state) => {
      const existing = state.jobs[evt.jobId]
      const base: UpdateJob = existing ?? {
        id: evt.jobId,
        targetType: evt.targetType,
        targetId: evt.targetId,
        name: evt.name,
        stackId: evt.stackId,
        status: evt.status,
        lines: [],
        createdAt: new Date().toISOString(),
      }
      const updated: UpdateJob = {
        ...base,
        status: evt.status,
        name: evt.name,
        error: evt.error,
        finishedAt: new Date().toISOString(),
      }
      if (evt.outcome !== undefined) updated.outcome = evt.outcome
      if (evt.reason !== undefined) updated.reason = evt.reason
      return {
        jobs: {
          ...state.jobs,
          [evt.jobId]: updated,
        },
      }
    })
  },

  appendLine: (jobId, line) => {
    set((state) => {
      const job = state.jobs[jobId]
      if (!job) return state
      return {
        jobs: {
          ...state.jobs,
          [jobId]: {
            ...job,
            lines: [...job.lines, line],
          },
        },
      }
    })
  },

  setStatus: (jobId, status, error) => {
    set((state) => {
      const job = state.jobs[jobId]
      if (!job) return state
      const updated: UpdateJob = { ...job, status }
      if (error !== undefined) updated.error = error
      if (status === 'success' || status === 'error') {
        updated.finishedAt = updated.finishedAt ?? new Date().toISOString()
      }
      return {
        jobs: {
          ...state.jobs,
          [jobId]: updated,
        },
      }
    })
  },

  setOutcome: (jobId, outcome, reason) => {
    set((state) => {
      const job = state.jobs[jobId]
      if (!job) return state
      const updated: UpdateJob = { ...job, outcome }
      if (reason !== undefined) updated.reason = reason
      return {
        jobs: {
          ...state.jobs,
          [jobId]: updated,
        },
      }
    })
  },

  hydrate: (jobs) => {
    const map: Record<string, UpdateJob> = {}
    for (const job of jobs) {
      map[job.id] = job
    }
    set({ jobs: map })
  },

  clearEvicted: (ids) => {
    set((state) => {
      const next = { ...state.jobs }
      for (const id of ids) {
        delete next[id]
      }
      return { jobs: next }
    })
  },

  jobForContainer: (containerId) => {
    const { jobs } = get()
    return Object.values(jobs).find(
      (j) => j.targetType === 'container' && j.targetId === containerId,
    )
  },

  jobsForStack: (stackId) => {
    const { jobs } = get()
    return Object.values(jobs).filter((j) => j.stackId === stackId)
  },
}))
