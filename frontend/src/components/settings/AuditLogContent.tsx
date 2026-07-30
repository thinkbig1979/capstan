import { Fragment, useEffect, useState } from 'react'
import { useQuery, keepPreviousData } from '@tanstack/react-query'
import { settingsApi } from '@/lib/api'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { TableSearch } from '@/components/ui/table-search'
import {
  Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
} from '@/components/ui/select'
import { LoadingSpinner } from '@/components/LoadingSkeleton'
import { HelpHint } from '@/components/ui/help-hint'
import { ChevronLeft, ChevronRight, ChevronDown, ScrollText, X } from 'lucide-react'
import { queryKeys } from '@/lib/query-keys'

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

/** Documents exactly which actions land in the audit log, surfaced via a help
 *  icon next to the log so the scope is discoverable without reading the docs. */
function AuditedEventsNote() {
  return (
    <div className="flex items-center gap-1.5 text-sm text-muted-foreground">
      <span>Records security-relevant actions: who did what, and when.</span>
      <HelpHint label="Audited events" title="What gets recorded" side="bottom" align="start">
        <p>Each entry captures the user, action, timestamp, and non-sensitive details:</p>
        <ul className="list-disc space-y-0.5 pl-4">
          <li><strong>Authentication</strong> — login, failed login (on existing accounts), logout, first-run setup</li>
          <li><strong>Stacks</strong> — start, stop, restart, pull, create, delete, compose and env edits, git pull</li>
          <li><strong>Updates</strong> — manual update scans, container and stack image updates</li>
          <li><strong>Docker resources</strong> — deleting containers, images, volumes and networks; creating networks; prune operations</li>
          <li><strong>Settings</strong> — global env, git credentials, update schedule, log retention, scan depth, default directory, password change</li>
          <li><strong>Backups</strong> — backup and restore</li>
        </ul>
        <p className="text-xs">Secrets (passwords, tokens, env values) are never written to the log.</p>
      </HelpHint>
    </div>
  )
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

  // Any filter change resets to the first page. Adjusted during render (rather
  // than in an effect) by comparing against the filter combination from the
  // previous render — see https://react.dev/learn/you-might-not-need-an-effect.
  const filterKey = `${action}\0${search}\0${dateFrom}\0${dateTo}`
  const [prevFilterKey, setPrevFilterKey] = useState(filterKey)
  if (filterKey !== prevFilterKey) {
    setPrevFilterKey(filterKey)
    setPage(1)
  }

  const hasActiveFilters = Boolean(action || search || dateFrom || dateTo)

  const clearFilters = () => {
    setAction('')
    setSearchInput('')
    setSearch('')
    setDateFrom('')
    setDateTo('')
  }

  const { data, isLoading, error } = useQuery({
    queryKey: queryKeys.auditLog({ page, pageSize, action, search, dateFrom, dateTo }),
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
      <div className="space-y-4">
        <AuditedEventsNote />
        <div className="text-center py-8 text-muted-foreground">
          <ScrollText className="h-8 w-8 mx-auto mb-2 opacity-50" />
          No audit log entries yet.
        </div>
      </div>
    )
  }

  const totalPages = Math.ceil(data.total / pageSize)
  const availableActions = data.availableActions ?? []

  return (
    <div className="space-y-4">
      <AuditedEventsNote />
      <div className="flex flex-wrap items-end gap-3">
        <div className="flex-1 min-w-[180px] space-y-1">
          <label htmlFor="audit-search" className="text-xs text-muted-foreground">Search</label>
          <TableSearch
            id="audit-search"
            value={searchInput}
            onChange={setSearchInput}
            placeholder="Search detail or action"
            className="h-9"
          />
        </div>
        <div className="space-y-1">
          <label htmlFor="audit-action" className="text-xs text-muted-foreground">Action</label>
          <Select
            value={action || ALL_ACTIONS}
            onValueChange={(v) => setAction(v === ALL_ACTIONS ? '' : v)}
          >
            <SelectTrigger id="audit-action" className="h-9 w-[180px]">
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
