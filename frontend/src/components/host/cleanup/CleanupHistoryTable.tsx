import { LoadingSpinner } from '@/components/LoadingSkeleton'
import { Badge } from '@/components/ui/badge'
import {
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
} from '@/components/ui/table'
import { RefreshFailedNotice } from '@/components/RefreshFailedNotice'
import { formatBytes, formatRelativeTime, formatDateFull } from '@/lib/format'
import { useDockerCleanupHistory } from '@/hooks/useResources'
import { runFreedBytes, runRemovedSummary } from './categories'

/** Recorded cleanup runs, newest first. */
export function CleanupHistoryTable() {
  const history = useDockerCleanupHistory()

  return (
    <div className="space-y-3">
      {/* agent-os-wczm: `!history.data` alone decides the unreadable state;
          `history.isError` is true for a failed REFETCH too, and one 500 must
          not replace a table of real runs. */}
      {history.isError && !!history.data && (
        // agent-os-6iui: every run-history table offers a way out of a stale view.
        <RefreshFailedNotice what="the cleanup run history" onRetry={() => history.refetch()} />
      )}
      {/* Three states, three sentences: an unreadable history is not an empty
          one, and the operator's next move differs. */}
      {history.isPending ? (
        <LoadingSpinner />
      ) : !history.data ? (
        <p className="text-sm text-destructive">Run history is unavailable.</p>
      ) : history.data.runs.length === 0 ? (
        <p className="text-sm text-muted-foreground">No cleanup runs yet.</p>
      ) : (
        <Table>
          <TableHeader>
            {/* No Finished column: finishedAt answers nothing an operator acts
                on, and it is optional on the wire, so a date formatter would
                turn its absence into "Invalid Date". */}
            <TableRow>
              <TableHead>Started</TableHead>
              <TableHead>Trigger</TableHead>
              <TableHead>Status</TableHead>
              <TableHead>Removed</TableHead>
              <TableHead>Freed</TableHead>
              <TableHead>Age floor</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {history.data.runs.map((run) => (
              <TableRow key={run.id}>
                <TableCell title={formatDateFull(run.startedAt)}>
                  {formatRelativeTime(run.startedAt)}
                </TableCell>
                <TableCell>{run.trigger}</TableCell>
                <TableCell>
                  <Badge variant={run.status === 'failed' ? 'destructive' : 'secondary'}>
                    {run.status}
                  </Badge>
                  {/* Guarded, never interpolated: errorMessage is omitted from
                      every successful run, and a template would print "undefined". */}
                  {run.errorMessage && (
                    <p className={`mt-1 text-xs ${run.status === 'failed' ? 'text-destructive' : 'text-muted-foreground'}`}>
                      {run.status === 'skipped' ? `Nothing was removed: ${run.errorMessage}` : run.errorMessage}
                    </p>
                  )}
                </TableCell>
                <TableCell>{runRemovedSummary(run) ?? 'Nothing'}</TableCell>
                <TableCell>{formatBytes(runFreedBytes(run))}</TableCell>
                {/* Keyed on 0, not on status: the server clamps every floor a real
                    run applies to at least 1, so 0 only ever means no floor was
                    applied, as on a scheduled pass that never ran (agent-os-8fw2). */}
                <TableCell>{run.minAgeHours === 0 ? '—' : `${run.minAgeHours} h`}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </div>
  )
}
