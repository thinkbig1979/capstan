import { useState, useMemo } from 'react'
import { useCheckUpdates, useCheckUpdatesRefresh, useUpdateContainer, useAutoUpdatePolicies, useUpdateJobs } from '@/hooks/useResources'
import { useUpdateScanStore } from '@/stores/updateScanStore'
import { useUpdateJobStore } from '@/stores/updateJobStore'
import { toast } from 'sonner'
import { classifyError } from '@/lib/error-handler'
import { useTextFilter } from '@/hooks/useTextFilter'
import type { AutoUpdatePolicy } from '@/types'
import { UPDATE_SEARCH_FIELDS, type SortKey, type UpdateItem } from './types'

/**
 * Owns every hook, derived value, and handler behind the Available Updates
 * panel: the check/refresh/update queries, scan and job store reads, sort and
 * text-filter state, expanded-log-row state, and the update-trigger toast
 * messaging. Kept as a single hook (rather than several) because these pieces
 * interleave — e.g. sortedUpdates depends on the text filter, and handleUpdate's
 * toast wording depends on the same container data the table renders.
 */
export function useUpdatesData() {
  const { data: updateData, isLoading, isError } = useCheckUpdates()
  const refreshMutation = useCheckUpdatesRefresh()
  const updateMutation = useUpdateContainer()
  const { data: policiesData } = useAutoUpdatePolicies()
  const { isScanning } = useUpdateScanStore()
  const [sortBy, setSortBy] = useState<SortKey>('name')
  // Expanded log state: set of containerIds with expanded log panels
  const [expandedIds, setExpandedIds] = useState<Set<string>>(new Set())

  // Hydrate store on mount so returning to the tab reflects in-flight/recent jobs
  useUpdateJobs()

  const jobForContainer = useUpdateJobStore((s) => s.jobForContainer)

  const updates = useMemo(() => updateData?.updates ?? [], [updateData?.updates])
  const fromCache = updateData?.fromCache ?? false
  const scannedAt = updateData?.scannedAt

  const { query, setQuery, filtered: filteredUpdates } = useTextFilter(updates, UPDATE_SEARCH_FIELDS)

  const policies = useMemo(() => {
    const map = new Map<string, AutoUpdatePolicy>()
    if (policiesData?.policies) {
      for (const p of policiesData.policies) {
        map.set(`${p.targetType}:${p.targetId}`, p)
      }
    }
    return map
  }, [policiesData])

  const handleCheck = () => {
    refreshMutation.mutate()
  }

  const handleUpdate = (container: UpdateItem) => {
    updateMutation.mutate(container.containerId, {
      onSuccess: () => {
        // The job WS drives the outcome UI (the cell shows queued/pulling/success/no_change/failed).
        // A neutral info toast confirms the request was accepted — it is NOT a success claim
        // (finding #4 / pattern P-6: no unconditional toast.success on 2xx/enqueue).
        const action = container.state === 'running' ? 'queued for update and restart' : 'queued for update'
        toast.info(`${container.containerName} ${action}`)
      },
      onError: (err) => {
        toast.error(classifyError(err).message || `Failed to update ${container.containerName}`)
      },
    })
  }

  const toggleExpand = (containerId: string) => {
    setExpandedIds((prev) => {
      const next = new Set(prev)
      if (next.has(containerId)) {
        next.delete(containerId)
      } else {
        next.add(containerId)
      }
      return next
    })
  }

  const sortedUpdates = useMemo(() => {
    if (!filteredUpdates.length) return filteredUpdates
    const sorted = [...filteredUpdates]
    switch (sortBy) {
      case 'name':
        return sorted.sort((a, b) => a.containerName.localeCompare(b.containerName))
      case 'image':
        return sorted.sort((a, b) => a.imageRef.localeCompare(b.imageRef))
      case 'state':
        return sorted.sort((a, b) => a.state.localeCompare(b.state))
      case 'stack':
        return sorted.sort((a, b) => (a.projectName || '').localeCompare(b.projectName || ''))
      default:
        return sorted
    }
  }, [filteredUpdates, sortBy])

  const hasData = updates.length > 0
  const isRefreshing = isScanning
  const neverScanned = !updateData && !isLoading && !isError && !isScanning

  return {
    isLoading,
    isError,
    isRefreshing,
    neverScanned,
    hasData,
    updates,
    sortedUpdates,
    fromCache,
    scannedAt,
    sortBy,
    setSortBy,
    query,
    setQuery,
    policies,
    jobForContainer,
    expandedIds,
    toggleExpand,
    handleCheck,
    handleUpdate,
    updatePending: updateMutation.isPending,
  }
}
