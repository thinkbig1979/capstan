import { useMemo, useState, useEffect, useCallback, useRef } from 'react'
import { useNavigate } from 'react-router-dom'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent } from '@/components/ui/card'
import {
  ChevronDown,
  ChevronRight,
  Folder,
  FolderOpen,
  GitBranch,
  GitPullRequest,
} from 'lucide-react'
import { SortFilterBar } from '@/components/dashboard/SortFilterBar'
import type { ConfiguredDir } from '@/types'

interface Stack {
  id: string
  directory: string
}

interface DirTreeNode {
  name: string
  fullPath: string
  relativePath: string
  dir?: ConfiguredDir
  stackCount: number
  children: DirTreeNode[]
}

function findRootDir(dirPath: string, configuredDirs: string[]): string | null {
  for (const cd of configuredDirs) {
    if (dirPath === cd || dirPath.startsWith(cd + '/')) return cd
  }
  return null
}

function collapseNode(node: DirTreeNode): DirTreeNode {
  if (node.children.length === 0) return node

  const collapsedChildren = node.children
    .map(collapseNode)
    .sort((a, b) => a.name.localeCompare(b.name))

  if (collapsedChildren.length === 1 && !node.dir) {
    const child = collapsedChildren[0]
    return {
      ...child,
      name: `${node.name} / ${child.name}`,
    }
  }

  return { ...node, children: collapsedChildren }
}

function countSubtreeStacks(node: DirTreeNode): number {
  return (
    (node.dir?.stackCount ?? 0) +
    node.children.reduce((sum, c) => sum + countSubtreeStacks(c), 0)
  )
}

