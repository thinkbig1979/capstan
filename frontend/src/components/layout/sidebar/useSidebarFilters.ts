import { useEffect, useState } from 'react'
import type { StackStatus } from '@/types'

export function useSidebarFilters() {
  const [searchQuery, setSearchQuery] = useState(
    () => localStorage.getItem('sidebar-search') || '',
  )
  const [statusFilter, setStatusFilter] = useState<StackStatus | 'all'>(() => {
    const saved = localStorage.getItem('sidebar-filter')
    return (saved as StackStatus | 'all') || 'all'
  })
  const [sortBy, setSortBy] = useState<'name' | 'status'>(() => {
    return (localStorage.getItem('sidebar-sort') as 'name' | 'status') || 'name'
  })

  useEffect(() => {
    localStorage.setItem('sidebar-search', searchQuery)
  }, [searchQuery])
  useEffect(() => {
    localStorage.setItem('sidebar-filter', statusFilter)
  }, [statusFilter])
  useEffect(() => {
    localStorage.setItem('sidebar-sort', sortBy)
  }, [sortBy])

  const hasFilters = Boolean(searchQuery) || statusFilter !== 'all'

  const clearFilters = () => {
    setSearchQuery('')
    setStatusFilter('all')
  }

  return {
    searchQuery,
    setSearchQuery,
    statusFilter,
    setStatusFilter,
    sortBy,
    setSortBy,
    hasFilters,
    clearFilters,
  }
}
