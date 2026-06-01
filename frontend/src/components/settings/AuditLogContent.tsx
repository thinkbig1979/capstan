import { Fragment, useEffect, useState } from 'react'
import { useQuery, keepPreviousData } from '@tanstack/react-query'
import { settingsApi } from '@/lib/api'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
} from '@/components/ui/select'
import { LoadingSpinner } from '@/components/LoadingSkeleton'
import { ChevronLeft, ChevronRight, ChevronDown, ScrollText, X } from 'lucide-react'

const ALL_ACTIONS = '__all__'

/** Turn a raw JSON detail string into a readable one-line summary plus the
 *  pretty-printed original (shown on expand). Falls back to plain text. */
function humanizeDetail(detail: string): { summary: string; raw: string | null } {
  if (!detail) return { summary: '—', raw: null }
  try {
    const obj = JSON.parse(detail)
    if (obj && typeof obj === 'object' && !Array.isArray(obj)) {
      const parts = Object.entries(obj).map(([key, value]) => {
        let display: string
        if (typeof value === 'boolean') {
          display = value ? 'yes' : 'no'
        } else {
          display = String(value)
          // Shorten id-like values so the summary stays scannable
          if (display.length > 12 && /^[0-9a-f-]{16,}$/i.test(display)) {
            display = `${display.slice(0, 8)}…`
          }
        }
        return `${key.replace(/_/g, ' ')}: ${display}`
      })
      return { summary: parts.join(' · '), raw: JSON.stringify(obj, null, 2) }
    }
  } catch {
    // not JSON — show as-is
  }
  return { summary: detail, raw: null }
}

export function AuditLogContent() {
  const [page, setPage] = useState(1)
  const [expanded, setExpanded] = useState<Set<string>>(new Set())
  const pageSize = 50

  const [action, setAction] = useState('')
  const [searchInput, setSearchInput] = useState('')
  const [search, setSearch] = useState('')
  const [dateFrom, setDateFrom] = useState('')
  const [dateTo, setDateTo] = useState('')

  const toggleExpanded = (id: string) =>
    setExpanded((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })

  // Debounce the free-text search so we don't refetch on every keystroke
  useEffect(() => {
    const t = setTimeout(() => setSearch(searchInput.trim()), 300)
    return () => clearTimeout(t)
  }, [searchInput])

  // Any filter change resets to the first page
  useEffect(() => {
    setPage(1)
  }, [action, search, dateFrom, dateTo])

  const hasActiveFilters = Boolean(action || search || dateFrom || dateTo)

  const clearFilters = () => {
    setAction('')
    setSearchInput('')
    setSearch('')
    setDateFrom('')
    setDateTo('')
  }

  const { data, isLoading, error } = useQuery({
    queryKey: ['audit-log', page, pageSize, action, search, dateFrom, dateTo],
    queryFn: () => settingsApi.getAuditLog(page, pageSize, { action, search, dateFrom, dateTo }),
    placeholderData: keepPreviousData,
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

  // Truly empty log (no entries and no filter applied) — skip the filter bar
  if (!data || (data.entries.length === 0 && !hasActiveFilters)) {
    return (
      <div className="text-center py-8 text-muted-foreground">
        <ScrollText className="h-8 w-8 mx-auto mb-2 opacity-50" />
        No audit log entries yet.
      </div>
    )
  }

  const totalPages = Math.ceil(data.total / pageSize)
  const availableActions = data.availableActions ?? []

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-end gap-3">
        <div className="flex-1 min-w-[180px] space-y-1">
          <label htmlFor="audit-search" className="text-xs text-muted-foreground">Search</label>
          <Input
            id="audit-search"
            type="search"
            placeholder="Search detail or action"
            value={searchInput}
            onChange={(e) => setSearchInput(e.target.value)}
            className="h-9"
          />
        </div>
        <div className="space-y-1">
          <label className="text-xs text-muted-foreground">Action</label>
          <Select
            value={action || ALL_ACTIONS}
            onValueChange={(v) => setAction(v === ALL_ACTIONS ? '' : v)}
          >
            <SelectTrigger className="h-9 w-[180px]">
              <SelectValue placeholder="All actions" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={ALL_ACTIONS}>All actions</SelectItem>
              {availableActions.map((a) => (
                <SelectItem key={a} value={a}>{a}</SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
        <div className="space-y-1">
          <label htmlFor="audit-from" className="text-xs text-muted-foreground">From</label>
          <Input
            id="audit-from"
            type="date"
            value={dateFrom}
            onChange={(e) => setDateFrom(e.target.value)}
            className="h-9 w-[150px]"
          />
        </div>
        <div className="space-y-1">
          <label htmlFor="audit-to" className="text-xs text-muted-foreground">To</label>
          <Input
            id="audit-to"
            type="date"
            value={dateTo}
            onChange={(e) => setDateTo(e.target.value)}
            className="h-9 w-[150px]"
          />
        </div>
        {hasActiveFilters && (
          <Button variant="ghost" size="sm" className="h-9" onClick={clearFilters}>
            <X className="h-4 w-4 mr-1" />
            Clear
          </Button>
        )}
      </div>

      {data.entries.length === 0 ? (
        <div className="text-center py-8 text-muted-foreground">
          <ScrollText className="h-8 w-8 mx-auto mb-2 opacity-50" />
          No entries match these filters.
        </div>
      ) : (
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
            {data.entries.map((entry) => {
              const { summary, raw } = humanizeDetail(entry.detail)
              const isExpanded = expanded.has(entry.id)
              return (
                <Fragment key={entry.id}>
                  <TableRow>
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
                    <TableCell className="text-sm text-muted-foreground">
                      <div className="flex items-center gap-2">
                        <span className="truncate">{summary}</span>
                        {raw && (
                          <Button
                            variant="ghost"
                            size="sm"
                            className="h-6 shrink-0 px-1.5 text-xs text-muted-foreground"
                            onClick={() => toggleExpanded(entry.id)}
                            aria-expanded={isExpanded}
                            aria-label={isExpanded ? 'Hide raw detail' : 'View raw detail'}
                          >
                            <ChevronDown
                              className={`h-3.5 w-3.5 transition-transform ${isExpanded ? 'rotate-180' : ''}`}
                            />
                            {isExpanded ? 'Hide raw' : 'View raw'}
                          </Button>
                        )}
                      </div>
                    </TableCell>
                  </TableRow>
                  {isExpanded && raw && (
                    <TableRow>
                      <TableCell colSpan={4} className="bg-muted/30">
                        <pre className="overflow-x-auto whitespace-pre-wrap break-all font-mono text-xs text-muted-foreground">
                          {raw}
                        </pre>
                      </TableCell>
                    </TableRow>
                  )}
                </Fragment>
              )
            })}
          </TableBody>
        </Table>
      </div>
      )}

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
