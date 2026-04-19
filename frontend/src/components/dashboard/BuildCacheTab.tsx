import { useState, useMemo } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useBuildCache } from '@/hooks/useResources'
import { resourcesApi } from '@/lib/api'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { Button } from '@/components/ui/button'
import {
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
} from '@/components/ui/table'
import { Database, Scissors } from 'lucide-react'
import { toast } from 'sonner'
import { SortFilterBar } from '@/components/dashboard/SortFilterBar'
import { useConfirm } from '@/components/ConfirmDialog'
import type { BuildCacheEntry } from '@/types'

function formatBytes(bytes: number): string {
  if (bytes === 0) return '0 B'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return `${(bytes / Math.pow(k, i)).toFixed(1)} ${sizes[i]}`
}

function formatDate(dateStr: string | null): string {
  if (!dateStr) return '-'
  const date = new Date(dateStr)
  return date.toLocaleDateString(undefined, {
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  })
}

function timeAgo(dateStr: string | null): string {
  if (!dateStr) return '-'
  const seconds = Math.floor((Date.now() - new Date(dateStr).getTime()) / 1000)
  if (seconds < 60) return `${seconds}s ago`
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes}m ago`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h ago`
  const days = Math.floor(hours / 24)
  return `${days}d ago`
}

type SortKey = 'id' | 'type' | 'size' | 'lastUsed' | 'usageCount'

export function BuildCacheTab() {
  const queryClient = useQueryClient()
  const { confirm, ConfirmComponent } = useConfirm()
  const { data: entries, isLoading } = useBuildCache()
  const [sortBy, setSortBy] = useState<SortKey>('size')

  const pruneMutation = useMutation({
    mutationFn: () => resourcesApi.pruneBuildCache(),
    onSuccess: (data) => {
      const count = data.deleted?.length || 0
      const space = data.spaceReclaimed ? formatBytes(data.spaceReclaimed) : '0 B'
      toast.success(`Pruned ${count} cache entr${count !== 1 ? 'ies' : 'y'}, ${space} reclaimed`)
      queryClient.invalidateQueries({ queryKey: ['resources', 'build-cache'] })
      queryClient.invalidateQueries({ queryKey: ['dashboard-stats'] })
    },
    onError: () => toast.error('Failed to prune build cache'),
  })

  const handlePrune = async () => {
    const totalSize = entries?.reduce((sum, e) => sum + e.Size, 0) || 0
    const confirmed = await confirm(
      'Prune Build Cache?',
      `This will remove all unused build cache entries (${formatBytes(totalSize)}). This cannot be undone.`,
      { confirmText: 'Prune', isDangerous: true },
    )
    if (confirmed) pruneMutation.mutate()
  }

  const sortedEntries = useMemo(() => {
    if (!entries) return []
    const sorted = [...entries]
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
  }, [entries, sortBy])

  const totalSize = entries?.reduce((sum, e) => sum + e.Size, 0) || 0

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
      <Card>
        <CardContent className="flex flex-col items-center justify-center py-12">
          <Database className="h-12 w-12 text-muted-foreground mb-4" />
          <p className="text-lg font-semibold">No Build Cache</p>
          <p className="text-sm text-muted-foreground">
            Build cache is empty
          </p>
        </CardContent>
      </Card>
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
        actions={
          <Button variant="outline" size="sm" className="h-7 text-xs" onClick={handlePrune} disabled={pruneMutation.isPending} title="Remove all unused build cache">
            <Scissors className="mr-1 h-3 w-3" />
            Prune All
          </Button>
        }
        countDisplay={`${entries.length} entries · ${formatBytes(totalSize)}`}
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
            {sortedEntries.map((entry: BuildCacheEntry) => (
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
                    {timeAgo(entry.LastUsedAt)}
                  </span>
                </TableCell>
                <TableCell className="text-center">
                  <Badge variant="secondary">{entry.UsageCount}</Badge>
                </TableCell>
                <TableCell>
                  {entry.InUse ? (
                    <Badge variant="outline" className="text-xs text-green-600">Yes</Badge>
                  ) : (
                    <span className="text-sm text-muted-foreground">No</span>
                  )}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
      <ConfirmComponent />
    </div>
  )
}
