import { useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import { backupApi, resourcesApi, stacksApi } from '@/lib/api'
import type { StackStatus } from '@/types'
import { queryKeys } from '@/lib/query-keys'
import { STACKS_LIST_POLLING } from '@/lib/query-client'

interface UseSidebarDataParams {
  searchQuery: string
  statusFilter: StackStatus | 'all'
  sortBy: 'name' | 'status'
  pinnedStacks: string[]
}

export function useSidebarData({ searchQuery, statusFilter, sortBy, pinnedStacks }: UseSidebarDataParams) {
  const {
    data: stacksData,
    isPending,
    isError: stacksError,
    refetch: refetchStacks,
  } = useQuery({
    queryKey: queryKeys.stacks(),
    queryFn: () => stacksApi.list(),
    ...STACKS_LIST_POLLING,
  })
  // A failed request has no data, and [] would read as "No stacks found"
  // (agent-os-kdqm). The empty array only keeps the derivations below simple;
  // consumers check stacksLoadFailed before treating it as an answer.
  const stacks = useMemo(() => stacksData ?? [], [stacksData])
  const stacksLoadFailed = stacksError && !stacksData
  const stacksRefreshFailed = stacksError && !!stacksData

  // Cached update scan — drives the aggregate "N updates" badge. refresh=false
  // never kicks off a heavy scan, it just reads whatever the backend has.
  // Shares the canonical ['resources','updates'] key so update mutations and the
  // scan watcher (useResources) invalidate this badge too — otherwise it stays
  // stale until a full page refresh.
  const { data: updateData } = useQuery({
    queryKey: queryKeys.resources.updates(),
    queryFn: () => resourcesApi.checkUpdates(false),
    staleTime: 60_000,
    retry: false,
  })
  const updateCount = updateData?.updates?.length ?? 0

  // Global backup status for the footer (last run + next-run countdown).
  const { data: backupStatus } = useQuery({
    queryKey: queryKeys.backup.status(),
    queryFn: backupApi.getStatus,
    staleTime: 60_000,
    refetchInterval: 60_000,
    retry: false,
  })

  const filteredStacks = useMemo(() => {
    const pinnedSet = new Set(pinnedStacks)
    let result = [...stacks]
    if (searchQuery) {
      const q = searchQuery.toLowerCase()
      result = result.filter((s) => s.projectName.toLowerCase().includes(q))
    }
    if (statusFilter !== 'all') {
      result = result.filter((s) => s.status === statusFilter)
    }
    // Pinned stacks move to the top of the one list; the name/status sort
    // applies inside each group. Sorting after the filters means a pinned stack
    // that fails them is hidden, not floated.
    result.sort((a, b) => {
      const pinDiff = Number(pinnedSet.has(b.id)) - Number(pinnedSet.has(a.id))
      if (pinDiff !== 0) return pinDiff
      if (sortBy === 'status')
        return (
          a.status.localeCompare(b.status) ||
          a.projectName.localeCompare(b.projectName)
        )
      return a.projectName.localeCompare(b.projectName)
    })
    return result
  }, [stacks, searchQuery, statusFilter, sortBy, pinnedStacks])

  return {
    stacks,
    isPending,
    stacksLoadFailed,
    stacksRefreshFailed,
    refetchStacks,
    updateCount,
    backupStatus,
    filteredStacks,
  }
}
