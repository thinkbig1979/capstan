import { useCallback, useEffect, useState } from 'react'
import { loadCollapsedSet, saveCollapsedSet } from '@/lib/collapsed-set-storage'

const SIDEBAR_COLLAPSED_KEY = 'sidebar-collapsed:v1'
// Pre-versioning key. Read once as a migration so existing users don't lose
// their collapsed-group state; never written to again.
const SIDEBAR_COLLAPSED_LEGACY_KEY = 'sidebar-collapsed'

function loadCollapsed(): Set<string> {
  return loadCollapsedSet(SIDEBAR_COLLAPSED_KEY, SIDEBAR_COLLAPSED_LEGACY_KEY) ?? new Set()
}

function saveCollapsed(set: Set<string>) {
  saveCollapsedSet(SIDEBAR_COLLAPSED_KEY, set)
}

export function useCollapsedGroups() {
  const [collapsedGroups, setCollapsedGroups] = useState<Set<string>>(loadCollapsed)

  useEffect(() => {
    saveCollapsed(collapsedGroups)
  }, [collapsedGroups])

  const toggleGroup = useCallback((dirPath: string) => {
    setCollapsedGroups((prev) => {
      const next = new Set(prev)
      if (next.has(dirPath)) next.delete(dirPath)
      else next.add(dirPath)
      return next
    })
  }, [])

  return { collapsedGroups, toggleGroup }
}
