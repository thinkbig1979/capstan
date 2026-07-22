import { useCallback, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'
import { stacksApi } from '@/lib/api'
import type { Stack } from '@/types'
import { BULK_LABELS, type BulkAction } from './constants'

export function useSidebarSelection(filteredStacks: Stack[]) {
  const queryClient = useQueryClient()
  const [selecting, setSelecting] = useState(false)
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set())
  const [bulkPending, setBulkPending] = useState(false)

  const toggleSelected = useCallback((id: string) => {
    setSelectedIds((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }, [])

  const exitSelectMode = useCallback(() => {
    setSelecting(false)
    setSelectedIds(new Set())
  }, [])

  const selectAllVisible = useCallback(() => {
    setSelectedIds((prev) =>
      prev.size === filteredStacks.length
        ? new Set()
        : new Set(filteredStacks.map((s) => s.id)),
    )
  }, [filteredStacks])

  const runBulk = useCallback(
    async (action: BulkAction) => {
      const ids = [...selectedIds]
      if (ids.length === 0) return
      setBulkPending(true)
      try {
        const results = await Promise.allSettled(
          ids.map((id) => stacksApi[action](id)),
        )
        const ok = results.filter((r) => r.status === 'fulfilled').length
        const failed = results.length - ok
        const verb = BULK_LABELS[action]
        if (failed === 0) {
          toast.success(`${verb} ${ok} stack${ok === 1 ? '' : 's'}`)
        } else if (ok === 0) {
          toast.error(`Failed to ${action} ${failed} stack${failed === 1 ? '' : 's'}`)
        } else {
          toast.warning(`${verb} ${ok}, ${failed} failed`)
        }
        queryClient.invalidateQueries({ queryKey: ['stacks'] })
        queryClient.invalidateQueries({ queryKey: ['dashboard-stats'] })
        exitSelectMode()
      } finally {
        setBulkPending(false)
      }
    },
    [selectedIds, queryClient, exitSelectMode],
  )

  return {
    selecting,
    setSelecting,
    selectedIds,
    bulkPending,
    toggleSelected,
    exitSelectMode,
    selectAllVisible,
    runBulk,
  }
}
