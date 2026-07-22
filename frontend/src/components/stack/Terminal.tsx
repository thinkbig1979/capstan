import { Button } from '@/components/ui/button'
import { TerminalToolbar } from '@/components/stack/TerminalToolbar'
import { TerminalSearchBar } from '@/components/stack/TerminalSearchBar'
import { EmptyState } from '@/components/EmptyState'
import { TerminalSquare } from 'lucide-react'
import { useTerminalSession } from './terminal/useTerminalSession'
import { MAX_FONT_SIZE, MIN_FONT_SIZE } from './terminal/constants'
import type { TerminalProps } from './terminal/types'

export function TerminalComponent({ stack, initialContainer }: TerminalProps) {
  const {
    selectedContainer,
    isConnected,
    isConnecting,
    sessionDuration,
    disconnectCountdown,
    hasSelection,
    fontSize,
    showHints,
    showSearch,
    searchAddonInstance,
    terminalRef,
    runningContainers,
    hasRunningContainers,
    formatDuration,
    handleContainerChange,
    handleFontSizeChange,
    handleCopy,
    handlePaste,
    toggleSearch,
    handleDisconnect,
    handleReconnect,
    handleClose,
    handleCloseSearch,
    dismissHints,
    handleContextMenu,
  } = useTerminalSession({ stack, initialContainer })

  return (
    <div className="flex h-full flex-col space-y-4">
      {hasRunningContainers && (
      <TerminalToolbar
        selectedContainer={selectedContainer}
        runningContainers={runningContainers}
        onContainerChange={handleContainerChange}
        isConnected={isConnected}
        isConnecting={isConnecting}
        sessionDuration={sessionDuration}
        disconnectCountdown={disconnectCountdown}
        fontSize={fontSize}
        minFontSize={MIN_FONT_SIZE}
        maxFontSize={MAX_FONT_SIZE}
        hasSelection={hasSelection}
        showSearch={showSearch}
        formatDuration={formatDuration}
        onFontSizeChange={handleFontSizeChange}
        onCopy={handleCopy}
        onPaste={handlePaste}
        onToggleSearch={toggleSearch}
        onDisconnect={handleDisconnect}
        onReconnect={handleReconnect}
        onClose={handleClose}
      />
      )}
      {showSearch && isConnected && (
        <TerminalSearchBar searchAddon={searchAddonInstance} onClose={handleCloseSearch} />
      )}
      {!hasRunningContainers && (
        <EmptyState
          icon={<TerminalSquare className="h-12 w-12 text-muted-foreground" />}
          title="No running containers"
          description="The terminal opens a shell inside a running container. Start the stack to use it."
        />
      )}
      <div className={`rounded-lg border bg-terminal-background p-2 ${hasRunningContainers ? '' : 'hidden'}`}>
        <div
          ref={terminalRef}
          className="overflow-hidden"
          style={{ minHeight: '400px' }}
          onContextMenu={handleContextMenu}
        />
      </div>

      {showHints && isConnected && (
        <div className="rounded-lg border bg-muted/50 p-4 text-sm">
          <div className="mb-2 font-medium">Keyboard Shortcuts</div>
          <div className="space-y-1 text-muted-foreground">
            <div><kbd className="rounded border bg-background px-1.5 py-0.5">Ctrl</kbd> + <kbd className="rounded border bg-background px-1.5 py-0.5">Shift</kbd> + <kbd className="rounded border bg-background px-1.5 py-0.5">C</kbd> - Copy selection</div>
            <div><kbd className="rounded border bg-background px-1.5 py-0.5">Ctrl</kbd> + <kbd className="rounded border bg-background px-1.5 py-0.5">Shift</kbd> + <kbd className="rounded border bg-background px-1.5 py-0.5">V</kbd> - Paste from clipboard</div>
            <div><kbd className="rounded border bg-background px-1.5 py-0.5">Ctrl</kbd> + <kbd className="rounded border bg-background px-1.5 py-0.5">Shift</kbd> + <kbd className="rounded border bg-background px-1.5 py-0.5">F</kbd> - Find in terminal</div>
          </div>
          <Button variant="link" size="sm" onClick={dismissHints} className="mt-2 p-0">
            Don't show again
          </Button>
        </div>
      )}
    </div>
  )
}
