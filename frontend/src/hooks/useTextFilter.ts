import { useMemo, useState } from 'react'

/**
 * Accessor for a searchable field: either a key of the item or a function that
 * derives a value from it. Values are stringified and matched case-insensitively.
 */
type FieldAccessor<T> = keyof T | ((item: T) => unknown)

/**
 * Client-side text filter modelled on the sidebar's stack search: an instant,
 * case-insensitive substring match across the supplied fields. No debounce —
 * the lists here are small and immediate feedback is the point.
 */
export function useTextFilter<T>(
  items: T[],
  accessors: Array<FieldAccessor<T>>,
): { query: string; setQuery: (q: string) => void; filtered: T[] } {
  const [query, setQuery] = useState('')

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase()
    if (!q) return items
    return items.filter((item) =>
      accessors.some((accessor) => {
        const value = typeof accessor === 'function' ? accessor(item) : item[accessor]
        return value != null && String(value).toLowerCase().includes(q)
      }),
    )
  }, [items, accessors, query])

  return { query, setQuery, filtered }
}
