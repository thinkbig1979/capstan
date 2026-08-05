import { useCallback, useEffect, useRef } from 'react'
import type { QueryKey } from '@tanstack/react-query'
import { useWebSocketJSON } from './useWebSocket'
import { queryClient } from '@/lib/query-client'
import { resolveUpdateScanSuccess, resolveUpdateScanError } from './useResources'
import { useUpdateJobStore } from '@/stores/updateJobStore'
import type { Stack } from '@/types'
import type { UpdateJobProgressEvent, UpdateJobCompleteEvent, UpdateJobOutcome } from '@/stores/updateJobStore'
import { queryKeys } from '@/lib/query-keys'

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

interface ScanCompleteEvent {
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

interface UpdateScanCompleteEvent {
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
  outcome?: UpdateJobOutcome
  reason?: string
}

/** Emitted by the backend when the updates cache has changed (row evicted after apply). */
interface UpdatesChangedEvent {
  type: 'updates_changed'
  timestamp: string
}

interface UpdatePolicyChangedEvent {
  type: 'update_policy_changed'
  timestamp: string
}

interface UpdateCompletedEvent {
  type: 'update_completed'
  containerId?: string
  timestamp: string
}

interface UpdateScanFailedEvent {
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
  | UpdatesChangedEvent

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

  // Only ever touches pendingRef/timerRef.current (stable refs) and the
  // module-level queryClient, so it never needs to change identity.
  const scheduleInvalidations = useCallback((keys: QueryKey[]) => {
    keys.forEach((k) => pendingRef.current.add(JSON.stringify(k)))
    if (timerRef.current) clearTimeout(timerRef.current)
    timerRef.current = setTimeout(() => {
      pendingRef.current.forEach((key) => {
        queryClient.invalidateQueries({ queryKey: JSON.parse(key) })
      })
      pendingRef.current.clear()
      timerRef.current = null
    }, 750)
  }, [])

  const handleStackStatusEvent = useCallback((event: StackStatusEvent) => {
    queryClient.setQueryData(queryKeys.stacks(), (old: Stack[] | undefined) => {
      if (!old) return old
      return old.map((stack) =>
        stack.id === event.stackId ? { ...stack, status: event.status } : stack
      )
    })
    scheduleInvalidations([
      queryKeys.stack.detail(event.stackId),
      queryKeys.dashboardStats(),
    ])
  }, [scheduleInvalidations])

  const handleContainerEvent = useCallback((event: ContainerEvent) => {
    const keys: QueryKey[] = [queryKeys.dashboardStats()]
    if (event.stackId) {
      keys.push(queryKeys.stack.detail(event.stackId), queryKeys.stacks())
    }
    scheduleInvalidations(keys)
  }, [scheduleInvalidations])

  const handleScanCompleteEvent = useCallback(() => {
    scheduleInvalidations([queryKeys.stacks(), queryKeys.directories()])
  }, [scheduleInvalidations])

  const handleResourceChangedEvent = useCallback((event: ResourceChangedEvent) => {
    const keys: QueryKey[] = [
      queryKeys.resources.images(),
      queryKeys.resources.volumes(),
      queryKeys.resources.networks(),
      queryKeys.resources.buildCache(),
      queryKeys.dashboardStats(),
    ]
    if (event.containerId) {
      keys.push(queryKeys.stacks())
    }
    scheduleInvalidations(keys)
  }, [scheduleInvalidations])

  // Fast path: the backend broadcasts these when a scan genuinely finishes. The
  // shared resolvers are gated on an active scan (so a scheduled background scan
  // doesn't pop a toast nobody asked for) and are idempotent with the watcher's
  // poll-based completion — whichever fires first wins.
  const handleUpdateScanCompleteEvent = useCallback(() => {
    resolveUpdateScanSuccess()
    scheduleInvalidations([
      queryKeys.resources.updates(),
      queryKeys.settings.updates(),
    ])
  }, [scheduleInvalidations])

  const handleUpdateScanFailedEvent = useCallback(() => {
    resolveUpdateScanError()
    scheduleInvalidations([
      queryKeys.resources.updates(),
    ])
  }, [scheduleInvalidations])

  const handleUpdatePolicyChangedEvent = useCallback(() => {
    scheduleInvalidations([
      queryKeys.autoUpdatePolicies(),
      queryKeys.settings.updates(),
      queryKeys.resources.updates(),
    ])
  }, [scheduleInvalidations])

  const handleUpdateCompletedEvent = useCallback(() => {
    scheduleInvalidations([
      queryKeys.updateHistory.all(),
      queryKeys.resources.updates(),
      queryKeys.dashboardStats(),
      queryKeys.stacks(),
    ])
  }, [scheduleInvalidations])

  const handleUpdateJobProgressEvent = useCallback((event: UpdateJobProgressStackEvent) => {
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
  }, [])

  const handleUpdateJobCompleteEvent = useCallback((event: UpdateJobCompleteStackEvent) => {
    const { applyComplete } = useUpdateJobStore.getState()
    const payload: UpdateJobCompleteEvent = {
      jobId: event.jobId,
      targetType: event.targetType,
      targetId: event.targetId,
      stackId: event.stackId,
      name: event.name,
      status: event.status,
      error: event.error,
      outcome: event.outcome,
      reason: event.reason,
    }
    applyComplete(payload)
    // Always invalidate history and stats.
    const keys: QueryKey[] = [
      queryKeys.updateHistory.all(),
      queryKeys.dashboardStats(),
      queryKeys.stacks(),
    ]
    // On success or no_change, the backend evicts the row from cached_updates and
    // broadcasts an updates-changed signal; we also force a refetch here so the
    // UI converges and the row disappears even if the WS signal races/misses.
    if (event.outcome === 'success' || event.outcome === 'no_change') {
      keys.push(queryKeys.resources.updates())
    } else {
      // For failed/unknown outcomes still invalidate so the list stays fresh.
      keys.push(queryKeys.resources.updates())
    }
    scheduleInvalidations(keys)
  }, [scheduleInvalidations])

  const handleUpdatesChangedEvent = useCallback(() => {
    // Backend evicted one or more rows from the updates cache (after a verified apply).
    // Refetch the updates list immediately so the row disappears.
    scheduleInvalidations([
      queryKeys.resources.updates(),
    ])
  }, [scheduleInvalidations])

  const handleMessage = useCallback((data: StackEvent) => {
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
      case 'updates_changed':
        handleUpdatesChangedEvent()
        break
    }
  }, [
    handleStackStatusEvent,
    handleContainerEvent,
    handleScanCompleteEvent,
    handleResourceChangedEvent,
    handleUpdateScanCompleteEvent,
    handleUpdateScanFailedEvent,
    handleUpdatePolicyChangedEvent,
    handleUpdateCompletedEvent,
    handleUpdateJobProgressEvent,
    handleUpdateJobCompleteEvent,
    handleUpdatesChangedEvent,
  ])

  useWebSocketJSON<StackEvent>('/ws/events', handleMessage)
}
