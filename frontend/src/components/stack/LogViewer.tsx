import { useState, useRef, useEffect, useCallback, useMemo } from 'react'
import { useWebSocketJSON } from '@/hooks/useWebSocket'
import { formatDateTimeLocal } from '@/lib/format'
import { useUIStore, type LogTimeRange } from '@/stores/uiStore'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuCheckboxItem,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
} from '@/components/ui/dropdown-menu'
import {
  ArrowDown,
  Search,
  Filter,
  Clock,
  Trash2,
  Download,
  RotateCcw,
  AlertCircle,
  AlertTriangle,
  Calendar,
  WrapText,
  X,
} from 'lucide-react'
import { cn } from '@/lib/utils'
import { parseAnsi, hasAnsi, stripAnsi, ansiSegmentClassName } from '@/lib/ansi'

interface LogMessage {
  container: string
  timestamp: string
  message: string
}

interface TimeRangeConfig {
  label: string
  value: LogTimeRange
  getStartTime: () => Date | null
}

interface LogViewerProps {
  stackId: string
  initialContainer?: string
  hasRunningContainers?: boolean
}

const MAX_LOG_BUFFER = 10000
// Per-container identifier colors. Not status — purely a visual diff between
// container names in interleaved log output. Assigned round-robin in order of
// first appearance (see containerColors below), so the first 10 distinct
// services are always different colors. Each entry pairs a darker shade for
// light themes with a lighter shade for dark themes so the bracket stays
// legible on the muted log background in both.
const CONTAINER_COLORS = [
  'text-red-600 dark:text-red-400',
  'text-orange-600 dark:text-orange-400',
  'text-amber-600 dark:text-amber-400',
  'text-green-600 dark:text-green-400',
  'text-teal-600 dark:text-teal-400',
  'text-sky-600 dark:text-sky-400',
  'text-blue-600 dark:text-blue-400',
  'text-violet-600 dark:text-violet-400',
  'text-fuchsia-600 dark:text-fuchsia-400',
  'text-rose-600 dark:text-rose-400',
]

const TIME_RANGE_OPTIONS: TimeRangeConfig[] = [
  {
    label: 'All',
    value: 'all',
    getStartTime: () => null,
  },
  {
    label: 'Last 5 min',
    value: '5m',
    getStartTime: () => new Date(Date.now() - 5 * 60 * 1000),
  },
  {
    label: 'Last 15 min',
    value: '15m',
    getStartTime: () => new Date(Date.now() - 15 * 60 * 1000),
  },
  {
    label: 'Last 1 hr',
    value: '1h',
    getStartTime: () => new Date(Date.now() - 60 * 60 * 1000),
  },
  {
    label: 'Custom',
    value: 'custom',
    getStartTime: () => null,
  },
]

type LogLevel = 'error' | 'warn' | 'other'

function getLogLevel(message: string): LogLevel {
  const upperMsg = message.toUpperCase()
  if (upperMsg.includes('ERROR') || upperMsg.includes('FATAL') || upperMsg.includes('PANIC')) {
    return 'error'
  }
  if (upperMsg.includes('WARN') || upperMsg.includes('WARNING')) {
    return 'warn'
  }
  return 'other'
}

function getLogLevelColor(message: string): string {
  switch (getLogLevel(message)) {
    case 'error':
      return 'text-destructive'
    case 'warn':
      return 'text-warning'
    default:
      return ''
  }
}

function isEditableTarget(target: EventTarget | null): boolean {
  const el = target as HTMLElement | null
  if (!el) return false
  const tag = el.tagName
  return tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || el.isContentEditable
}

