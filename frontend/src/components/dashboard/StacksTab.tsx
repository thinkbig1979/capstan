import { useMemo } from 'react'
import { useNavigate } from 'react-router'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { GitBranch, Plus } from 'lucide-react'
import { SortFilterBar } from '@/components/dashboard/SortFilterBar'
import { StatusBadge } from '@/components/dashboard/StatusBadge'
import { StackRowActions } from '@/components/dashboard/StackRowActions'
import { AutoUpdateToggle } from '@/components/dashboard/AutoUpdateToggle'
import { BackupToggle } from '@/components/dashboard/BackupToggle'
import {
  buildDirectoryTree,
  countTreeNodeStacks,
  type TreeNode,
} from '@/lib/stack-tree'
import type { AutoUpdatePolicy, Stack } from '@/types'
import { useTextFilter } from '@/hooks/useTextFilter'

const STACK_SEARCH_FIELDS = [
  (s: Stack) => s.projectName,
  (s: Stack) => s.status,
]

/**
 * The single source of truth for the table's width. Both the header cells and
 * the directory group-header row's colSpan derive from this, so adding a column
 * cannot leave the group header under-spanning the body.
 */
const STACK_TABLE_COLUMNS = [
  'Name',
  'Status',
  'Containers',
  'Auto update',
  'Auto backup',
  'Actions',
] as const

// The rows are role="link" and navigate on click, on Enter and on Space. A
// Switch inside one inherits all three, so a toggle would fire AND navigate
// away. Each toggle cell's content is wrapped in a div using this to keep the
// interaction local to the toggle. Propagation only, no preventDefault: the
// Switch is a button and needs its own default Space/Enter activation.
const stopRowNavigation = (e: React.SyntheticEvent) => e.stopPropagation()

type SortOption = 'name' | 'status'
type StatusFilter = 'all' | 'running' | 'stopped' | 'error'

interface StacksTabProps {
  stacks: Stack[]
  filteredStacks: Stack[]
  configuredDirs: string[]
  sortBy: SortOption
  statusFilter: StatusFilter
  onSortChange: (key: SortOption) => void
  onFilterChange: (key: StatusFilter) => void
  onCreateStack: () => void
  onStart: (stackId: string, e: React.MouseEvent) => void
  onStop: (stackId: string, e: React.MouseEvent) => void
  onRestart: (stackId: string, e: React.MouseEvent) => void
  onDelete: (stackId: string, stackName: string, e: React.MouseEvent) => void
  deletingStackId: string | null
  startPending: boolean
  stopPending: boolean
  restartPending: boolean
  deletePending: boolean
  isAnimating: (id: string) => boolean
  /** Auto-update policies for every target; fetched by DashboardPage so this
   *  component stays prop-driven. BackupToggle self-fetches instead — its
   *  queries are react-query keyed, so the rows dedupe. */
  autoUpdatePolicies: AutoUpdatePolicy[]
  globalAutoUpdateEnabled: boolean
}

