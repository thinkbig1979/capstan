import { useState, useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import { backupApi } from '@/lib/api'
import { queryKeys } from '@/lib/query-keys'
import { useBackupHistory } from '@/hooks/useBackup'
import { useTextFilter } from '@/hooks/useTextFilter'
import { TableSearch } from '@/components/ui/table-search'
import { Button } from '@/components/ui/button'
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
import {
  RefreshCw, ChevronLeft, ChevronRight, ChevronDown, ChevronUp, Archive, Loader2, AlertCircle,
} from 'lucide-react'
import type { BackupHistoryFilters, BackupRun } from '@/types'
import { formatRelativeTime, formatDurationShort, formatBytes } from '@/lib/format'
import { RunStatusBadge } from './backup-run-status'

const RUN_SEARCH_FIELDS = [
  (r: BackupRun) => r.id,
  (r: BackupRun) => r.kind,
  (r: BackupRun) => r.trigger,
  (r: BackupRun) => r.status,
  (r: BackupRun) => r.startedAt,
]

const COLUMN_COUNT = 8

/**
 * BackupRun carries no duration field, only the two timestamps, so the column
 * is derived. A run with no finishedAt has not ended (or never recorded an
 * end) and gets a dash rather than a made-up number.
 */
function runDuration(run: BackupRun): string {
  if (!run.finishedAt) return '-'
  const ms = new Date(run.finishedAt).getTime() - new Date(run.startedAt).getTime()
  if (!Number.isFinite(ms) || ms < 0) return '-'
  return formatDurationShort(ms)
}

/**
 * One run's per-stack records, fetched only once the row is expanded.
 *
 * The staleTime is the reason collapsing and re-expanding does not re-issue
 * the request: this component unmounts on collapse, and without it react-query
 * would treat the cached answer as stale on every remount. A finished run's
 * item list is immutable, so serving it from cache is correct, not merely
 * cheap.
 */
function useBackupRunDetail(runId: string) {
  return useQuery({
    queryKey: queryKeys.backup.run(runId),
    queryFn: () => backupApi.getRun(runId),
    staleTime: 5 * 60 * 1000,
  })
}

const ITEM_STATUS_CLASS: Record<'skipped' | 'success' | 'failed', string> = {
  success: 'text-green-700 dark:text-green-400',
  failed: 'text-destructive',
  skipped: 'text-muted-foreground',
}

function RunDetail({ runId }: { runId: string }) {
  const { data, isLoading, isError } = useBackupRunDetail(runId)

  if (isLoading) {
    return (
      <div className="flex items-center gap-2 px-4 py-3 text-sm text-muted-foreground">
        <Loader2 className="h-4 w-4 animate-spin" />
        Loading run details…
      </div>
    )
  }

  if (isError) {
    return (
      <div className="flex items-center gap-2 px-4 py-3 text-sm text-destructive">
        <AlertCircle className="h-4 w-4" />
        Failed to load run details.
      </div>
    )
  }

  const items = data?.items ?? []

  if (items.length === 0) {
    return (
      <div className="px-4 py-3 text-sm text-muted-foreground">
        No per-stack records for this run.
      </div>
    )
  }

  return (
    <div className="px-4 py-3">
      <ul className="space-y-1.5">
        {items.map((it) => (
          <li key={it.id} className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs">
            <span className="font-medium text-foreground">{it.stackId}</span>
            <span
              data-testid={`run-item-status-${it.id}`}
              className={ITEM_STATUS_CLASS[it.status]}
            >
              {it.status}
            </span>
            <span className="text-muted-foreground">{formatDurationShort(it.durationMs)}</span>
            {it.snapshotId && (
              <span className="font-mono text-muted-foreground">{it.snapshotId.slice(0, 8)}</span>
            )}
            {it.stopApplied && <span className="text-muted-foreground">stopped</span>}
            {it.errorMessage && <span className="text-destructive">{it.errorMessage}</span>}
          </li>
        ))}
      </ul>
    </div>
  )
}

function RunRow({ run }: { run: BackupRun }) {
  const [expanded, setExpanded] = useState(false)

  return (
    <>
      <TableRow>
        <TableCell className="w-[40px]">
          <Button
            variant="ghost"
            size="sm"
            className="h-7 w-7 p-0"
            aria-label={
              expanded ? `Hide details for run ${run.id}` : `Show details for run ${run.id}`
            }
            onClick={() => setExpanded((v) => !v)}
          >
            {expanded ? <ChevronUp className="h-4 w-4" /> : <ChevronDown className="h-4 w-4" />}
          </Button>
        </TableCell>
        <TableCell className="text-xs text-muted-foreground">
          <div>{formatRelativeTime(run.startedAt)}</div>
          {/* Short id, the same 8-char convention BackupsTab uses for snapshots:
              enough to correlate a row with a server log line without eating
              the column. */}
          <div className="font-mono opacity-70">{run.id.slice(0, 8)}</div>
        </TableCell>
        <TableCell className="text-sm">{run.kind}</TableCell>
        <TableCell className="text-sm text-muted-foreground">{run.trigger}</TableCell>
        <TableCell>
          <RunStatusBadge status={run.status} />
        </TableCell>
        <TableCell className="text-xs text-muted-foreground">
          {run.stacksOk} ok / {run.stacksFailed} failed
        </TableCell>
        <TableCell className="text-xs text-muted-foreground">
          {run.bytesAdded == null ? (
            <span title="Not recorded">-</span>
          ) : run.bytesAdded === 0 ? (
            'No change'
          ) : (
            formatBytes(run.bytesAdded)
          )}
        </TableCell>
        <TableCell
          data-testid={`run-duration-${run.id}`}
          className="text-xs text-muted-foreground"
        >
          {runDuration(run)}
        </TableCell>
      </TableRow>
      {expanded && (
        <TableRow>
          <TableCell colSpan={COLUMN_COUNT} className="bg-muted/30 p-0">
            <RunDetail runId={run.id} />
          </TableCell>
        </TableRow>
      )}
    </>
  )
}

export function BackupHistoryTab() {
  const [page, setPage] = useState(1)
  const [statusFilter, setStatusFilter] = useState<string>('all')
  const [kindFilter, setKindFilter] = useState<string>('all')
  const [triggerFilter, setTriggerFilter] = useState<string>('all')
  const [dateRange, setDateRange] = useState<string>('all')

  const filters = useMemo(() => {
    const f: BackupHistoryFilters = { page, limit: 25 }

    if (statusFilter !== 'all') f.status = statusFilter
    if (kindFilter !== 'all') f.kind = kindFilter
    if (triggerFilter !== 'all') f.trigger = triggerFilter

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
  }, [page, statusFilter, kindFilter, triggerFilter, dateRange])

  const { data, isLoading, isError, refetch } = useBackupHistory(filters)

  // The handler guarantees an array, so this default only covers the
  // pre-first-response render, never a null payload.
  const runs = useMemo(() => data?.runs ?? [], [data])
  const { query, setQuery, filtered } = useTextFilter(runs, RUN_SEARCH_FIELDS)

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
          <p className="text-lg font-semibold mb-2">Failed to Load Backup History</p>
          <p className="text-sm text-muted-foreground mb-4">
            An error occurred while loading backup run history
          </p>
          <Button onClick={() => refetch()}>
            <RefreshCw className="mr-2 h-4 w-4" />
            Retry
          </Button>
        </CardContent>
      </Card>
    )
  }

  const total = data?.total ?? 0
  // 0 when the server reports no runs at all; the empty branch below returns
  // first in that case, so the pager condition only ever sees 1 or more.
  const totalPages = data?.totalPages ?? 1

  if (runs.length === 0 && !isLoading) {
    return (
      <Card>
        <CardContent className="flex flex-col items-center justify-center py-12">
          <Archive className="h-12 w-12 text-muted-foreground mb-4" />
          <p className="text-lg font-semibold mb-2">No backup history</p>
          <p className="text-sm text-muted-foreground">
            Backup runs will appear here once backups have run. Enable and schedule them
            under Settings → Backup.
          </p>
        </CardContent>
      </Card>
    )
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center gap-2 flex-wrap">
        <TableSearch
          value={query}
          onChange={setQuery}
          placeholder="Filter runs…"
          className="w-full sm:w-56"
        />

        <Select value={statusFilter} onValueChange={handleFilterChange(setStatusFilter)}>
          <SelectTrigger className="w-[130px] h-8 text-xs">
            <SelectValue placeholder="Status" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">All Status</SelectItem>
            <SelectItem value="running">Running</SelectItem>
            <SelectItem value="success">Success</SelectItem>
            <SelectItem value="partial">Partial</SelectItem>
            <SelectItem value="failed">Failed</SelectItem>
            <SelectItem value="interrupted">Interrupted</SelectItem>
          </SelectContent>
        </Select>

        <Select value={kindFilter} onValueChange={handleFilterChange(setKindFilter)}>
          <SelectTrigger className="w-[130px] h-8 text-xs">
            <SelectValue placeholder="Kind" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">All Kinds</SelectItem>
            <SelectItem value="backup">Backup</SelectItem>
            <SelectItem value="sync">Sync</SelectItem>
            <SelectItem value="restore">Restore</SelectItem>
            <SelectItem value="dr_restore">DR Restore</SelectItem>
            <SelectItem value="prune">Prune</SelectItem>
          </SelectContent>
        </Select>

        <Select value={triggerFilter} onValueChange={handleFilterChange(setTriggerFilter)}>
          <SelectTrigger className="w-[130px] h-8 text-xs">
            <SelectValue placeholder="Trigger" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">All Triggers</SelectItem>
            <SelectItem value="manual">Manual</SelectItem>
            <SelectItem value="scheduled">Scheduled</SelectItem>
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
          {query ? `${filtered.length} of ${total} record${total !== 1 ? 's' : ''}` : `${total} record${total !== 1 ? 's' : ''}`}
        </span>
      </div>

      <div className="rounded-md border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-[40px]"><span className="sr-only">Expand</span></TableHead>
              <TableHead className="w-[120px]">Started</TableHead>
              <TableHead>Kind</TableHead>
              <TableHead>Trigger</TableHead>
              <TableHead>Status</TableHead>
              <TableHead>Stacks</TableHead>
              <TableHead>
                <span title="New data written to the repository. restic deduplicates, so an unchanged backup adds little or nothing.">
                  New data
                </span>
              </TableHead>
              <TableHead className="w-[80px]">Duration</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {filtered.map((run: BackupRun) => (
              <RunRow key={run.id} run={run} />
            ))}
            {query && filtered.length === 0 && (
              <TableRow>
                <TableCell colSpan={COLUMN_COUNT} className="py-8 text-center text-sm text-muted-foreground">
                  No runs match &quot;{query}&quot;.
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
