import type { BackupStatus, Stack, StackStatus } from '@/types'
import type { TreeNode } from '@/lib/stack-tree'
import type { BulkAction } from './constants'
import { SidebarHeader } from './SidebarHeader'
import { BulkActionBar } from './BulkActionBar'
import { FilterSummaryBar } from './FilterSummaryBar'
import { StackListBody } from './StackListBody'
import { BackupStatusFooter } from './BackupStatusFooter'

interface RootGroup {
  rootPath: string
  rootName: string
  nodes: TreeNode[]
}

interface SidebarBodyProps {
  stacks: Stack[]
  isLoading: boolean
  updateCount: number
  backupStatus?: BackupStatus
  selecting: boolean
  onToggleSelecting: () => void
  onCollapseSidebar: () => void
  searchQuery: string
  onSearchChange: (value: string) => void
  statusFilter: StackStatus | 'all'
  onStatusFilterChange: (status: StackStatus | 'all') => void
  hasFilters: boolean
  onClearFilters: () => void
  selectedIds: Set<string>
  bulkPending: boolean
  onSelectAllVisible: () => void
  onRunBulk: (action: BulkAction) => void
  onToggleSelect: (id: string) => void
  filteredStacks: Stack[]
  pinnedVisible: Stack[]
  pinnedStacks: string[]
  onTogglePin: (id: string) => void
  useGroups: boolean
  configuredDirs: { path: string; name: string }[]
  tree: TreeNode[]
  treeByRoot: RootGroup[]
  collapsedGroups: Set<string>
  toggleGroup: (dirPath: string) => void
}

export function SidebarBody({
  stacks,
  isLoading,
  updateCount,
  backupStatus,
  selecting,
  onToggleSelecting,
  onCollapseSidebar,
  searchQuery,
  onSearchChange,
  statusFilter,
  onStatusFilterChange,
  hasFilters,
  onClearFilters,
  selectedIds,
  bulkPending,
  onSelectAllVisible,
  onRunBulk,
  onToggleSelect,
  filteredStacks,
  pinnedVisible,
  pinnedStacks,
  onTogglePin,
  useGroups,
  configuredDirs,
  tree,
  treeByRoot,
  collapsedGroups,
  toggleGroup,
}: SidebarBodyProps) {
  return (
    <>
      <SidebarHeader
        stackCount={stacks.length}
        updateCount={updateCount}
        selecting={selecting}
        onToggleSelecting={onToggleSelecting}
        onCollapseSidebar={onCollapseSidebar}
        searchQuery={searchQuery}
        onSearchChange={onSearchChange}
        statusFilter={statusFilter}
        onStatusFilterChange={onStatusFilterChange}
      />

      {selecting && (
        <BulkActionBar
          selectedCount={selectedIds.size}
          totalVisible={filteredStacks.length}
          bulkPending={bulkPending}
          onSelectAllVisible={onSelectAllVisible}
          onRunBulk={onRunBulk}
        />
      )}

      {hasFilters && (
        <FilterSummaryBar
          visibleCount={filteredStacks.length}
          totalCount={stacks.length}
          onClear={onClearFilters}
        />
      )}

      <StackListBody
        isLoading={isLoading}
        hasFilters={hasFilters}
        filteredStacks={filteredStacks}
        pinnedVisible={pinnedVisible}
        selecting={selecting}
        selectedIds={selectedIds}
        onToggleSelect={onToggleSelect}
        pinnedStacks={pinnedStacks}
        onTogglePin={onTogglePin}
        useGroups={useGroups}
        configuredDirs={configuredDirs}
        tree={tree}
        treeByRoot={treeByRoot}
        collapsedGroups={collapsedGroups}
        toggleGroup={toggleGroup}
      />

      <BackupStatusFooter backupStatus={backupStatus} />
    </>
  )
}
