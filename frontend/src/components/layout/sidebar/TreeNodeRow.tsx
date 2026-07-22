import { ChevronDown, ChevronRight, FolderOpen } from 'lucide-react'
import { countTreeNodeStacks, type TreeNode } from '@/lib/stack-tree'
import { StackRow } from './StackRow'

interface TreeNodeRowProps {
  node: TreeNode
  depth: number
  isFirst?: boolean
  collapsedGroups: Set<string>
  toggleGroup: (dirPath: string) => void
  pinnedStacks: string[]
  onTogglePin: (id: string) => void
}

export function TreeNodeRow({
  node,
  depth,
  isFirst = false,
  collapsedGroups,
  toggleGroup,
  pinnedStacks,
  onTogglePin,
}: TreeNodeRowProps) {
  const isCollapsed = collapsedGroups.has(node.fullPath)
  const totalStacks = countTreeNodeStacks(node)
  const isTopLevel = depth === 0
  const indentPx = (2 + depth * 2) * 4

  return (
    <div className={isTopLevel && !isFirst ? 'border-t border-sidebar-border mt-1' : ''}>
      <button
        type="button"
        onClick={() => toggleGroup(node.fullPath)}
        className={`flex items-center gap-1.5 w-full transition-colors rounded ${
          isTopLevel
            ? 'py-1 text-[11px] font-semibold text-sidebar-accent-foreground bg-sidebar-accent/50 hover:bg-sidebar-accent/80'
            : 'py-0.5 text-[10px] font-medium text-muted-foreground hover:text-sidebar-foreground'
        }`}
        style={{ paddingLeft: `${indentPx}px`, paddingRight: '8px' }}
        title={node.fullPath}
      >
        {isCollapsed ? (
          <ChevronRight className={isTopLevel ? 'h-3 w-3 shrink-0' : 'h-2.5 w-2.5 shrink-0'} />
        ) : (
          <ChevronDown className={isTopLevel ? 'h-3 w-3 shrink-0' : 'h-2.5 w-2.5 shrink-0'} />
        )}
        {isTopLevel && <FolderOpen className="h-3.5 w-3.5 shrink-0" />}
        <span className="truncate flex-1 text-left">{node.name}</span>
        <span className={`font-normal tabular-nums ${isTopLevel ? 'text-[10px]' : 'text-[9px]'}`}>
          {totalStacks}
        </span>
      </button>
      {!isCollapsed && (
        <>
          {node.stacks.map((stack) => (
            <StackRow
              key={stack.id}
              stack={stack}
              selecting={false}
              selected={false}
              onToggleSelect={() => {}}
              pinned={pinnedStacks.includes(stack.id)}
              onTogglePin={() => onTogglePin(stack.id)}
            />
          ))}
          {node.children.map((child, i) => (
            <TreeNodeRow
              key={child.fullPath}
              node={child}
              depth={depth + 1}
              isFirst={i === 0}
              collapsedGroups={collapsedGroups}
              toggleGroup={toggleGroup}
              pinnedStacks={pinnedStacks}
              onTogglePin={onTogglePin}
            />
          ))}
        </>
      )}
    </div>
  )
}
