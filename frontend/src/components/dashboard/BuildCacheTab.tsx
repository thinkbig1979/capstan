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
import { TablePagination, usePagination } from '@/components/dashboard/TablePagination'
import { useTextFilter } from '@/hooks/useTextFilter'
import type { BuildCacheEntry } from '@/types'
import { formatBytes, formatDate, formatRelativeTime } from '@/lib/format'

const PAGE_SIZE = 50

type SortKey = 'id' | 'type' | 'size' | 'lastUsed' | 'usageCount'

const CACHE_SEARCH_FIELDS = [
  (e: BuildCacheEntry) => e.ID,
  (e: BuildCacheEntry) => e.Type,
  (e: BuildCacheEntry) => e.Description,
]

export function BuildCacheTab() {
  const { data: entries, isLoading } = useBuildCache()
  const [sortBy, setSortBy] = useState<SortKey>('size')

  const { query, setQuery, filtered } = useTextFilter(entries ?? [], CACHE_SEARCH_FIELDS)

  const sortedEntries = useMemo(() => {
    const sorted = [...filtered]
    switch (sortBy) {
      case 'id':
        return sorted.sort((a, b) => (a.Description || a.ID).localeCompare(b.Description || b.ID))
      case 'type':
        return sorted.sort((a, b) => a.Type.localeCompare(b.Type))
      case 'size':
        return sorted.sort((a, b) => b.Size - a.Size)
      case 'lastUsed':
        return sorted.sort((a, b) => {
          const aTime = a.LastUsedAt ? new Date(a.LastUsedAt).getTime() : 0
          const bTime = b.LastUsedAt ? new Date(b.LastUsedAt).getTime() : 0
          return bTime - aTime
        })
      case 'usageCount':
        return sorted.sort((a, b) => b.UsageCount - a.UsageCount)
      default:
        return sorted
    }
  }, [filtered, sortBy])

  const { page, setPage, totalPages, pageItems } = usePagination(sortedEntries, PAGE_SIZE)

  const totalSize = entries?.reduce((sum, e) => sum + e.Size, 0) || 0

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
            invalidateKeys={[['resources', 'build-cache'], ['dashboard-stats']]}
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
              <TableRow key={entry.ID}>
                <TableCell>
                  <span className="text-xs font-mono text-muted-foreground">
                    {entry.ID.substring(0, 19)}
                  </span>
                </TableCell>
                <TableCell>
                  <Badge variant="outline" className="text-xs">{entry.Type}</Badge>
                </TableCell>
                <TableCell>
                  <span className="text-sm truncate max-w-[300px] block">{entry.Description || '-'}</span>
                </TableCell>
                <TableCell>
                  <span className="text-sm font-medium">{formatBytes(entry.Size)}</span>
                </TableCell>
                <TableCell>
                  <span className="text-sm text-muted-foreground" title={formatDate(entry.LastUsedAt)}>
                    {formatRelativeTime(entry.LastUsedAt)}
                  </span>
                </TableCell>
                <TableCell className="text-center">
                  <Badge variant="secondary">{entry.UsageCount}</Badge>
                </TableCell>
                <TableCell>
                  {entry.InUse ? (
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
