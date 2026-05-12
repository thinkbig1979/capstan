import { useState, useRef, useEffect, useCallback, useMemo } from 'react'
import { useWebSocketJSON } from '@/hooks/useWebSocket'
import { formatDateTimeLocal } from '@/lib/format'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { 
  ArrowDown, 
  Search, 
  Filter, 
  Clock, 
  Trash2, 
  Download, 
  RotateCcw,
  AlertCircle,
  Calendar,
  X
} from 'lucide-react'
import { cn } from '@/lib/utils'

interface LogMessage {
  container: string
  timestamp: string
  message: string
}

type TimeRange = 'all' | '5m' | '15m' | '1h' | 'custom'

interface TimeRangeConfig {
  label: string
  value: TimeRange
  getStartTime: () => Date | null
}

interface LogViewerProps {
  stackId: string
  initialContainer?: string
  hasRunningContainers?: boolean
}

const MAX_LOG_BUFFER = 10000
// Per-container identifier hues. Not status — purely a visual diff between
// container names in interleaved log output. Kept as a fixed palette because
// they need to remain visually distinct, not adapt to theme semantics.
const CONTAINER_COLORS = [
  'text-red-500',
  'text-orange-500',
  'text-yellow-500',
  'text-green-500',
  'text-teal-500',
  'text-blue-500',
  'text-indigo-500',
  'text-purple-500',
  'text-pink-500',
  'text-rose-500',
]

function getContainerColor(containerName: string): string {
  let hash = 0
  for (let i = 0; i < containerName.length; i++) {
    hash = containerName.charCodeAt(i) + ((hash << 5) - hash)
  }
  return CONTAINER_COLORS[Math.abs(hash) % CONTAINER_COLORS.length]
}

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

