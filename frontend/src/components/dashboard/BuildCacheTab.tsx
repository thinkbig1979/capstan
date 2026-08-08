import { useState, useMemo } from 'react'
import { useBuildCache } from '@/hooks/useResources'
import { resourcesApi } from '@/lib/api'
import { Badge } from '@/components/ui/badge'
import { Skeleton } from '@/components/ui/skeleton'
import { EmptyState } from '@/components/EmptyState'
import {
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
} from '@/components/ui/table'
import { Database } from 'lucide-react'
import { SortFilterBar } from '@/components/dashboard/SortFilterBar'
import { HelpHint } from '@/components/ui/help-hint'
import { PruneButton } from '@/components/dashboard/PruneButton'
import { TablePagination } from '@/components/dashboard/TablePagination'
import { usePagination } from '@/hooks/usePagination'
import { useTextFilter } from '@/hooks/useTextFilter'
import type { BuildCacheEntry } from '@/types'
import { formatBytes, formatDate, formatRelativeTime } from '@/lib/format'
import { queryKeys } from '@/lib/query-keys'

const PAGE_SIZE = 50

type SortKey = 'id' | 'type' | 'size' | 'lastUsed' | 'usageCount'

const CACHE_SEARCH_FIELDS = [
  (e: BuildCacheEntry) => e.id,
  (e: BuildCacheEntry) => e.type,
  (e: BuildCacheEntry) => e.description,
]

export function BuildCacheTab() {
  const { data: entries, isLoading } = useBuildCache()
  const [sortBy, setSortBy] = useState<SortKey>('size')

  const { query, setQuery, filtered } = useTextFilter(entries ?? [], CACHE_SEARCH_FIELDS)

  const sortedEntries = useMemo(() => {
    const sorted = [...filtered]
    switch (sortBy) {
      case 'id':
        return sorted.sort((a, b) => (a.description || a.id).localeCompare(b.description || b.id))
      case 'type':
        return sorted.sort((a, b) => a.type.localeCompare(b.type))
      case 'size':
        return sorted.sort((a, b) => b.size - a.size)
      case 'lastUsed':
        return sorted.sort((a, b) => {
          const aTime = a.lastUsedAt ? new Date(a.lastUsedAt).getTime() : 0
          const bTime = b.lastUsedAt ? new Date(b.lastUsedAt).getTime() : 0
          return bTime - aTime
        })
      case 'usageCount':
        return sorted.sort((a, b) => b.usageCount - a.usageCount)
      default:
        return sorted
    }
  }, [filtered, sortBy])

  const { page, setPage, totalPages, pageItems } = usePagination(sortedEntries, PAGE_SIZE)

  const totalSize = entries?.reduce((sum, e) => sum + e.size, 0) || 0

  const pruneDescription = `Removes unused build cache${totalSize > 0 ? ` (up to ${formatBytes(totalSize)})` : ''}. Enable 'all' to also remove cache that could still be reused. This cannot be undone.`

  if (isLoading) {
    return (
      <div className="space-y-4">
        {Array.from({ length: 5 }).map((_, i) => (
          <Skeleton key={i} className="h-12 w-full" />
        ))}
      </div>
    )
  }

  if (!entries || entries.length === 0) {
    return (
      <EmptyState
        icon={<Database className="h-12 w-12 text-muted-foreground" />}
        title="No Build Cache"
        description="Build cache is empty"
      />
    )
  }

  return (
    <div className="space-y-4">
      <SortFilterBar
        sortOptions={[
          { key: 'id', label: 'Name' },
          { key: 'type', label: 'Type' },
          { key: 'size', label: 'Size' },
          { key: 'lastUsed', label: 'Last Used' },
          { key: 'usageCount', label: 'Usage' },
        ]}
        sortValue={sortBy}
        onSortChange={(key) => setSortBy(key as SortKey)}
        help={
          <HelpHint label="Build cache" title="Build cache" side="bottom" align="start">
            <p>
              Docker saves image layers here while building, so repeat builds run faster.
              Clearing it is safe; Docker rebuilds whatever it needs next time.
            </p>
            <p>The &apos;all&apos; option also drops cache that could still be reused.</p>
          </HelpHint>
        }
        actions={
          <PruneButton
            resourceType="cache entry"
            pruneFn={(opts) => resourcesApi.pruneBuildCache(opts)}
            options={{ all: { label: 'Remove all build cache, not just unused' }, until: true }}
            confirmMessage="Prune Build Cache?"
            confirmDescription={pruneDescription}
            invalidateKeys={[queryKeys.resources.buildCache(), queryKeys.dashboardStats()]}
          />
        }
        searchValue={query}
        onSearchChange={setQuery}
        searchPlaceholder="Filter cache…"
        countDisplay={
          query
            ? `${sortedEntries.length} of ${entries.length} entries`
            : `${entries.length} entries · ${formatBytes(totalSize)}`
        }
      />

      <div className="rounded-md border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>ID</TableHead>
              <TableHead>Type</TableHead>
              <TableHead>Description</TableHead>
              <TableHead>Size</TableHead>
              <TableHead>Last Used</TableHead>
              <TableHead className="text-center">Usage</TableHead>
              <TableHead>In Use</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {pageItems.map((entry: BuildCacheEntry) => (
              <TableRow key={entry.id}>
                <TableCell>
                  <span className="text-xs font-mono text-muted-foreground">
                    {entry.id.substring(0, 19)}
                  </span>
                </TableCell>
                <TableCell>
                  <Badge variant="outline" className="text-xs">{entry.type}</Badge>
                </TableCell>
                <TableCell>
                  <span className="text-sm truncate max-w-[300px] block">{entry.description || '-'}</span>
                </TableCell>
                <TableCell>
                  <span className="text-sm font-medium">{formatBytes(entry.size)}</span>
                </TableCell>
                <TableCell>
                  <span className="text-sm text-muted-foreground" title={formatDate(entry.lastUsedAt)}>
                    {formatRelativeTime(entry.lastUsedAt)}
                  </span>
                </TableCell>
                <TableCell className="text-center">
                  <Badge variant="secondary">{entry.usageCount}</Badge>
                </TableCell>
                <TableCell>
                  {entry.inUse ? (
                    <Badge variant="outline" className="text-xs text-success">Yes</Badge>
                  ) : (
                    <span className="text-sm text-muted-foreground">No</span>
                  )}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
      <TablePagination
        page={page}
        totalPages={totalPages}
        pageSize={PAGE_SIZE}
        total={sortedEntries.length}
        onPageChange={setPage}
        label="entries"
      />
    </div>
  )
}
