import { ScrollArea } from '@/components/ui/scroll-area'
import { LoadFailedNotice } from '@/components/LoadFailedNotice'
import { RefreshFailedNotice } from '@/components/RefreshFailedNotice'
import type { Stack } from '@/types'
import { StackRow } from './StackRow'

interface StackListBodyProps {
  isLoading: boolean
  /** The first load failed: there is no list, so no "No stacks found" either. */
  loadFailed: boolean
  /** A refetch failed over a list the server did send: keep it, say so. */
  refreshFailed: boolean
  onRetry: () => void
  hasFilters: boolean
  filteredStacks: Stack[]
  selecting: boolean
  selectedIds: Set<string>
  onToggleSelect: (id: string) => void
  pinnedStacks: string[]
  onTogglePin: (id: string) => void
}

export function StackListBody({
  isLoading,
  loadFailed,
  refreshFailed,
  onRetry,
  hasFilters,
  filteredStacks,
  selecting,
  selectedIds,
  onToggleSelect,
  pinnedStacks,
  onTogglePin,
}: StackListBodyProps) {
  return (
    <ScrollArea className="flex-1">
      <div className="p-2 space-y-0.5">
        {refreshFailed && (
          <RefreshFailedNotice what="the stack list" onRetry={onRetry} className="mb-1" />
        )}
        {isLoading ? (
          <div className="px-2 py-4 text-sm text-muted-foreground">
            Loading...
          </div>
        ) : loadFailed ? (
          <LoadFailedNotice what="the stack list" onRetry={onRetry} />
        ) : filteredStacks.length === 0 ? (
          <div className="px-2 py-4 text-sm text-muted-foreground">
            {hasFilters ? 'No stacks match filters' : 'No stacks found'}
          </div>
        ) : (
          filteredStacks.map((stack) => (
            <StackRow
              key={stack.id}
              stack={stack}
              selecting={selecting}
              selected={selectedIds.has(stack.id)}
              onToggleSelect={() => onToggleSelect(stack.id)}
              pinned={pinnedStacks.includes(stack.id)}
              onTogglePin={() => onTogglePin(stack.id)}
            />
          ))
        )}
      </div>
    </ScrollArea>
  )
}