function getLogLevelColor(message: string): string {
  const upperMsg = message.toUpperCase()
  if (upperMsg.includes('ERROR') || upperMsg.includes('FATAL') || upperMsg.includes('PANIC')) {
    return 'text-destructive'
  }
  if (upperMsg.includes('WARN') || upperMsg.includes('WARNING')) {
    return 'text-warning'
  }
  if (upperMsg.includes('DEBUG') || upperMsg.includes('TRACE')) {
    return 'text-muted-foreground'
  }
  return ''
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

export function LogViewer({ stackId, initialContainer, hasRunningContainers = true }: LogViewerProps) {
  const [logs, setLogs] = useState<LogMessage[]>([])
  const [autoScroll, setAutoScroll] = useState(true)
  const [searchTerm, setSearchTerm] = useState('')
  const [showTimestamps, setShowTimestamps] = useState(true)
  const [selectedContainers, setSelectedContainers] = useState<string[]>(
    initialContainer ? [initialContainer] : []
  )
  const [uniqueContainers, setUniqueContainers] = useState<Set<string>>(new Set())
  const [scrolledUp, setScrolledUp] = useState(false)
  const [timeRange, setTimeRange] = useState<TimeRange>('all')
  const [customStartTime, setCustomStartTime] = useState<Date | null>(null)
  const [customEndTime, setCustomEndTime] = useState<Date | null>(null)
  
  const logsRef = useRef<LogMessage[]>([])
  const batchRef = useRef<LogMessage[]>([])
  const flushTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const logContainerRef = useRef<HTMLDivElement>(null)
  const isAutoScrollingRef = useRef(false)

  const filterStartTime = useCallback((): Date | null => {
    if (timeRange === 'all') return null
    if (timeRange === 'custom') return customStartTime
    const config = TIME_RANGE_OPTIONS.find((opt) => opt.value === timeRange)
    return config?.getStartTime() || null
  }, [timeRange, customStartTime])

  const filteredLogs = useMemo(() => {
    const startTime = filterStartTime()
    return logs.filter((log) => {
      const matchesSearch =
        !searchTerm || log.message.toLowerCase().includes(searchTerm.toLowerCase())
      const matchesContainer =
        selectedContainers.length === 0 || selectedContainers.includes(log.container)
      const matchesTimeRange =
        !startTime || new Date(log.timestamp) >= startTime
      return matchesSearch && matchesContainer && matchesTimeRange
    })
  }, [logs, searchTerm, selectedContainers, filterStartTime])

  const handleLogMessage = useCallback((data: LogMessage) => {
    batchRef.current.push(data)
    
    if (!flushTimeoutRef.current) {
      flushTimeoutRef.current = setTimeout(() => {
        setLogs((prevLogs) => {
          const newLogs = [...prevLogs, ...batchRef.current]
          if (newLogs.length > MAX_LOG_BUFFER) {
            return newLogs.slice(-MAX_LOG_BUFFER)
          }
          return newLogs
        })
        
        setUniqueContainers((prev) => {
          const newSet = new Set(prev)
          batchRef.current.forEach((log) => newSet.add(log.container))
          return newSet
        })
        
        batchRef.current = []
        flushTimeoutRef.current = null
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

  useEffect(() => {
    const container = logContainerRef.current
    if (!container) return

    container.addEventListener('scroll', handleScroll, { passive: true })
    return () => {
      container.removeEventListener('scroll', handleScroll)
    }
  }, [handleScroll])

  const handleClear = useCallback(() => {
    setLogs([])
    logsRef.current = []
    batchRef.current = []
  }, [])

  const handleDownload = useCallback(() => {
    const content = filteredLogs
      .map(
        (log) =>
          `${showTimestamps ? `[${log.timestamp}] ` : ''}[${log.container}] ${log.message}`
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

  const handleContainerFilterChange = useCallback(
    (value: string) => {
      if (value === 'all') {
        setSelectedContainers([])
        send(JSON.stringify({ type: 'filter', containers: [] }))
      } else {
        setSelectedContainers([value])
        send(JSON.stringify({ type: 'filter', containers: [value] }))
      }
    },
    [send]
  )

  const handleReconnect = useCallback(() => {
    reconnect()
  }, [reconnect])

  const isDisconnected = status === 'disconnected' || status === 'reconnecting'

  const handleTimeRangeChange = useCallback((value: string) => {
    const range = value as TimeRange
    setTimeRange(range)
    if (range !== 'custom') {
      setCustomStartTime(null)
      setCustomEndTime(null)
    }
  }, [])

  const handleClearTimeRange = useCallback(() => {
    setTimeRange('all')
    setCustomStartTime(null)
    setCustomEndTime(null)
  }, [])

  return (
    <div className="flex h-full flex-col gap-2">
      <div className="flex flex-wrap items-center gap-2">
        <Button
          variant={autoScroll ? 'default' : 'outline'}
          size="sm"
          onClick={() => setAutoScroll(!autoScroll)}
          title={autoScroll ? 'Auto-scroll enabled' : 'Auto-scroll disabled'}
        >
          <ArrowDown className="h-4 w-4" />
        </Button>

        <div className="relative">
          <Search className="absolute left-2 top-2.5 h-4 w-4 text-muted-foreground" />
            <Input
              placeholder="Search logs..."
              value={searchTerm}
              onChange={(e) => setSearchTerm(e.target.value)}
              className="w-full sm:w-64 pl-8"
            />
        </div>

        <Select value={selectedContainers[0] || 'all'} onValueChange={handleContainerFilterChange}>
          <SelectTrigger className="w-48">
            <Filter className="mr-2 h-4 w-4" />
            <SelectValue placeholder="All containers" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">All containers</SelectItem>
            {Array.from(uniqueContainers).map((container) => (
              <SelectItem key={container} value={container}>
                {container}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>

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
          variant={showTimestamps ? 'default' : 'outline'}
          size="sm"
          onClick={() => setShowTimestamps(!showTimestamps)}
          title={showTimestamps ? 'Timestamps shown' : 'Timestamps hidden'}
        >
          <Clock className="h-4 w-4" />
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

      <div
        ref={logContainerRef}
        className="flex-1 overflow-auto rounded-lg border bg-muted/50 p-4 font-mono text-sm"
      >
        {filteredLogs.length === 0 ? (
          <div className="flex h-full items-center justify-center text-muted-foreground">
            {!hasRunningContainers ? 'No containers are running. Start the stack to view logs.' :
             logs.length === 0 ? 'Waiting for logs...' : 
             timeRange !== 'all' ? 'No logs in selected time range' :
             'No logs match current filters'}
          </div>
        ) : (
          filteredLogs.map((log, index) => {
            const containerColor = getContainerColor(log.container)
            const logLevelColor = getLogLevelColor(log.message)

            return (
              <div
                key={`${log.container}-${log.timestamp}-${index}`}
                className="flex gap-2 whitespace-pre-wrap wrap-break-word py-0.5"
                role="log"
              >
                {showTimestamps && log.timestamp && (
                  <span className="text-muted-foreground">
                    [{log.timestamp}] 
                  </span>
                )}
                <span className={cn('text-muted-foreground', containerColor)}>
                  [{log.container}]
                </span>
                <span className={cn('flex-1', logLevelColor)}>
                  {highlightSearchTerm(log.message, searchTerm)}
                </span>
              </div>
            )
          })
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
