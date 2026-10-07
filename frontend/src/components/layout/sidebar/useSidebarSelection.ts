import { useCallback, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'
import { toastInvalid } from '@/lib/error-handler'
import { stacksApi } from '@/lib/api'
import { isActionResult } from '@/lib/action-result'
import type { Stack } from '@/types'
import { BULK_LABELS, type BulkAction } from './constants'
import { queryKeys } from '@/lib/query-keys'

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
        // A fulfilled call is not a successful one: a stack that only partly
        // started answers 207 with a `partial` ActionResult, which axios resolves.
        // So the outcome is read (isActionResult, the shared guard); a body that
        // is not an ActionResult counts as OK, as it always did. no_change is OK:
        // the stack was already in the requested state.
        const ok = results.filter(
          (r) =>
            r.status === 'fulfilled' &&
            (!isActionResult(r.value) ||
              r.value.outcome === 'success' ||
              r.value.outcome === 'no_change'),
        ).length
        const bad = results.length - ok
        const verb = BULK_LABELS[action]
        if (bad === 0) {
          toast.success(`${verb} ${ok} stack${ok === 1 ? '' : 's'}`)
        } else if (ok === 0) {
          // Not "Failed to": a batch of partials did start some services.
          toastInvalid(`Could not fully ${action} ${bad} stack${bad === 1 ? '' : 's'}`)
        } else {
          toast.warning(`${verb} ${ok}, ${bad} failed or only partly done`)
        }
        queryClient.invalidateQueries({ queryKey: queryKeys.stacks() })
        queryClient.invalidateQueries({ queryKey: queryKeys.dashboardStats() })
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
