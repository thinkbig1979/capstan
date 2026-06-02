import { useEffect, useRef } from 'react'
import { useWebSocketJSON } from './useWebSocket'
import { queryClient } from '@/lib/query-client'
import { resolveUpdateScanSuccess, resolveUpdateScanError } from './useResources'
import { useUpdateJobStore } from '@/stores/updateJobStore'
import type { Stack } from '@/types'
import type { UpdateJobProgressEvent, UpdateJobCompleteEvent } from '@/stores/updateJobStore'

export interface StackStatusEvent {
  type: 'stack_status'
  stackId: string
  status: 'running' | 'stopped' | 'partial' | 'unknown' | 'error'
  timestamp: string
}

export interface ContainerEvent {
  type: 'container_event'
  stackId: string
  containerId: string
  event: string
  timestamp: string
}

export interface ScanCompleteEvent {
  type: 'scan_complete'
  added: number
  removed: number
  timestamp: string
}

export interface ResourceChangedEvent {
  type: 'resource_changed'
  event?: string
  containerId?: string
  timestamp: string
}

export interface UpdateScanCompleteEvent {
  type: 'update_scan_complete'
  timestamp: string
}

export interface UpdateJobProgressStackEvent {
  type: 'update_job_progress'
  jobId: string
  targetType: 'container' | 'stack'
  targetId: string
  stackId: string
  name: string
  status: 'queued' | 'pulling' | 'recreating' | 'success' | 'error'
}

export interface UpdateJobCompleteStackEvent {
  type: 'update_job_complete'
  jobId: string
  targetType: 'container' | 'stack'
  targetId: string
  stackId: string
  name: string
  status: 'queued' | 'pulling' | 'recreating' | 'success' | 'error'
  error?: string
}

export interface UpdatePolicyChangedEvent {
  type: 'update_policy_changed'
  timestamp: string
}

export interface UpdateCompletedEvent {
  type: 'update_completed'
  containerId?: string
  timestamp: string
}

export interface UpdateScanFailedEvent {
  type: 'update_scan_failed'
  timestamp: string
}

export type StackEvent =
  | StackStatusEvent
  | ContainerEvent
  | ScanCompleteEvent
  | ResourceChangedEvent
  | UpdateScanCompleteEvent
  | UpdatePolicyChangedEvent
  | UpdateCompletedEvent
  | UpdateScanFailedEvent
  | UpdateJobProgressStackEvent
  | UpdateJobCompleteStackEvent

export function useStackEvents() {
  const pendingRef = useRef<Set<string>>(new Set())
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  useEffect(() => {
    const currentPending = pendingRef.current
    return () => {
      if (currentPending.size > 0) {
        currentPending.forEach((key) => {
          queryClient.invalidateQueries({ queryKey: JSON.parse(key) })
        })
      }
      if (timerRef.current) clearTimeout(timerRef.current)
    }
  }, [])

  const scheduleInvalidations = (keys: string[][]) => {
    keys.forEach((k) => pendingRef.current.add(JSON.stringify(k)))
    if (timerRef.current) clearTimeout(timerRef.current)
    timerRef.current = setTimeout(() => {
      pendingRef.current.forEach((key) => {
        queryClient.invalidateQueries({ queryKey: JSON.parse(key) })
      })
      pendingRef.current.clear()
      timerRef.current = null
    }, 750)
  }

  const handleStackStatusEvent = (event: StackStatusEvent) => {
    queryClient.setQueryData(['stacks'], (old: Stack[] | undefined) => {
      if (!old) return old
      return old.map((stack) =>
        stack.id === event.stackId ? { ...stack, status: event.status } : stack
      )
    })
    scheduleInvalidations([
      ['stack', event.stackId],
      ['dashboard-stats'],
    ])
  }

  const handleContainerEvent = (event: ContainerEvent) => {
    const keys: string[][] = [['dashboard-stats']]
    if (event.stackId) {
      keys.push(['stack', event.stackId], ['stacks'])
    }
    scheduleInvalidations(keys)
  }

  const handleScanCompleteEvent = () => {
    scheduleInvalidations([['stacks'], ['directories']])
  }

  const handleResourceChangedEvent = (event: ResourceChangedEvent) => {
    const keys: string[][] = [
      ['resources', 'images'],
      ['resources', 'volumes'],
      ['resources', 'networks'],
      ['resources', 'build-cache'],
      ['dashboard-stats'],
    ]
    if (event.containerId) {
      keys.push(['stacks'])
    }
    scheduleInvalidations(keys)
  }

  // Fast path: the backend broadcasts these when a scan genuinely finishes. The
  // shared resolvers are gated on an active scan (so a scheduled background scan
  // doesn't pop a toast nobody asked for) and are idempotent with the watcher's
  // poll-based completion — whichever fires first wins.
  const handleUpdateScanCompleteEvent = () => {
    resolveUpdateScanSuccess()
    scheduleInvalidations([
      ['resources', 'updates'],
      ['settings', 'updates'],
    ])
  }

  const handleUpdateScanFailedEvent = () => {
    resolveUpdateScanError()
    scheduleInvalidations([
      ['resources', 'updates'],
    ])
  }

  const handleUpdatePolicyChangedEvent = () => {
    scheduleInvalidations([
      ['auto-update-policies'],
      ['settings', 'updates'],
      ['resources', 'updates'],
    ])
  }

  const handleUpdateCompletedEvent = () => {
    scheduleInvalidations([
      ['update-history'],
      ['resources', 'updates'],
      ['dashboard-stats'],
      ['stacks'],
    ])
  }

  const handleUpdateJobProgressEvent = (event: UpdateJobProgressStackEvent) => {
    const { applyProgress } = useUpdateJobStore.getState()
    const payload: UpdateJobProgressEvent = {
      jobId: event.jobId,
      targetType: event.targetType,
      targetId: event.targetId,
      stackId: event.stackId,
      name: event.name,
      status: event.status,
    }
    applyProgress(payload)
  }

  const handleUpdateJobCompleteEvent = (event: UpdateJobCompleteStackEvent) => {
    const { applyComplete } = useUpdateJobStore.getState()
    const payload: UpdateJobCompleteEvent = {
      jobId: event.jobId,
      targetType: event.targetType,
      targetId: event.targetId,
      stackId: event.stackId,
      name: event.name,
      status: event.status,
      error: event.error,
    }
    applyComplete(payload)
    scheduleInvalidations([
      ['update-history'],
      ['resources', 'updates'],
      ['dashboard-stats'],
      ['stacks'],
    ])
  }

  useWebSocketJSON<StackEvent>(
    '/ws/events',
    (data) => {
      switch (data.type) {
        case 'stack_status':
          handleStackStatusEvent(data)
          break
        case 'container_event':
          handleContainerEvent(data)
          break
        case 'scan_complete':
          handleScanCompleteEvent()
          break
        case 'resource_changed':
          handleResourceChangedEvent(data)
          break
        case 'update_scan_complete':
          handleUpdateScanCompleteEvent()
          break
        case 'update_scan_failed':
          handleUpdateScanFailedEvent()
          break
        case 'update_policy_changed':
          handleUpdatePolicyChangedEvent()
          break
        case 'update_completed':
          handleUpdateCompletedEvent()
          break
        case 'update_job_progress':
          handleUpdateJobProgressEvent(data)
          break
        case 'update_job_complete':
          handleUpdateJobCompleteEvent(data)
          break
      }
    }
  )
}
