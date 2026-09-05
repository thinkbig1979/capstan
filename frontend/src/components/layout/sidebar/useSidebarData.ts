import { useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import { backupApi, resourcesApi, settingsApi, stacksApi } from '@/lib/api'
import { buildDirectoryTree, hasTreeNesting } from '@/lib/stack-tree'
import type { StackStatus } from '@/types'
import { queryKeys } from '@/lib/query-keys'

interface UseSidebarDataParams {
  searchQuery: string
  statusFilter: StackStatus | 'all'
  sortBy: 'name' | 'status'
  pinnedStacks: string[]
}

export function useSidebarData({ searchQuery, statusFilter, sortBy, pinnedStacks }: UseSidebarDataParams) {
  const { data: stacks = [], isLoading } = useQuery({
    queryKey: queryKeys.stacks(),
    queryFn: () => stacksApi.list(),
  })

  const { data: config } = useQuery({
    queryKey: queryKeys.config(),
    queryFn: settingsApi.getConfig,
    staleTime: Infinity,
  })

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

  const configuredDirs = useMemo(() => {
    if (!config?.stacksDirectories) return []
    return config.stacksDirectories.map((p: string) => ({
      path: p,
      name: p.split('/').filter(Boolean).pop() || p,
    }))
  }, [config])

  const filteredStacks = useMemo(() => {
    let result = [...stacks]
    if (searchQuery) {
      const q = searchQuery.toLowerCase()
      result = result.filter((s) => s.projectName.toLowerCase().includes(q))
    }
    if (statusFilter !== 'all') {
      result = result.filter((s) => s.status === statusFilter)
    }
    result.sort((a, b) => {
      if (sortBy === 'status')
        return (
          a.status.localeCompare(b.status) ||
          a.projectName.localeCompare(b.projectName)
        )
      return a.projectName.localeCompare(b.projectName)
    })
    return result
  }, [stacks, searchQuery, statusFilter, sortBy])

  const pinnedVisible = useMemo(
    () => filteredStacks.filter((s) => pinnedStacks.includes(s.id)),
    [filteredStacks, pinnedStacks],
  )

  const tree = useMemo(() => {
    if (configuredDirs.length === 0) return []
    return buildDirectoryTree(filteredStacks, configuredDirs.map((d) => d.path))
  }, [filteredStacks, configuredDirs])

  const treeByRoot = useMemo(() => {
    return configuredDirs
      .map((cd) => {
        const rootStacks = filteredStacks.filter(
          (s) => s.directory === cd.path || s.directory.startsWith(cd.path + '/'),
        )
        return {
          rootPath: cd.path,
          rootName: cd.name,
          nodes: buildDirectoryTree(rootStacks, [cd.path]),
        }
      })
      .filter((g) => g.nodes.length > 0)
  }, [filteredStacks, configuredDirs])

  const useGroups = useMemo(() => {
    if (stacks.length === 0) return false
    const allTree = buildDirectoryTree(stacks, configuredDirs.map((d) => d.path))
    return configuredDirs.length > 1 || allTree.length > 1 || hasTreeNesting(allTree)
  }, [stacks, configuredDirs])

  return {
    stacks,
    isLoading,
    updateCount,
    backupStatus,
    configuredDirs,
    filteredStacks,
    pinnedVisible,
    tree,
    treeByRoot,
    useGroups,
  }
}
