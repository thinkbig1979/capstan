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
  AlertTriangle,
  Calendar,
  WrapText,
  X,
} from 'lucide-react'
import { formatDateTimeLocal } from '@/lib/format'
import type { LogTimeRange } from '@/stores/uiStore'
import { TIME_RANGE_OPTIONS } from './constants'

interface LogToolbarProps {
  autoScroll: boolean
  onToggleAutoScroll: () => void

  searchInputRef: React.RefObject<HTMLInputElement | null>
  searchTerm: string
  onSearchChange: (value: string) => void

  uniqueContainers: Set<string>
  selectedContainers: string[]
  onToggleContainer: (name: string) => void
  onClearContainerFilter: () => void
  containerFilterLabel: string

  timeRange: LogTimeRange
  onTimeRangeChange: (value: string) => void
  onClearTimeRange: () => void
  customStartTime: Date | null
  customEndTime: Date | null
  onCustomStartChange: (date: Date | null) => void
  onCustomEndChange: (date: Date | null) => void

  errorsOnly: boolean
  onToggleErrorsOnly: () => void
  showTimestamps: boolean
  onToggleShowTimestamps: () => void
  wrap: boolean
  onToggleWrap: () => void

  onClear: () => void
  onDownload: () => void
}

export function LogToolbar({
  autoScroll,
  onToggleAutoScroll,
  searchInputRef,
  searchTerm,
  onSearchChange,
  uniqueContainers,
  selectedContainers,
  onToggleContainer,
  onClearContainerFilter,
  containerFilterLabel,
  timeRange,
  onTimeRangeChange,
  onClearTimeRange,
  customStartTime,
  customEndTime,
  onCustomStartChange,
  onCustomEndChange,
  errorsOnly,
  onToggleErrorsOnly,
  showTimestamps,
  onToggleShowTimestamps,
  wrap,
  onToggleWrap,
  onClear,
  onDownload,
}: LogToolbarProps) {
  return (
    <>
      <div className="flex flex-wrap items-center gap-2">
        <Button
          variant={autoScroll ? 'default' : 'outline'}
          size="sm"
          onClick={onToggleAutoScroll}
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
            onChange={(e) => onSearchChange(e.target.value)}
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
            <DropdownMenuItem onClick={onClearContainerFilter}>All containers</DropdownMenuItem>
            {uniqueContainers.size > 0 && <DropdownMenuSeparator />}
            {Array.from(uniqueContainers).map((container) => (
              <DropdownMenuCheckboxItem
                key={container}
                checked={selectedContainers.includes(container)}
                onCheckedChange={() => onToggleContainer(container)}
                onSelect={(e) => e.preventDefault()}
              >
                {container}
              </DropdownMenuCheckboxItem>
            ))}
          </DropdownMenuContent>
        </DropdownMenu>

        <Select value={timeRange} onValueChange={onTimeRangeChange}>
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
          <Button variant="outline" size="sm" onClick={onClearTimeRange} title="Clear time filter">
            <X className="h-4 w-4" />
          </Button>
        )}

        <Button
          variant={errorsOnly ? 'default' : 'outline'}
          size="sm"
          onClick={onToggleErrorsOnly}
          title={errorsOnly ? 'Showing errors & warnings only' : 'Show errors & warnings only'}
        >
          <AlertTriangle className="h-4 w-4" />
        </Button>

        <Button
          variant={showTimestamps ? 'default' : 'outline'}
          size="sm"
          onClick={onToggleShowTimestamps}
          title={showTimestamps ? 'Timestamps shown' : 'Timestamps hidden'}
        >
          <Clock className="h-4 w-4" />
        </Button>

        <Button
          variant={wrap ? 'default' : 'outline'}
          size="sm"
          onClick={onToggleWrap}
          title={wrap ? 'Wrapping long lines' : 'Lines not wrapped'}
        >
          <WrapText className="h-4 w-4" />
        </Button>

        <Button variant="outline" size="sm" onClick={onClear} title="Clear logs">
          <Trash2 className="h-4 w-4" />
        </Button>

        <Button variant="outline" size="sm" onClick={onDownload} title="Download logs">
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
            onChange={(e) => onCustomStartChange(e.target.value ? new Date(e.target.value) : null)}
          />
          <span className="text-sm text-muted-foreground">to</span>
          <Input
            type="datetime-local"
            className="w-auto"
            value={customEndTime ? formatDateTimeLocal(customEndTime) : ''}
            onChange={(e) => onCustomEndChange(e.target.value ? new Date(e.target.value) : null)}
          />
        </div>
      )}
    </>
  )
}
