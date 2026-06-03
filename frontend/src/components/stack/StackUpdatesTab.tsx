import { useState, useMemo } from 'react'
import { useUpdateHistory } from '@/hooks/useResources'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Status, type StatusTone } from '@/components/ui/status'
import { Card, CardContent } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
} from '@/components/ui/table'
import {
  Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
} from '@/components/ui/select'
import { TableSearch } from '@/components/ui/table-search'
import { useTextFilter } from '@/hooks/useTextFilter'
import { RefreshCw, ChevronLeft, ChevronRight, History } from 'lucide-react'
import type { UpdateHistoryEntry } from '@/types'
import { formatRelativeTime, formatDurationShort } from '@/lib/format'

// A stack rarely has more than a screenful of update events; a high limit keeps
// the whole history on one page so the client-side text filter matches all of it.
const PAGE_LIMIT = 100

const UPDATE_SEARCH_FIELDS = [
  (e: UpdateHistoryEntry) => e.containerName,
  (e: UpdateHistoryEntry) => e.image,
  (e: UpdateHistoryEntry) => e.oldImageRef ?? '',
  (e: UpdateHistoryEntry) => e.newImageRef ?? '',
  (e: UpdateHistoryEntry) => e.status,
  (e: UpdateHistoryEntry) => e.trigger,
]

function truncateDigest(digest?: string): string {
  if (!digest) return '-'
  return digest.replace('sha256:', '').substring(0, 12)
}

const updateStatusTone: Record<string, StatusTone> = {
  success: 'success',
  failed: 'error',
  pending: 'warning',
  paused: 'warning',
}

function StatusBadge({ status }: { status: string }) {
  return <Status tone={updateStatusTone[status] || 'warning'} className="text-xs">{status}</Status>
}

function TriggerBadge({ trigger }: { trigger: string }) {
  return (
    <Status tone={trigger === 'auto' ? 'info' : 'neutral'} className="text-xs">
      {trigger}
    </Status>
  )
}