function escapeRegExp(str: string): string {
  return str.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

function highlightSearchTerm(text: string, searchTerm: string): React.ReactNode {
  if (!searchTerm) return text

  const escaped = escapeRegExp(searchTerm)
  const parts = text.split(new RegExp(`(${escaped})`, 'gi'))
  return parts.map((part, i) =>
    part.toLowerCase() === searchTerm.toLowerCase() ? (
      <mark key={i} className="bg-warning/40 text-warning-foreground rounded px-0.5">
        {part}
      </mark>
    ) : (
      <span key={i}>{part}</span>
    )
  )
}

// Render a log message: ANSI-styled spans when escape codes are present,
// otherwise plain text. Search matches are highlighted within either path.
function renderMessage(message: string, searchTerm: string): React.ReactNode {
  if (!hasAnsi(message)) {
    return highlightSearchTerm(message, searchTerm)
  }
  return parseAnsi(message).map((seg, i) => (
    <span key={i} className={ansiSegmentClassName(seg)}>
      {highlightSearchTerm(seg.text, searchTerm)}
    </span>
  ))
}

export function LogViewer({ stackId, initialContainer, hasRunningContainers = true }: LogViewerProps) {
  const logPrefs = useUIStore((s) => s.logPrefs)
  const setLogPrefs = useUIStore((s) => s.setLogPrefs)
  const { showTimestamps, autoScroll, wrap, errorsOnly, timeRange } = logPrefs

  const [logs, setLogs] = useState<LogMessage[]>([])
  const [searchTerm, setSearchTerm] = useState('')
  const [selectedContainers, setSelectedContainers] = useState<string[]>(
    initialContainer ? [initialContainer] : []
  )
  const [uniqueContainers, setUniqueContainers] = useState<Set<string>>(new Set())
  const [scrolledUp, setScrolledUp] = useState(false)
  const [newCount, setNewCount] = useState(0)
  const [customStartTime, setCustomStartTime] = useState<Date | null>(null)
  const [customEndTime, setCustomEndTime] = useState<Date | null>(null)

  const logsRef = useRef<LogMessage[]>([])
  const batchRef = useRef<LogMessage[]>([])
  const flushTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const logContainerRef = useRef<HTMLDivElement>(null)
  const isAutoScrollingRef = useRef(false)
  const searchInputRef = useRef<HTMLInputElement>(null)
  const prevLenRef = useRef(0)

  const setTimeRange = useCallback(
    (range: LogTimeRange) => setLogPrefs({ timeRange: range }),
    [setLogPrefs]
  )

  const filterStartTime = useCallback((): Date | null => {
    if (timeRange === 'all') return null
    if (timeRange === 'custom') return customStartTime
    const config = TIME_RANGE_OPTIONS.find((opt) => opt.value === timeRange)
    return config?.getStartTime() || null
  }, [timeRange, customStartTime])

  const filteredLogs = useMemo(() => {
    const startTime = filterStartTime()
    return logs.filter((log) => {
      const text = hasAnsi(log.message) ? stripAnsi(log.message) : log.message
      const matchesSearch =
        !searchTerm || text.toLowerCase().includes(searchTerm.toLowerCase())
      const matchesContainer =
        selectedContainers.length === 0 || selectedContainers.includes(log.container)
      const matchesTimeRange =
        !startTime || new Date(log.timestamp) >= startTime
      const matchesLevel = !errorsOnly || getLogLevel(text) !== 'other'
      return matchesSearch && matchesContainer && matchesTimeRange && matchesLevel
    })
  }, [logs, searchTerm, selectedContainers, filterStartTime, errorsOnly])

  // Assign each container a color round-robin by first-appearance order.
  // uniqueContainers is a Set (insertion-ordered) that only grows, so a
  // container keeps its color for the whole session and the first 10 distinct
  // services are always visually different.
  const containerColors = useMemo(() => {
    const map = new Map<string, string>()
    let i = 0
    for (const name of uniqueContainers) {
      map.set(name, CONTAINER_COLORS[i % CONTAINER_COLORS.length])
      i++
    }
    return map
  }, [uniqueContainers])

  const handleLogMessage = useCallback((data: LogMessage) => {
    batchRef.current.push(data)

    if (!flushTimeoutRef.current) {
      flushTimeoutRef.current = setTimeout(() => {
        // Snapshot and reset the buffer up front. React invokes these state
        // updaters lazily (at render), so referencing the mutable batchRef
        // inside them would race with the reset and could observe an emptied
        // buffer — which silently dropped every container from uniqueContainers.
        const batch = batchRef.current
        batchRef.current = []
        flushTimeoutRef.current = null

        setLogs((prevLogs) => {
          const newLogs = [...prevLogs, ...batch]
          if (newLogs.length > MAX_LOG_BUFFER) {
            return newLogs.slice(-MAX_LOG_BUFFER)
          }
          return newLogs
        })

        setUniqueContainers((prev) => {
          let changed = false
          const newSet = new Set(prev)
          for (const log of batch) {
            if (!newSet.has(log.container)) {
              newSet.add(log.container)
              changed = true
            }
          }
          return changed ? newSet : prev
        })
      }, 50)
    }
  }, [])

  const { status, send, reconnect, reconnectAttempts } = useWebSocketJSON<LogMessage>(
    `/ws/logs/${stackId}`,
    handleLogMessage,
    {
      skip: !hasRunningContainers,
      onReconnecting: (attempt) => {
        console.log(`Reconnecting... attempt ${attempt}`)
      },
    }
  )

  useEffect(() => {
    if (initialContainer) {
      send(JSON.stringify({ type: 'filter', containers: [initialContainer] }))
    }
  }, [initialContainer, send])

  const handleScroll = useCallback(() => {
    if (!logContainerRef.current || isAutoScrollingRef.current) return

    const { scrollTop, scrollHeight, clientHeight } = logContainerRef.current
    const atBottom = scrollHeight - scrollTop <= clientHeight + 10
    setScrolledUp(!atBottom)
  }, [])

  const scrollToBottom = useCallback(() => {
    if (!logContainerRef.current) return
    isAutoScrollingRef.current = true
    logContainerRef.current.scrollTop = logContainerRef.current.scrollHeight
    requestAnimationFrame(() => {
      isAutoScrollingRef.current = false
    })
  }, [])

  useEffect(() => {
    if (autoScroll && !scrolledUp) {
      scrollToBottom()
    }
  }, [logs, autoScroll, scrolledUp, scrollToBottom])

  // Count lines that arrived while the user is scrolled up, so the jump pill can
  // show how far behind they are. Reset to zero whenever we're back at bottom.
  useEffect(() => {
    const len = filteredLogs.length
    if (scrolledUp) {
      if (len > prevLenRef.current) {
        setNewCount((c) => c + (len - prevLenRef.current))
      }
    } else if (newCount !== 0) {
      setNewCount(0)
    }
    prevLenRef.current = len
  }, [filteredLogs, scrolledUp, newCount])

  useEffect(() => {
    const container = logContainerRef.current
    if (!container) return

    container.addEventListener('scroll', handleScroll, { passive: true })
    return () => {
      container.removeEventListener('scroll', handleScroll)
    }
  }, [handleScroll])

  // "/" focuses the log search box (skipped while typing in another field).
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === '/' && !isEditableTarget(e.target)) {
        e.preventDefault()
        searchInputRef.current?.focus()
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  const handleClear = useCallback(() => {
    setLogs([])
    logsRef.current = []
    batchRef.current = []
  }, [])

  const handleDownload = useCallback(() => {
    const content = filteredLogs
      .map(
        (log) =>
          `${showTimestamps ? `[${log.timestamp}] ` : ''}[${log.container}] ${stripAnsi(log.message)}`
      )
      .join('\n')

    const blob = new Blob([content], { type: 'text/plain' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `${stackId}-logs.log`
    document.body.appendChild(a)
    a.click()
    document.body.removeChild(a)
    URL.revokeObjectURL(url)
  }, [filteredLogs, showTimestamps, stackId])

  const toggleContainer = useCallback(
    (name: string) => {
      setSelectedContainers((prev) => {
        const next = prev.includes(name)
          ? prev.filter((c) => c !== name)
          : [...prev, name]
        send(JSON.stringify({ type: 'filter', containers: next }))
        return next
      })
    },
    [send]
  )

  const clearContainerFilter = useCallback(() => {
    setSelectedContainers([])
    send(JSON.stringify({ type: 'filter', containers: [] }))
  }, [send])

  const handleReconnect = useCallback(() => {
    reconnect()
  }, [reconnect])

  const handleJumpToLatest = useCallback(() => {
    setScrolledUp(false)
    setNewCount(0)
    scrollToBottom()
  }, [scrollToBottom])

  const isDisconnected = status === 'disconnected' || status === 'reconnecting'

  const handleTimeRangeChange = useCallback(
    (value: string) => {
      const range = value as LogTimeRange
      setTimeRange(range)
      if (range !== 'custom') {
        setCustomStartTime(null)
        setCustomEndTime(null)
      }
    },
    [setTimeRange]
  )

  const handleClearTimeRange = useCallback(() => {
    setTimeRange('all')
    setCustomStartTime(null)
    setCustomEndTime(null)
  }, [setTimeRange])

  const containerFilterLabel =
    selectedContainers.length === 0
      ? 'All containers'
      : selectedContainers.length === 1
        ? selectedContainers[0]
        : `${selectedContainers.length} selected`

  return (
    <div className="flex h-full flex-col gap-2">
      <div className="flex flex-wrap items-center gap-2">
        <Button
          variant={autoScroll ? 'default' : 'outline'}
          size="sm"
          onClick={() => setLogPrefs({ autoScroll: !autoScroll })}
          title={autoScroll ? 'Auto-scroll enabled' : 'Auto-scroll disabled'}
        >
          <ArrowDown className="h-4 w-4" />
        </Button>

        <div className="relative">
          <Search className="absolute left-2 top-2.5 h-4 w-4 text-muted-foreground" />
          <Input
            ref={searchInputRef}
            placeholder="Search logs... ( / )"
            value={searchTerm}
            onChange={(e) => setSearchTerm(e.target.value)}
            className="w-full sm:w-64 pl-8"
          />
        </div>

        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button variant="outline" size="sm" className="w-48 justify-start font-normal">
              <Filter className="mr-2 h-4 w-4 shrink-0" />
              <span className="truncate">{containerFilterLabel}</span>
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="start" className="w-56">
            <DropdownMenuLabel>Filter containers</DropdownMenuLabel>
            <DropdownMenuItem onClick={clearContainerFilter}>
              All containers
            </DropdownMenuItem>
            {uniqueContainers.size > 0 && <DropdownMenuSeparator />}
            {Array.from(uniqueContainers).map((container) => (
              <DropdownMenuCheckboxItem
                key={container}
                checked={selectedContainers.includes(container)}
                onCheckedChange={() => toggleContainer(container)}
                onSelect={(e) => e.preventDefault()}
              >
                {container}
              </DropdownMenuCheckboxItem>
            ))}
          </DropdownMenuContent>
        </DropdownMenu>

        <Select value={timeRange} onValueChange={handleTimeRangeChange}>
          <SelectTrigger className="w-40">
            <Clock className="mr-2 h-4 w-4" />
            <SelectValue placeholder="All time" />
          </SelectTrigger>
          <SelectContent>
            {TIME_RANGE_OPTIONS.map((option) => (
              <SelectItem key={option.value} value={option.value}>
                {option.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>

        {timeRange !== 'all' && (
          <Button variant="outline" size="sm" onClick={handleClearTimeRange} title="Clear time filter">
            <X className="h-4 w-4" />
          </Button>
        )}

        <Button
          variant={errorsOnly ? 'default' : 'outline'}
          size="sm"
          onClick={() => setLogPrefs({ errorsOnly: !errorsOnly })}
          title={errorsOnly ? 'Showing errors & warnings only' : 'Show errors & warnings only'}
        >
          <AlertTriangle className="h-4 w-4" />
        </Button>

        <Button
          variant={showTimestamps ? 'default' : 'outline'}
          size="sm"
          onClick={() => setLogPrefs({ showTimestamps: !showTimestamps })}
          title={showTimestamps ? 'Timestamps shown' : 'Timestamps hidden'}
        >
          <Clock className="h-4 w-4" />
        </Button>

        <Button
          variant={wrap ? 'default' : 'outline'}
          size="sm"
          onClick={() => setLogPrefs({ wrap: !wrap })}
          title={wrap ? 'Wrapping long lines' : 'Lines not wrapped'}
        >
          <WrapText className="h-4 w-4" />
        </Button>

        <Button variant="outline" size="sm" onClick={handleClear} title="Clear logs">
          <Trash2 className="h-4 w-4" />
        </Button>

        <Button variant="outline" size="sm" onClick={handleDownload} title="Download logs">
          <Download className="h-4 w-4" />
        </Button>
      </div>

      {timeRange === 'custom' && (
        <div className="flex items-center gap-2 rounded-lg border bg-muted/50 p-2">
          <Calendar className="h-4 w-4 text-muted-foreground" />
          <Input
            type="datetime-local"
            className="w-auto"
            value={customStartTime ? formatDateTimeLocal(customStartTime) : ''}
            onChange={(e) => setCustomStartTime(e.target.value ? new Date(e.target.value) : null)}
          />
          <span className="text-sm text-muted-foreground">to</span>
          <Input
            type="datetime-local"
            className="w-auto"
            value={customEndTime ? formatDateTimeLocal(customEndTime) : ''}
            onChange={(e) => setCustomEndTime(e.target.value ? new Date(e.target.value) : null)}
          />
        </div>
      )}

      {isDisconnected && hasRunningContainers && (
        <div className="flex items-center justify-between rounded-lg border border-warning/40 bg-warning/10 px-4 py-2 text-warning">
          <div className="flex items-center gap-2">
            <AlertCircle className="h-4 w-4" />
            <span>
              {status === 'reconnecting'
                ? `Connection lost. Reconnecting... (attempt ${reconnectAttempts})`
                : 'Disconnected'}
            </span>
          </div>
          <Button variant="outline" size="sm" onClick={handleReconnect}>
            <RotateCcw className="mr-2 h-4 w-4" />
            Reconnect
          </Button>
        </div>
      )}

      <div className="relative flex-1 min-h-0">
        <div
          ref={logContainerRef}
          className="h-full overflow-auto rounded-lg border bg-muted/50 p-4 font-mono text-sm"
        >
          {filteredLogs.length === 0 ? (
            <div className="flex h-full items-center justify-center text-muted-foreground">
              {!hasRunningContainers ? 'No containers are running. Start the stack to view logs.' :
               logs.length === 0 ? 'Waiting for logs...' :
               errorsOnly ? 'No errors or warnings match current filters' :
               timeRange !== 'all' ? 'No logs in selected time range' :
               'No logs match current filters'}
            </div>
          ) : (
            filteredLogs.map((log, index) => {
              const containerColor = containerColors.get(log.container) ?? CONTAINER_COLORS[0]
              const logLevelColor = hasAnsi(log.message) ? '' : getLogLevelColor(log.message)

              return (
                <div
                  key={`${log.container}-${log.timestamp}-${index}`}
                  className={cn(
                    'flex gap-2 py-0.5',
                    wrap ? 'whitespace-pre-wrap wrap-break-word' : 'whitespace-pre'
                  )}
                  role="log"
                >
                  {showTimestamps && log.timestamp && (
                    <span className="text-muted-foreground shrink-0">
                      [{log.timestamp}]
                    </span>
                  )}
                  <span className={cn('font-medium shrink-0', containerColor)}>
                    [{log.container}]
                  </span>
                  <span className={cn('flex-1', logLevelColor)}>
                    {renderMessage(log.message, searchTerm)}
                  </span>
                </div>
              )
            })
          )}
        </div>

        {scrolledUp && (
          <button
            onClick={handleJumpToLatest}
            className="absolute bottom-4 left-1/2 -translate-x-1/2 flex items-center gap-1.5 rounded-full bg-primary px-3 py-1.5 text-xs font-medium text-primary-foreground shadow-md hover:bg-primary/90 transition-colors"
          >
            <ArrowDown className="h-3.5 w-3.5" />
            {newCount > 0
              ? `${newCount} new line${newCount === 1 ? '' : 's'}`
              : 'Jump to latest'}
          </button>
        )}
      </div>

      <div className="flex items-center justify-between text-xs text-muted-foreground">
        <span>
          Showing {filteredLogs.length} {filteredLogs.length === 1 ? 'log' : 'logs'}
          {logs.length !== filteredLogs.length && ` of ${logs.length} total`}
        </span>
        <span>Max buffer: {MAX_LOG_BUFFER} lines</span>
      </div>
    </div>
  )
}
