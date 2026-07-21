import { describe, it, expect } from 'vitest'
import { buildDirectoryTree, countTreeNodeStacks, type TreeNode } from '../stack-tree'
import type { Stack } from '@/types'

function countAll(nodes: TreeNode[]): number {
  return nodes.reduce((sum, n) => sum + countTreeNodeStacks(n), 0)
}

function makeStack(overrides: Partial<Stack> & Pick<Stack, 'id' | 'directory'>): Stack {
  return {
    composeFile: 'docker-compose.yml',
    projectName: overrides.id,
    status: 'running',
    isGitRepo: false,
    gitDirty: false,
    gitAhead: 0,
    gitBehind: 0,
    ...overrides,
  }
}

describe('buildDirectoryTree', () => {
  // Regression: stacksByDir used `.get(dir)!.push(stack)` after a has-check,
  // which crashes if the get and the has-check ever fall out of sync (e.g. a
  // future refactor reorders them, or the key gets normalized differently
  // between the has/get calls). Multiple stacks sharing one directory is the
  // path that exercises the second (non-set) branch of that lookup.
  it('groups multiple stacks in the same directory without crashing', () => {
    const stacks: Stack[] = [
      makeStack({ id: 's1', directory: '/stacks/shared' }),
      makeStack({ id: 's2', directory: '/stacks/shared' }),
      makeStack({ id: 's3', directory: '/stacks/shared' }),
    ]

    const tree = buildDirectoryTree(stacks, ['/stacks'])

    expect(countAll(tree)).toBe(3)
    const node = tree.find((n) => n.fullPath === '/stacks/shared')
    expect(node?.stacks.map((s) => s.id).sort()).toEqual(['s1', 's2', 's3'])
  })

  it('places each stack directory under its configured root', () => {
    const stacks: Stack[] = [
      makeStack({ id: 'a', directory: '/stacks/group-a/app' }),
      makeStack({ id: 'b', directory: '/stacks/group-b/app' }),
    ]

    const tree = buildDirectoryTree(stacks, ['/stacks'])

    expect(countAll(tree)).toBe(2)
  })

  it('ignores stacks outside every configured directory', () => {
    const stacks: Stack[] = [makeStack({ id: 'outside', directory: '/elsewhere/app' })]

    const tree = buildDirectoryTree(stacks, ['/stacks'])

    expect(tree).toEqual([])
  })
})