export function StackUpdatesTab({ stackId }: { stackId: string }) {
  const [page, setPage] = useState(1)
  const [statusFilter, setStatusFilter] = useState<string>('all')
  const [triggerFilter, setTriggerFilter] = useState<string>('all')

  const filters = useMemo(() => {
    const f: {
      page: number
      limit: number
      stackId: string
      status?: string
      trigger?: string
    } = { page, limit: PAGE_LIMIT, stackId }
    if (statusFilter !== 'all') f.status = statusFilter
    if (triggerFilter !== 'all') f.trigger = triggerFilter
    return f
  }, [page, statusFilter, triggerFilter, stackId])

  const { data, isLoading, isError, refetch } = useUpdateHistory(filters)

  const entries = useMemo(() => data?.entries ?? [], [data])
  const { query, setQuery, filtered } = useTextFilter(entries, UPDATE_SEARCH_FIELDS)

  const total = data?.total ?? 0
  const totalPages = data?.totalPages ?? 1

  const handleSelectChange = (setter: (v: string) => void) => (value: string) => {
    setter(value)
    setPage(1)
  }

  if (isLoading && !data) {
    return (
      <div className="space-y-2">
        <div className="flex gap-2">
          <Skeleton className="h-8 w-56" />
          <Skeleton className="h-8 w-28" />
          <Skeleton className="h-8 w-28" />
        </div>
        {Array.from({ length: 4 }).map((_, i) => (
          <Skeleton key={i} className="h-12 w-full" />
        ))}
      </div>
    )
  }

  if (isError) {
    return (
      <Card>
        <CardContent className="flex flex-col items-center justify-center py-12">
          <p className="mb-2 text-lg font-semibold">Failed to Load Update History</p>
          <p className="mb-4 text-sm text-muted-foreground">
            An error occurred while loading this stack&apos;s update history
          </p>
          <Button onClick={() => refetch()}>
            <RefreshCw className="mr-2 h-4 w-4" />
            Retry
          </Button>
        </CardContent>
      </Card>
    )
  }

  if (entries.length === 0) {
    return (
      <Card>
        <CardContent className="flex flex-col items-center justify-center py-12">
          <History className="mb-4 h-12 w-12 text-muted-foreground" />
          <p className="mb-2 text-lg font-semibold">No Update History</p>
          <p className="text-sm text-muted-foreground">
            Update events for this stack will appear here after its containers are updated.
          </p>
        </CardContent>
      </Card>
    )
  }

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-2">
        <TableSearch
          value={query}
          onChange={setQuery}
          placeholder="Filter events…"
          className="w-full sm:w-56"
        />

        <Select value={statusFilter} onValueChange={handleSelectChange(setStatusFilter)}>
          <SelectTrigger className="h-8 w-[130px] text-xs">
            <SelectValue placeholder="Status" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">All Status</SelectItem>
            <SelectItem value="success">Success</SelectItem>
            <SelectItem value="failed">Failed</SelectItem>
            <SelectItem value="pending">Pending</SelectItem>
            <SelectItem value="paused">Paused</SelectItem>
          </SelectContent>
        </Select>

        <Select value={triggerFilter} onValueChange={handleSelectChange(setTriggerFilter)}>
          <SelectTrigger className="h-8 w-[130px] text-xs">
            <SelectValue placeholder="Trigger" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">All Triggers</SelectItem>
            <SelectItem value="manual">Manual</SelectItem>
            <SelectItem value="auto">Auto</SelectItem>
          </SelectContent>
        </Select>

        <span className="ml-auto text-xs text-muted-foreground">
          {query ? `${filtered.length} of ${total}` : `${total} event${total !== 1 ? 's' : ''}`}
        </span>
      </div>

      <div className="rounded-md border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-[140px]">Time</TableHead>
              <TableHead>Container</TableHead>
              <TableHead>Image</TableHead>
              <TableHead>Version Change</TableHead>
              <TableHead>Status</TableHead>
              <TableHead>Trigger</TableHead>
              <TableHead className="w-[80px]">Duration</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {filtered.map((entry: UpdateHistoryEntry) => (
              <TableRow key={entry.id}>
                <TableCell className="text-xs text-muted-foreground">
                  {formatRelativeTime(entry.startedAt)}
                </TableCell>
                <TableCell>
                  <span className="text-sm font-medium">{entry.containerName}</span>
                </TableCell>
                <TableCell>
                  <Badge variant="secondary" className="text-xs font-mono">
                    {entry.image.length > 25 ? entry.image.substring(0, 25) + '...' : entry.image}
                  </Badge>
                </TableCell>
                <TableCell className="font-mono text-xs">
                  {truncateDigest(entry.oldDigest)} → {truncateDigest(entry.newDigest)}
                </TableCell>
                <TableCell>
                  <StatusBadge status={entry.status} />
                </TableCell>
                <TableCell>
                  <TriggerBadge trigger={entry.trigger} />
                </TableCell>
                <TableCell className="text-xs text-muted-foreground">
                  {entry.durationMs != null ? formatDurationShort(entry.durationMs) : '-'}
                </TableCell>
              </TableRow>
            ))}
            {query && filtered.length === 0 && (
              <TableRow>
                <TableCell colSpan={7} className="py-8 text-center text-sm text-muted-foreground">
                  No events match “{query}”.
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </div>

      {totalPages > 1 && (
        <div className="flex items-center justify-between">
          <span className="text-sm text-muted-foreground">
            Page {page} of {totalPages}
          </span>
          <div className="flex gap-2">
            <Button
              variant="outline"
              size="sm"
              onClick={() => setPage((p) => Math.max(1, p - 1))}
              disabled={page <= 1}
            >
              <ChevronLeft className="h-4 w-4" />
              Previous
            </Button>
            <Button
              variant="outline"
              size="sm"
              onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
              disabled={page >= totalPages}
            >
              Next
              <ChevronRight className="h-4 w-4" />
            </Button>
          </div>
        </div>
      )}
    </div>
  )
}
