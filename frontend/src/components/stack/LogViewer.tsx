import { useLogStream } from './logviewer/useLogStream'
import { LogToolbar } from './logviewer/LogToolbar'
import { DisconnectedBanner } from './logviewer/DisconnectedBanner'
import { LogList } from './logviewer/LogList'
import { StatusFooter } from './logviewer/StatusFooter'
import type { LogViewerProps } from './logviewer/types'

export function LogViewer({ stackId, initialContainer, hasRunningContainers = true }: LogViewerProps) {
  const {
    autoScroll,
    toggleAutoScroll,
    searchInputRef,
    searchTerm,
    setSearchTerm,
    uniqueContainers,
    selectedContainers,
    toggleContainer,
    clearContainerFilter,
    containerFilterLabel,
    timeRange,
    handleTimeRangeChange,
    handleClearTimeRange,
    customStartTime,
    customEndTime,
    setCustomStartTime,
    setCustomEndTime,
    errorsOnly,
    toggleErrorsOnly,
    showTimestamps,
    toggleShowTimestamps,
    wrap,
    toggleWrap,
    handleClear,
    handleDownload,
    isDisconnected,
    status,
    reconnectAttempts,
    handleReconnect,
    logContainerRef,
    filteredLogs,
    logs,
    containerColors,
    scrolledUp,
    newCount,
    handleJumpToLatest,
  } = useLogStream({ stackId, initialContainer, hasRunningContainers })

  return (
    <div className="flex h-full flex-col gap-2">
      <LogToolbar
        autoScroll={autoScroll}
        onToggleAutoScroll={toggleAutoScroll}
        searchInputRef={searchInputRef}
        searchTerm={searchTerm}
        onSearchChange={setSearchTerm}
        uniqueContainers={uniqueContainers}
        selectedContainers={selectedContainers}
        onToggleContainer={toggleContainer}
        onClearContainerFilter={clearContainerFilter}
        containerFilterLabel={containerFilterLabel}
        timeRange={timeRange}
        onTimeRangeChange={handleTimeRangeChange}
        onClearTimeRange={handleClearTimeRange}
        customStartTime={customStartTime}
        customEndTime={customEndTime}
        onCustomStartChange={setCustomStartTime}
        onCustomEndChange={setCustomEndTime}
        errorsOnly={errorsOnly}
        onToggleErrorsOnly={toggleErrorsOnly}
        showTimestamps={showTimestamps}
        onToggleShowTimestamps={toggleShowTimestamps}
        wrap={wrap}
        onToggleWrap={toggleWrap}
        onClear={handleClear}
        onDownload={handleDownload}
      />

      <DisconnectedBanner
        show={isDisconnected && hasRunningContainers}
        status={status}
        reconnectAttempts={reconnectAttempts}
        onReconnect={handleReconnect}
      />

      <LogList
        ref={logContainerRef}
        filteredLogs={filteredLogs}
        totalLogsCount={logs.length}
        hasRunningContainers={hasRunningContainers}
        errorsOnly={errorsOnly}
        timeRange={timeRange}
        wrap={wrap}
        showTimestamps={showTimestamps}
        searchTerm={searchTerm}
        containerColors={containerColors}
        scrolledUp={scrolledUp}
        newCount={newCount}
        onJumpToLatest={handleJumpToLatest}
      />

      <StatusFooter filteredCount={filteredLogs.length} totalCount={logs.length} />
    </div>
  )
}
