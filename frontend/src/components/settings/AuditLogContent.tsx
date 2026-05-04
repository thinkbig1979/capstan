import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { settingsApi } from '@/lib/api'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Button } from '@/components/ui/button'
import { LoadingSpinner } from '@/components/LoadingSkeleton'
import { ChevronLeft, ChevronRight, ScrollText } from 'lucide-react'

export function AuditLogContent() {
  const [page, setPage] = useState(1)
  const pageSize = 50

  const { data, isLoading, error } = useQuery({
    queryKey: ['audit-log', page, pageSize],
    queryFn: () => settingsApi.getAuditLog(page, pageSize),
  })

  if (isLoading) {
    return (
      <div className="flex items-center justify-center py-8">
        <LoadingSpinner size="large" />
      </div>
    )
  }

  if (error) {
    return (
      <div className="text-center py-8 text-muted-foreground">
        Failed to load audit log.
      </div>
    )
  }

  if (!data || data.entries.length === 0) {
    return (
      <div className="text-center py-8 text-muted-foreground">
        <ScrollText className="h-8 w-8 mx-auto mb-2 opacity-50" />
        No audit log entries yet.
      </div>
    )
  }

  const totalPages = Math.ceil(data.total / pageSize)

  return (
    <div className="space-y-4">
      <div className="rounded-md border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Timestamp</TableHead>
              <TableHead>User</TableHead>
              <TableHead>Action</TableHead>
              <TableHead>Detail</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {data.entries.map((entry) => (
              <TableRow key={entry.id}>
                <TableCell className="text-sm whitespace-nowrap">
                  {new Date(entry.createdAt).toLocaleString()}
                </TableCell>
                <TableCell className="text-sm font-mono">
                  {entry.userId.slice(0, 8)}
                </TableCell>
                <TableCell className="text-sm">
                  <span className="inline-flex items-center rounded-md bg-primary/10 px-2 py-0.5 text-xs font-medium text-primary">
                    {entry.action}
                  </span>
                </TableCell>
                <TableCell className="text-sm text-muted-foreground max-w-md truncate">
                  {entry.detail}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>

      {totalPages > 1 && (
        <div className="flex items-center justify-between">
          <p className="text-sm text-muted-foreground">
            Showing {(page - 1) * pageSize + 1}-{Math.min(page * pageSize, data.total)} of {data.total}
          </p>
          <div className="flex gap-2">
            <Button
              variant="outline"
              size="sm"
              onClick={() => setPage((p) => Math.max(1, p - 1))}
              disabled={page <= 1}
            >
              <ChevronLeft className="h-4 w-4 mr-1" />
              Previous
            </Button>
            <Button
              variant="outline"
              size="sm"
              onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
              disabled={page >= totalPages}
            >
              Next
              <ChevronRight className="h-4 w-4 ml-1" />
            </Button>
          </div>
        </div>
      )}
    </div>
  )
}
