import { useMemo, useState } from 'react'

/**
 * Client-side pagination for long resource lists. Resource endpoints return the
 * full set, so we slice locally and clamp to the last valid page whenever the
 * list shrinks past the requested page (e.g. after a prune or a filter change
 * upstream).
 *
 * `page` is derived from `requestedPage` at render time rather than clamped via
 * an effect, so a shrinking list can never render out-of-range results even for
 * one frame.
 */
export function usePagination<T>(items: T[], pageSize: number) {
  const [requestedPage, setRequestedPage] = useState(1)
  const totalPages = Math.max(1, Math.ceil(items.length / pageSize))
  const page = Math.min(requestedPage, totalPages)

  const pageItems = useMemo(
    () => items.slice((page - 1) * pageSize, page * pageSize),
    [items, page, pageSize],
  )

  return { page, setPage: setRequestedPage, totalPages, pageItems, pageSize, total: items.length }
}
