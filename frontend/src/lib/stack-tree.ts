import type { Stack } from '@/types'

export interface TreeNode {
  name: string
  fullPath: string
  relativePath: string
  stacks: Stack[]
  children: TreeNode[]
}

function findRootDir(dirPath: string, configuredDirs: string[]): string | null {
  for (const cd of configuredDirs) {
    if (dirPath === cd || dirPath.startsWith(cd + '/')) return cd
  }
  return null
}

function collapseNode(node: TreeNode): TreeNode {
  if (node.children.length === 0) return node

  const collapsedChildren = node.children
    .map(collapseNode)
    .sort((a, b) => a.name.localeCompare(b.name))

  if (collapsedChildren.length === 1 && node.stacks.length === 0) {
    const child = collapsedChildren[0]
    return {
      ...child,
      name: `${node.name} / ${child.name}`,
    }
  }

  return { ...node, children: collapsedChildren }
}

export function countTreeNodeStacks(node: TreeNode): number {
  return node.stacks.length + node.children.reduce((sum, c) => sum + countTreeNodeStacks(c), 0)
}

export function hasTreeNesting(nodes: TreeNode[]): boolean {
  return nodes.some(
    (n) => n.children.length > 0 || n.stacks.length > 1,
  )
}

export function buildDirectoryTree(
  stacks: Stack[],
  configuredDirs: string[],
): TreeNode[] {
  const stacksByDir = new Map<string, Stack[]>()
  for (const stack of stacks) {
    const dirStacks = stacksByDir.get(stack.directory) ?? []
    dirStacks.push(stack)
    stacksByDir.set(stack.directory, dirStacks)
  }

  interface DirInfo {
    rootDir: string
    relativePath: string
    fullPath: string
    stacks: Stack[]
  }

  const dirs: DirInfo[] = []
  for (const [dirPath, dirStacks] of stacksByDir) {
    const rootDir = findRootDir(dirPath, configuredDirs)
    if (!rootDir) continue
    const relativePath = dirPath.slice(rootDir.length).replace(/^\//, '')
    dirs.push({ rootDir, relativePath, fullPath: dirPath, stacks: dirStacks })
  }

  const nodeMap = new Map<string, TreeNode>()

  for (const dir of dirs) {
    const segments = dir.relativePath.split('/').filter(Boolean)

    if (segments.length === 0) {
      const key = dir.rootDir + ':__root__'
      if (!nodeMap.has(key)) {
        nodeMap.set(key, {
          name: dir.rootDir.split('/').pop() || dir.rootDir,
          fullPath: dir.fullPath,
          relativePath: '',
          stacks: [...dir.stacks],
          children: [],
        })
      } else {
        nodeMap.get(key)!.stacks.push(...dir.stacks)
      }
      continue
    }

    for (let i = 0; i < segments.length; i++) {
      const partialRelPath = segments.slice(0, i + 1).join('/')
      const key = dir.rootDir + ':' + partialRelPath
      const isLeaf = i === segments.length - 1

      if (!nodeMap.has(key)) {
        nodeMap.set(key, {
          name: segments[i],
          fullPath: isLeaf ? dir.fullPath : `${dir.rootDir}/${partialRelPath}`,
          relativePath: partialRelPath,
          stacks: isLeaf ? [...dir.stacks] : [],
          children: [],
        })
      } else if (isLeaf) {
        const node = nodeMap.get(key)!
        node.stacks.push(...dir.stacks)
        if (!node.fullPath || node.fullPath === `${dir.rootDir}/${partialRelPath}`) {
          node.fullPath = dir.fullPath
        }
      }
    }

    for (let i = segments.length - 1; i > 0; i--) {
      const childKey = dir.rootDir + ':' + segments.slice(0, i + 1).join('/')
      const parentKey = dir.rootDir + ':' + segments.slice(0, i).join('/')
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
    .filter((n) => {
      if (n.relativePath === '') return true
      return !n.relativePath.includes('/')
    })
    .sort((a, b) => a.name.localeCompare(b.name))

  return topNodes.map(collapseNode)
}
