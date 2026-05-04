import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Button } from '@/components/ui/button'
import { Clock, Copy, Clipboard, Unplug, Plus, Minus, Search, RotateCcw, Terminal } from 'lucide-react'

interface ContainerOption {
  id: string
  name: string
}

interface TerminalToolbarProps {
  selectedContainer: string
  runningContainers: ContainerOption[]
  onContainerChange: (value: string) => void
  isConnected: boolean
  isConnecting: boolean
  sessionDuration: number
  disconnectCountdown: number | null
  fontSize: number
  minFontSize: number
  maxFontSize: number
  hasSelection: boolean
  showSearch: boolean
  formatDuration: (seconds: number) => string
  onFontSizeChange: (delta: number) => void
  onCopy: () => void
  onPaste: () => void
  onToggleSearch: () => void
  onDisconnect: () => void
  onReconnect: () => void
  onClose: () => void
}

export function TerminalToolbar({
  selectedContainer,
  runningContainers,
  onContainerChange,
  isConnected,
  isConnecting,
  sessionDuration,
  disconnectCountdown,
  fontSize,
  minFontSize,
  maxFontSize,
  hasSelection,
  showSearch,
  formatDuration,
  onFontSizeChange,
  onCopy,
  onPaste,
  onToggleSearch,
  onDisconnect,
  onReconnect,
  onClose,
}: TerminalToolbarProps) {
  return (
    <div className="flex items-center justify-between">
      <div className="flex items-center space-x-4">
        <Terminal className="h-5 w-5 text-muted-foreground" />
        <Select value={selectedContainer} onValueChange={onContainerChange}>
          <SelectTrigger className="w-[300px]">
            <SelectValue placeholder="Select container">
              {selectedContainer
                ? runningContainers.find(c => c.id === selectedContainer)?.name || 'Unknown'
                : 'Select container'}
            </SelectValue>
          </SelectTrigger>
          <SelectContent>
            {runningContainers.length === 0 ? (
              <div className="px-2 py-2 text-sm text-muted-foreground">
                No running containers
              </div>
            ) : (
              runningContainers.map(container => (
                <SelectItem key={container.id} value={container.id}>
                  {container.name}
                </SelectItem>
              ))
            )}
          </SelectContent>
        </Select>
      </div>
      <div className="flex items-center space-x-2">
        {isConnected && (
          <>
            {disconnectCountdown !== null ? (
              <span className="flex items-center text-sm text-red-500 font-medium">
                <Clock className="mr-1.5 h-4 w-4" />
                Disconnecting in {disconnectCountdown} seconds
              </span>
            ) : (
              <span className="flex items-center text-sm text-muted-foreground">
                <Clock className="mr-1.5 h-4 w-4" />
                Active for {formatDuration(sessionDuration)}
              </span>
            )}
            <span className="flex items-center text-sm text-muted-foreground">
              <span className="mr-1.5 h-2 w-2 rounded-full bg-green-500" />
              Connected
            </span>
            <Button variant="ghost" size="sm" onClick={onCopy} disabled={!hasSelection} title="Copy (Ctrl+Shift+C)">
              <Copy className="h-4 w-4" />
            </Button>
            <Button variant="ghost" size="sm" onClick={onPaste} title="Paste (Ctrl+Shift+V)">
              <Clipboard className="h-4 w-4" />
            </Button>
            <div className="flex items-center gap-0.5 border-l pl-2 ml-1">
              <Button variant="ghost" size="sm" onClick={() => onFontSizeChange(-1)} title="Decrease font size" disabled={fontSize <= minFontSize}>
                <Minus className="h-3.5 w-3.5" />
              </Button>
              <span className="w-8 text-center text-xs text-muted-foreground tabular-nums" title="Font size">{fontSize}</span>
              <Button variant="ghost" size="sm" onClick={() => onFontSizeChange(1)} title="Increase font size" disabled={fontSize >= maxFontSize}>
                <Plus className="h-3.5 w-3.5" />
              </Button>
            </div>
            <Button variant="ghost" size="sm" onClick={onToggleSearch} title="Find (Ctrl+Shift+F)" className={showSearch ? 'bg-accent' : ''}>
              <Search className="h-4 w-4" />
            </Button>
            <Button variant="ghost" size="sm" onClick={onDisconnect} title="Disconnect terminal" className="text-muted-foreground hover:text-foreground">
              <Unplug className="h-4 w-4" />
            </Button>
          </>
        )}
        {isConnecting && (
          <span className="flex items-center text-sm text-muted-foreground">
            <span className="mr-1.5 h-2 w-2 animate-pulse rounded-full bg-yellow-500" />
            Connecting...
          </span>
        )}
        {!isConnected && !isConnecting && selectedContainer && (
          <div className="flex items-center gap-2">
            <Button variant="outline" size="sm" onClick={onReconnect}>
              <RotateCcw className="mr-2 h-4 w-4" />
              Reconnect
            </Button>
            <Button variant="ghost" size="sm" onClick={onClose} className="text-muted-foreground">
              Close
            </Button>
          </div>
        )}
      </div>
    </div>
  )
}