export function StacksTab({
  stacks,
  filteredStacks,
  configuredDirs,
  sortBy,
  statusFilter,
  onSortChange,
  onFilterChange,
  onCreateStack,
  onStart,
  onStop,
  onRestart,
  onDelete,
  deletingStackId,
  startPending,
  stopPending,
  restartPending,
  deletePending,
  isAnimating,
  autoUpdatePolicies,
  globalAutoUpdateEnabled,
}: StacksTabProps) {
  const navigate = useNavigate()

  const { query, setQuery, filtered: textFilteredStacks } = useTextFilter(filteredStacks, STACK_SEARCH_FIELDS)

  const tree = useMemo(
    () => buildDirectoryTree(textFilteredStacks, configuredDirs),
    [textFilteredStacks, configuredDirs],
  )

  const renderNameCell = (stack: Stack, style?: React.CSSProperties) => (
    <TableCell className="font-medium font-mono text-[13px]" style={style}>
      <span className="inline-flex items-center gap-1.5">
        {stack.projectName}
        {stack.isGitRepo && (
          <GitBranch
            className="h-3 w-3 text-muted-foreground"
            data-testid={`git-repo-${stack.id}`}
          />
        )}
      </span>
    </TableCell>
  )

  const renderToggleCells = (stack: Stack) => {
    const policy = autoUpdatePolicies.find(
      (p) => p.targetType === 'stack' && p.targetId === stack.id,
    )
    return (
      <>
        <TableCell>
          <div onClick={stopRowNavigation} onKeyDown={stopRowNavigation}>
            <AutoUpdateToggle
              targetType="stack"
              targetId={stack.id}
              enabled={policy?.enabled ?? false}
              paused={policy?.paused ?? false}
              consecutiveFailures={policy?.consecutiveFailures ?? 0}
              globalDisabled={!globalAutoUpdateEnabled}
            />
          </div>
        </TableCell>
        <TableCell>
          <div onClick={stopRowNavigation} onKeyDown={stopRowNavigation}>
            {/* Install-wide, not per-stack, so a multi-row table must not
                render the last-run icon on every row — see BackupToggle. */}
            <BackupToggle stackId={stack.id} showLastRunStatus={false} />
          </div>
        </TableCell>
      </>
    )
  }

  const renderActionsCell = (stack: Stack) => (
    <TableCell>
      <StackRowActions
        stackId={stack.id}
        stackName={stack.projectName}
        status={stack.status}
        isDeleting={deletingStackId === stack.id}
        startPending={startPending}
        stopPending={stopPending}
        restartPending={restartPending}
        deletePending={deletePending}
        onStart={onStart}
        onStop={onStop}
        onRestart={onRestart}
        onDelete={onDelete}
      />
    </TableCell>
  )

  const renderStatusAndCountCells = (stack: Stack) => (
    <>
      <TableCell>
        <StatusBadge status={stack.status as 'running' | 'stopped' | 'partial' | 'error' | 'unknown'} pulse={isAnimating(stack.id)} />
      </TableCell>
      <TableCell>
        {stack.containers?.length ? (
          <Badge variant="outline">{stack.containers.length}</Badge>
        ) : (
          <span className="text-sm text-muted-foreground">-</span>
        )}
      </TableCell>
    </>
  )

  const rowNavigationProps = (stackId: string) => ({
    className: 'cursor-pointer',
    role: 'link',
    tabIndex: 0,
    onClick: () => navigate(`/stacks/${stackId}`),
    onKeyDown: (e: React.KeyboardEvent) => {
      if (e.key === 'Enter' || e.key === ' ') {
        e.preventDefault()
        navigate(`/stacks/${stackId}`)
      }
    },
  })

  const renderTreeNodes = (nodes: TreeNode[], depth: number): React.ReactNode[] => {
    return nodes.flatMap((node) => {
      const totalStacks = countTreeNodeStacks(node)
      const headerPadding = depth * 16 + 16
      const stackPadding = depth * 16 + 24

      return [
        <TableRow
          key={`group-${node.fullPath}`}
          className="bg-muted/50 hover:bg-muted/50"
        >
          <TableCell
            colSpan={STACK_TABLE_COLUMNS.length}
            className="py-1.5"
            style={{ paddingLeft: `${headerPadding}px` }}
          >
            <span className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
              {node.name}
            </span>
            {node.stacks[0]?.isGitRepo && (
              <GitBranch
                className="inline h-3 w-3 ml-1.5 text-muted-foreground align-middle"
                data-testid={`git-repo-group-${node.name}`}
              />
            )}
            <span className="text-xs text-muted-foreground ml-2">
              {totalStacks} stack{totalStacks !== 1 ? 's' : ''}
            </span>
          </TableCell>
        </TableRow>,
        ...node.stacks.map((stack) => (
          <TableRow key={stack.id} {...rowNavigationProps(stack.id)}>
            {renderNameCell(stack, { paddingLeft: `${stackPadding}px` })}
            {renderStatusAndCountCells(stack)}
            {renderToggleCells(stack)}
            {renderActionsCell(stack)}
          </TableRow>
        )),
        ...renderTreeNodes(node.children, depth + 1),
      ]
    })
  }

  const hasGroups = tree.length > 1 || tree.some((n) => n.children.length > 0 || n.stacks.length > 1)

  return (
    <div className="space-y-4">
      <SortFilterBar
        sortOptions={[
          { key: 'name', label: 'Name' },
          { key: 'status', label: 'Status' },
        ]}
        sortValue={sortBy}
        onSortChange={(key) => onSortChange(key as SortOption)}
        filterOptions={[
          { key: 'all', label: 'All' },
          { key: 'running', label: 'Running' },
          { key: 'stopped', label: 'Stopped' },
          { key: 'error', label: 'Error' },
        ]}
        filterValue={statusFilter}
        onFilterChange={(key) => onFilterChange(key as StatusFilter)}
        searchValue={query}
        onSearchChange={setQuery}
        searchPlaceholder="Filter stacks…"
        countDisplay={
          query
            ? `${textFilteredStacks.length} of ${stacks.length} stacks`
            : (
              <>
                <span className="font-medium">{filteredStacks.length}</span> of <span className="font-medium">{stacks.length}</span>
              </>
            )
        }
      />
      {textFilteredStacks.length > 0 ? (
        <div className="rounded-md border">
          <Table>
            <TableHeader>
              <TableRow>
                {STACK_TABLE_COLUMNS.map((column) => (
                  <TableHead key={column}>{column}</TableHead>
                ))}
              </TableRow>
            </TableHeader>
            <TableBody>
              {hasGroups ? (
                renderTreeNodes(tree, 0)
              ) : (
                textFilteredStacks.map((stack) => (
                  <TableRow key={stack.id} {...rowNavigationProps(stack.id)}>
                    {renderNameCell(stack)}
                    {renderStatusAndCountCells(stack)}
                    {renderToggleCells(stack)}
                    {renderActionsCell(stack)}
                  </TableRow>
                ))
              )}
            </TableBody>
          </Table>
        </div>
      ) : (
        <Card>
          <CardContent className="pt-6">
            <div className="text-center space-y-4">
              <p className="text-muted-foreground">
                {query
                  ? `No stacks match "${query}"`
                  : statusFilter === 'all'
                    ? 'No stacks configured yet'
                    : `No ${statusFilter} stacks found`}
              </p>
              {statusFilter === 'all' && !query && (
                <Button onClick={onCreateStack}>
                  <Plus className="mr-2 h-4 w-4" />
                  Create Your First Stack
                </Button>
              )}
            </div>
          </CardContent>
        </Card>
      )}
    </div>
  )
}
