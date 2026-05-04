import { useState, useMemo } from 'react'
import { useUpdateHistory } from '@/hooks/useResources'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
} from '@/components/ui/table'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { RefreshCw, ChevronLeft, ChevronRight, History } from 'lucide-react'
import type { UpdateHistoryEntry } from '@/types'
import { formatRelativeTime, formatDurationShort } from '@/lib/format'

function truncateDigest(digest?: string): string {
  if (!digest) return '-'
  const cleaned = digest.replace('sha256:', '')
  return cleaned.substring(0, 12)
}

function StatusBadgeComponent({ status }: { status: string }) {
  const variants: Record<string, { variant: 'default' | 'destructive' | 'secondary' | 'outline'; className: string }> = {
    success: { variant: 'default', className: 'bg-green-100 text-green-800 dark:bg-green-900 dark:text-green-200' },
    failed: { variant: 'destructive', className: '' },
    pending: { variant: 'secondary', className: 'bg-yellow-100 text-yellow-800 dark:bg-yellow-900 dark:text-yellow-200' },
    paused: { variant: 'outline', className: 'border-orange-400 text-orange-700 dark:text-orange-300' },
  }
  const config = variants[status] || variants.pending
  return <Badge variant={config.variant} className={`text-xs ${config.className}`}>{status}</Badge>
}

function TriggerBadgeComponent({ trigger }: { trigger: string }) {
  return (
    <Badge
      variant="outline"
      className={`text-xs ${trigger === 'auto'
        ? 'border-purple-400 text-purple-700 dark:text-purple-300'
        : 'border-blue-400 text-blue-700 dark:text-blue-300'
      }`}
    >
      {trigger}
    </Badge>
  )
}

export function UpdateLogTab() {
  const [page, setPage] = useState(1)
  const [statusFilter, setStatusFilter] = useState<string>('all')
  const [triggerFilter, setTriggerFilter] = useState<string>('all')
  const [dateRange, setDateRange] = useState<string>('all')

  const filters = useMemo(() => {
    const f: {
      page: number
      limit: number
      status?: string
      trigger?: string
      from?: string
    } = { page, limit: 25 }

    if (statusFilter && statusFilter !== 'all') f.status = statusFilter
    if (triggerFilter && triggerFilter !== 'all') f.trigger = triggerFilter

    if (dateRange !== 'all') {
      const now = new Date()
      let from: Date
      switch (dateRange) {
        case '24h': from = new Date(now.getTime() - 24 * 60 * 60 * 1000); break
        case '7d': from = new Date(now.getTime() - 7 * 24 * 60 * 60 * 1000); break
        case '30d': from = new Date(now.getTime() - 30 * 24 * 60 * 60 * 1000); break
        default: from = now
      }
      f.from = from.toISOString()
    }

    return f
  }, [page, statusFilter, triggerFilter, dateRange])

  const { data, isLoading, isError, refetch } = useUpdateHistory(filters)

  const handleFilterChange = (setter: (v: string) => void) => (value: string) => {
    setter(value)
    setPage(1)
  }

  if (isLoading && !data) {
    return (
      <div className="space-y-2">
        <div className="flex gap-2">
          <Skeleton className="h-9 w-28" />
          <Skeleton className="h-9 w-28" />
          <Skeleton className="h-9 w-28" />
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
          <p className="text-lg font-semibold mb-2">Failed to Load Update History</p>
          <p className="text-sm text-muted-foreground mb-4">
            An error occurred while loading update history
          </p>
          <Button onClick={() => refetch()}>
            <RefreshCw className="mr-2 h-4 w-4" />
            Retry
          </Button>
        </CardContent>
      </Card>
    )
  }

  const entries = data?.entries ?? []
  const total = data?.total ?? 0
  const totalPages = data?.totalPages ?? 1

  if (entries.length === 0 && !isLoading) {
    return (
      <Card>
        <CardContent className="flex flex-col items-center justify-center py-12">
          <History className="h-12 w-12 text-muted-foreground mb-4" />
          <p className="text-lg font-semibold mb-2">No Update History</p>
          <p className="text-sm text-muted-foreground">
            Updates will appear here after containers are updated.
          </p>
        </CardContent>
      </Card>
    )
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center gap-2 flex-wrap">
        <Select value={statusFilter} onValueChange={handleFilterChange(setStatusFilter)}>
          <SelectTrigger className="w-[130px] h-8 text-xs">
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

        <Select value={triggerFilter} onValueChange={handleFilterChange(setTriggerFilter)}>
          <SelectTrigger className="w-[130px] h-8 text-xs">
            <SelectValue placeholder="Trigger" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">All Triggers</SelectItem>
            <SelectItem value="manual">Manual</SelectItem>
            <SelectItem value="auto">Auto</SelectItem>
          </SelectContent>
        </Select>

        <Select value={dateRange} onValueChange={handleFilterChange(setDateRange)}>
          <SelectTrigger className="w-[130px] h-8 text-xs">
            <SelectValue placeholder="Date Range" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">All Time</SelectItem>
            <SelectItem value="24h">Last 24h</SelectItem>
            <SelectItem value="7d">Last 7 days</SelectItem>
            <SelectItem value="30d">Last 30 days</SelectItem>
          </SelectContent>
        </Select>

        <span className="text-xs text-muted-foreground ml-auto">
          {total} record{total !== 1 ? 's' : ''}
        </span>
      </div>

      <div className="rounded-md border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-[140px]">Time</TableHead>
              <TableHead>Container</TableHead>
              <TableHead>Stack</TableHead>
              <TableHead>Image</TableHead>
              <TableHead>Version Change</TableHead>
              <TableHead>Status</TableHead>
              <TableHead>Trigger</TableHead>
              <TableHead className="w-[80px]">Duration</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {entries.map((entry: UpdateHistoryEntry) => (
              <TableRow key={entry.id}>
                <TableCell className="text-xs text-muted-foreground">
                  {formatRelativeTime(entry.startedAt)}
                </TableCell>
                <TableCell>
                  <span className="text-sm font-medium">{entry.containerName}</span>
                </TableCell>
                <TableCell>
                  {entry.stackName ? (
                    <a
                      href={`/stacks/${entry.stackId}`}
                      className="text-sm text-blue-500 hover:underline"
                    >
                      {entry.stackName}
                    </a>
                  ) : (
                    <span className="text-xs text-muted-foreground italic">standalone</span>
                  )}
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
                  <StatusBadgeComponent status={entry.status} />
                </TableCell>
                <TableCell>
                  <TriggerBadgeComponent trigger={entry.trigger} />
                </TableCell>
                <TableCell className="text-xs text-muted-foreground">
                  {entry.durationMs != null ? formatDurationShort(entry.durationMs) : '-'}
                </TableCell>
              </TableRow>
            ))}
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
