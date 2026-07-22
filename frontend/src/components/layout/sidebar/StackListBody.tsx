import { ScrollArea } from '@/components/ui/scroll-area'
import { ChevronDown, ChevronRight, FolderOpen, Star } from 'lucide-react'
import { countTreeNodeStacks, type TreeNode } from '@/lib/stack-tree'
import type { Stack } from '@/types'
import { StackRow } from './StackRow'
import { TreeNodeRow } from './TreeNodeRow'

interface RootGroup {
  rootPath: string
  rootName: string
  nodes: TreeNode[]
}

interface StackListBodyProps {
  isLoading: boolean
  hasFilters: boolean
  filteredStacks: Stack[]
  pinnedVisible: Stack[]
  selecting: boolean
  selectedIds: Set<string>
  onToggleSelect: (id: string) => void
  pinnedStacks: string[]
  onTogglePin: (id: string) => void
  useGroups: boolean
  configuredDirs: { path: string; name: string }[]
  tree: TreeNode[]
  treeByRoot: RootGroup[]
  collapsedGroups: Set<string>
  toggleGroup: (dirPath: string) => void
}

export function StackListBody({
  isLoading,
  hasFilters,
  filteredStacks,
  pinnedVisible,
  selecting,
  selectedIds,
  onToggleSelect,
  pinnedStacks,
  onTogglePin,
  useGroups,
  configuredDirs,
  tree,
  treeByRoot,
  collapsedGroups,
  toggleGroup,
}: StackListBodyProps) {
  return (
    <ScrollArea className="flex-1">
      <div className="p-2 space-y-0.5">
        {!selecting && pinnedVisible.length > 0 && (
          <div className="mb-1">
            <div className="flex items-center gap-1 px-2 pt-1 pb-0.5 text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">
              <Star className="h-2.5 w-2.5 fill-current text-warning" />
              Pinned
            </div>
            {pinnedVisible.map((stack) => (
              <StackRow
                key={stack.id}
                stack={stack}
                selecting={selecting}
                selected={selectedIds.has(stack.id)}
                onToggleSelect={() => onToggleSelect(stack.id)}
                pinned={pinnedStacks.includes(stack.id)}
                onTogglePin={() => onTogglePin(stack.id)}
              />
            ))}
            <div className="h-px bg-sidebar-border my-1" />
          </div>
        )}
        {isLoading ? (
          <div className="px-2 py-4 text-sm text-muted-foreground">
            Loading...
          </div>
        ) : filteredStacks.length === 0 ? (
          <div className="px-2 py-4 text-sm text-muted-foreground">
            {hasFilters ? 'No stacks match filters' : 'No stacks found'}
          </div>
        ) : useGroups && !selecting ? (
          configuredDirs.length > 1 ? (
            treeByRoot.map((rootGroup) => {
              const isRootCollapsed = collapsedGroups.has(rootGroup.rootPath)
              const totalStacks = rootGroup.nodes.reduce(
                (sum, n) => sum + countTreeNodeStacks(n),
                0,
              )
              return (
                <div key={rootGroup.rootPath}>
                  <button
                    type="button"
                    onClick={() => toggleGroup(rootGroup.rootPath)}
                    className="flex items-center gap-1.5 w-full px-2 pt-2 pb-0.5 text-[10px] font-semibold text-muted-foreground uppercase tracking-wider hover:text-sidebar-foreground transition-colors"
                    title={rootGroup.rootPath}
                  >
                    {isRootCollapsed ? (
                      <ChevronRight className="h-3 w-3 shrink-0" />
                    ) : (
                      <ChevronDown className="h-3 w-3 shrink-0" />
                    )}
                    <FolderOpen className="h-3 w-3 shrink-0" />
                    <span className="truncate flex-1 text-left">
                      {rootGroup.rootName}
                    </span>
                    <span className="text-[9px] font-normal tabular-nums">
                      {totalStacks}
                    </span>
                  </button>
                  {!isRootCollapsed &&
                    rootGroup.nodes.map((node, i) => (
                      <TreeNodeRow
                        key={node.fullPath}
                        node={node}
                        depth={0}
                        isFirst={i === 0}
                        collapsedGroups={collapsedGroups}
                        toggleGroup={toggleGroup}
                        pinnedStacks={pinnedStacks}
                        onTogglePin={onTogglePin}
                      />
                    ))}
                </div>
              )
            })
          ) : (
            tree.map((node, i) => (
              <TreeNodeRow
                key={node.fullPath}
                node={node}
                depth={0}
                isFirst={i === 0}
                collapsedGroups={collapsedGroups}
                toggleGroup={toggleGroup}
                pinnedStacks={pinnedStacks}
                onTogglePin={onTogglePin}
              />
            ))
          )
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
