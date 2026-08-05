import { useState, useRef, useEffect, useCallback, useMemo } from 'react'
import { useWebSocketJSON } from '@/hooks/useWebSocket'
import { stripAnsi, hasAnsi } from '@/lib/ansi'
import { useUIStore, type LogTimeRange } from '@/stores/uiStore'
import { getLogLevel, isEditableTarget } from './log-utils'
import { MAX_LOG_BUFFER, CONTAINER_COLORS, TIME_RANGE_OPTIONS } from './constants'
import type { LogMessage, DisplayLogMessage } from './types'

interface UseLogStreamParams {
  stackId: string
  initialContainer?: string
  hasRunningContainers: boolean
}

// All log-viewer state, refs, effects, and derived values live here, called
// once from the LogViewer composition root — see the quirks called out
// inline below, which a refactor must not touch.
export function useLogStream({ stackId, initialContainer, hasRunningContainers }: UseLogStreamParams) {
  const logPrefs = useUIStore((s) => s.logPrefs)
  const setLogPrefs = useUIStore((s) => s.setLogPrefs)
  const { showTimestamps, autoScroll, wrap, errorsOnly, timeRange } = logPrefs

  const [logs, setLogs] = useState<DisplayLogMessage[]>([])
  const [searchTerm, setSearchTerm] = useState('')
  const [selectedContainers, setSelectedContainers] = useState<string[]>(
    initialContainer ? [initialContainer] : []
  )
  const [uniqueContainers, setUniqueContainers] = useState<Set<string>>(new Set())
  const [scrolledUp, setScrolledUp] = useState(false)
  const [newCount, setNewCount] = useState(0)
  const [customStartTime, setCustomStartTime] = useState<Date | null>(null)
  const [customEndTime, setCustomEndTime] = useState<Date | null>(null)

  const logsRef = useRef<DisplayLogMessage[]>([])
  const batchRef = useRef<DisplayLogMessage[]>([])
  const nextLogIdRef = useRef(0)
  const flushTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const logContainerRef = useRef<HTMLDivElement>(null)
  const isAutoScrollingRef = useRef(false)
  const searchInputRef = useRef<HTMLInputElement>(null)

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
    batchRef.current.push({ ...data, id: nextLogIdRef.current++ })

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
  // Adjusted during render (rather than in an effect) by comparing against the
  // previous render's length/scrolledUp — see
  // https://react.dev/learn/you-might-not-need-an-effect.
  const [prevLogTracker, setPrevLogTracker] = useState({ len: 0, scrolledUp: false })
  if (filteredLogs.length !== prevLogTracker.len || scrolledUp !== prevLogTracker.scrolledUp) {
    const len = filteredLogs.length
    const prevLen = prevLogTracker.len
    setPrevLogTracker({ len, scrolledUp })
    if (scrolledUp) {
      if (len > prevLen) {
        setNewCount((c) => c + (len - prevLen))
      }
    } else {
      setNewCount(0)
    }
  }

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
      const next = selectedContainers.includes(name)
        ? selectedContainers.filter((c) => c !== name)
        : [...selectedContainers, name]
      setSelectedContainers(next)
      send(JSON.stringify({ type: 'filter', containers: next }))
    },
    [selectedContainers, send]
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

  // Thin wrappers around setLogPrefs so presentational children stay
  // decoupled from the uiStore's preference-patch shape.
  const toggleAutoScroll = useCallback(() => setLogPrefs({ autoScroll: !autoScroll }), [setLogPrefs, autoScroll])
  const toggleErrorsOnly = useCallback(() => setLogPrefs({ errorsOnly: !errorsOnly }), [setLogPrefs, errorsOnly])
  const toggleShowTimestamps = useCallback(
    () => setLogPrefs({ showTimestamps: !showTimestamps }),
    [setLogPrefs, showTimestamps]
  )
  const toggleWrap = useCallback(() => setLogPrefs({ wrap: !wrap }), [setLogPrefs, wrap])

  return {
    showTimestamps,
    autoScroll,
    wrap,
    errorsOnly,
    timeRange,
    logs,
    filteredLogs,
    uniqueContainers,
    containerColors,
    searchTerm,
    setSearchTerm,
    searchInputRef,
    selectedContainers,
    toggleContainer,
    clearContainerFilter,
    containerFilterLabel,
    customStartTime,
    customEndTime,
    setCustomStartTime,
    setCustomEndTime,
    handleTimeRangeChange,
    handleClearTimeRange,
    toggleAutoScroll,
    toggleErrorsOnly,
    toggleShowTimestamps,
    toggleWrap,
    handleClear,
    handleDownload,
    status,
    isDisconnected,
    reconnectAttempts,
    handleReconnect,
    logContainerRef,
    scrolledUp,
    newCount,
    handleJumpToLatest,
  }
}