function buildDirTree(
  directories: ConfiguredDir[],
  rootDir: string,
): DirTreeNode[] {
  const dirsByPath = new Map<string, ConfiguredDir>()
  for (const dir of directories) {
    dirsByPath.set(dir.path, dir)
  }

  const nodeMap = new Map<string, DirTreeNode>()

  for (const dir of directories) {
    const relativePath = dir.path.slice(rootDir.length).replace(/^\//, '')
    const segments = relativePath.split('/').filter(Boolean)

    if (segments.length === 0) continue

    for (let i = 0; i < segments.length; i++) {
      const partialRelPath = segments.slice(0, i + 1).join('/')
      const key = partialRelPath
      const isLeaf = i === segments.length - 1

      if (!nodeMap.has(key)) {
        nodeMap.set(key, {
          name: segments[i],
          fullPath: `${rootDir}/${partialRelPath}`,
          relativePath: partialRelPath,
          dir: isLeaf ? dir : undefined,
          stackCount: isLeaf ? (dir.stackCount ?? 0) : 0,
          children: [],
        })
      } else if (isLeaf) {
        const node = nodeMap.get(key)!
        node.dir = dir
        node.stackCount = dir.stackCount ?? 0
      }
    }

    for (let i = segments.length - 1; i > 0; i--) {
      const childKey = segments.slice(0, i + 1).join('/')
      const parentKey = segments.slice(0, i).join('/')
      const child = nodeMap.get(childKey)!
      const parent = nodeMap.get(parentKey)!
      if (!parent.children.some((c) => c.fullPath === child.fullPath)) {
        parent.children.push(child)
      }
    }
  }

  for (const node of nodeMap.values()) {
    node.children.sort((a, b) => a.name.localeCompare(b.name))
  }

  const topNodes = [...nodeMap.values()]
    .filter((n) => !n.relativePath.includes('/'))
    .sort((a, b) => a.name.localeCompare(b.name))

  return topNodes.map(collapseNode)
}

function loadSavedCollapsed(): Set<string> | null {
  try {
    const raw = localStorage.getItem('dirs-tab-collapsed')
    if (raw) return new Set(JSON.parse(raw))
  } catch { /* ignore */ }
  return null
}

function computeDefaultCollapsed(
  rootGroups: { rootPath: string; tree: DirTreeNode[] }[],
): Set<string> {
  const collapsed = new Set<string>()
  const collectDeep = (nodes: DirTreeNode[], depth: number) => {
    for (const node of nodes) {
      if (depth >= 0 && node.children.length > 0) {
        collapsed.add(node.fullPath)
      }
      collectDeep(node.children, depth + 1)
    }
  }
  for (const group of rootGroups) {
    collectDeep(group.tree, 0)
  }
  return collapsed
}

function saveCollapsed(set: Set<string>) {
  localStorage.setItem('dirs-tab-collapsed', JSON.stringify([...set]))
}

function EmptyDirectories({ message }: { message: string }) {
  return (
    <Card>
      <CardContent className="pt-6">
        <p className="text-center text-muted-foreground">{message}</p>
      </CardContent>
    </Card>
  )
}

interface DirectoriesTabProps {
  directories: ConfiguredDir[]
  stacks: Stack[]
  configuredDirs: string[]
}

export function DirectoriesTab({ directories, stacks, configuredDirs }: DirectoriesTabProps) {
  const navigate = useNavigate()

  const rootGroups = useMemo(() => {
    const groups = new Map<string, { name: string; dirs: ConfiguredDir[] }>()

    for (const cd of configuredDirs) {
      const name = cd.split('/').filter(Boolean).pop() || cd
      groups.set(cd, { name, dirs: [] })
    }

    for (const dir of directories) {
      const rootDir = findRootDir(dir.path, configuredDirs)
      if (!rootDir) continue
      const group = groups.get(rootDir)
      if (group) group.dirs.push(dir)
    }

    return Array.from(groups.entries())
      .filter(([, { dirs }]) => dirs.length > 0)
      .map(([rootPath, { name, dirs }]) => ({
        rootPath,
        rootName: name,
        tree: buildDirTree(dirs, rootPath),
      }))
  }, [directories, configuredDirs])

  const [collapsedNodes, setCollapsedNodes] = useState<Set<string>>(
    () => loadSavedCollapsed() ?? new Set(),
  )
  const defaultsApplied = useRef(false)

  useEffect(() => {
    if (defaultsApplied.current) return
    if (rootGroups.length === 0) return
    defaultsApplied.current = true
    if (loadSavedCollapsed() !== null) return
    const defaults = computeDefaultCollapsed(rootGroups)
    if (defaults.size > 0) setCollapsedNodes(defaults)
  }, [rootGroups])

  useEffect(() => {
    saveCollapsed(collapsedNodes)
  }, [collapsedNodes])

  const nodeIndex = useMemo(() => {
    const idx = new Map<string, DirTreeNode>()
    const walk = (nodes: DirTreeNode[]) => {
      for (const n of nodes) {
        idx.set(n.fullPath, n)
        walk(n.children)
      }
    }
    for (const g of rootGroups) walk(g.tree)
    return idx
  }, [rootGroups])

  const toggleNode = useCallback((path: string) => {
    setCollapsedNodes((prev) => {
      const next = new Set(prev)
      if (next.has(path)) {
        next.delete(path)
        const node = nodeIndex.get(path)
        if (node) {
          const collapse = (nodes: DirTreeNode[]) => {
            for (const child of nodes) {
              if (child.children.length > 0) {
                next.add(child.fullPath)
                collapse(child.children)
              }
            }
          }
          collapse(node.children)
        }
      } else {
        next.add(path)
      }
      return next
    })
  }, [nodeIndex])

  const navigateToStack = useCallback(
    (dirPath: string) => {
      const firstStack = stacks.find((s) => s.directory === dirPath)
      if (firstStack) navigate(`/stacks/${firstStack.id}`)
    },
    [stacks, navigate],
  )

  const renderNode = (node: DirTreeNode, depth: number): React.ReactNode => {
    const isCollapsed = collapsedNodes.has(node.fullPath)
    const hasChildren = node.children.length > 0
    const totalStacks = countSubtreeStacks(node)
    const indentPx = depth * 24 + 16

    return (
      <div key={node.fullPath}>
        <div
          className={`flex items-center gap-2 py-2 px-4 border-b last:border-b-0 transition-colors ${
            node.dir
              ? 'cursor-pointer hover:bg-muted/50'
              : ''
          }`}
          style={{ paddingLeft: `${indentPx}px` }}
          onClick={() => node.dir && navigateToStack(node.dir.path)}
        >
          {hasChildren ? (
            <button
              onClick={(e) => {
                e.stopPropagation()
                toggleNode(node.fullPath)
              }}
              className="p-0.5 hover:bg-muted rounded shrink-0"
            >
              {isCollapsed ? (
                <ChevronRight className="h-3.5 w-3.5 text-muted-foreground" />
              ) : (
                <ChevronDown className="h-3.5 w-3.5 text-muted-foreground" />
              )}
            </button>
          ) : (
            <span className="w-[22px] shrink-0" />
          )}
          {hasChildren ? (
            <Folder className="h-4 w-4 text-muted-foreground shrink-0" />
          ) : (
            <FolderOpen className="h-4 w-4 text-muted-foreground shrink-0" />
          )}
          <span className="font-medium text-sm flex-1">{node.name}</span>
          <Badge variant="secondary" className="text-xs">
            {totalStacks}
          </Badge>
          {node.dir?.isGitRepo && (
            <Badge variant="outline" className="flex items-center gap-1 text-xs w-fit">
              <GitBranch className="h-3 w-3" />
              {node.dir.gitBranch || 'main'}
            </Badge>
          )}
          {node.dir?.isGitRepo && (node.dir.gitBehind ?? 0) > 0 && (
            <Badge variant="secondary" className="flex items-center gap-1 text-xs text-warning">
              <GitPullRequest className="h-3 w-3" />
              {node.dir.gitBehind}
            </Badge>
          )}
        </div>
        {!isCollapsed && node.children.map((child) => renderNode(child, depth + 1))}
      </div>
    )
  }

  return (
    <div className="space-y-4">
      <SortFilterBar
        sortOptions={[{ key: 'name', label: 'Name' }]}
        sortValue="name"
        onSortChange={() => {}}
        countDisplay={`${directories.length} directories`}
      />
      {directories.length === 0 ? (
        <EmptyDirectories message="No directories found" />
      ) : (
        <div className="space-y-4">
          {rootGroups.map((group) => {
            const isRootCollapsed = collapsedNodes.has(group.rootPath)
            const rootTotalStacks = group.tree.reduce(
              (sum, n) => sum + countSubtreeStacks(n),
              0,
            )

            return (
              <div key={group.rootPath} className="rounded-md border">
                <button
                  onClick={() => toggleNode(group.rootPath)}
                  className="flex items-center gap-2 w-full px-4 py-3 bg-muted/40 hover:bg-muted/60 transition-colors rounded-t-md"
                >
                  {isRootCollapsed ? (
                    <ChevronRight className="h-4 w-4 text-muted-foreground" />
                  ) : (
                    <ChevronDown className="h-4 w-4 text-muted-foreground" />
                  )}
                  <Folder className="h-4 w-4 text-muted-foreground" />
                  <span className="font-semibold text-sm">{group.rootName}</span>
                  <span className="text-xs text-muted-foreground ml-1">
                    {rootTotalStacks} stack{rootTotalStacks !== 1 ? 's' : ''}
                  </span>
                  <span className="text-xs text-muted-foreground ml-auto font-mono truncate max-w-[300px]">
                    {group.rootPath}
                  </span>
                </button>
                {!isRootCollapsed && (
                  <div>
                    {group.tree.map((node) => renderNode(node, 0))}
                  </div>
                )}
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}
