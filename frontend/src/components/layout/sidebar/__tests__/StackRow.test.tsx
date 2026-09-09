import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { StackRow } from '../StackRow'
import type { Stack } from '@/types'

/**
 * agent-os-yy00. StackRow is the fourth Stack-side gate on `isGitRepo` and had
 * no test of its own: every sidebar fixture in the tree carries
 * `isGitRepo: false`, so the dirty dot was never rendered anywhere.
 *
 * The predicate itself is computed in the backend (`resolveGitState`), and this
 * component only reads a boolean off the wire, so these are OUTCOME arms — what
 * renders for a given wire value — not fail-first regression arms.
 */

const stack = (overrides: Partial<Stack> = {}): Stack => ({
  id: 's1',
  directory: '/srv/stacks/web',
  composeFile: 'docker-compose.yaml',
  projectName: 'web',
  status: 'running',
  isGitRepo: false,
  gitDirty: false,
  gitAhead: 0,
  gitBehind: 0,
  containers: [],
  ...overrides,
})

const renderRow = (s: Stack) =>
  render(
    <MemoryRouter>
      <StackRow
        stack={s}
        selecting={false}
        selected={false}
        onToggleSelect={vi.fn()}
        pinned={false}
        onTogglePin={vi.fn()}
      />
    </MemoryRouter>,
  )

// The dot has no accessible role or name; its title is the only handle a user
// or a test has on it, and it is the string an operator hovers to read.
const dirtyDot = () => document.querySelector('[title="Uncommitted changes"]')

describe('StackRow — the uncommitted-changes dot', () => {
  it('shows the dot for a dirty git-backed stack', () => {
    renderRow(stack({ isGitRepo: true, gitDirty: true }))

    expect(dirtyDot()).not.toBeNull()
  })

  // Both halves of the conjunction, each on its own, so the arm above cannot be
  // satisfied by a component that dropped one of them.
  it('hides the dot when the stack is a repository but clean', () => {
    renderRow(stack({ isGitRepo: true, gitDirty: false }))

    expect(dirtyDot()).toBeNull()
  })

  it('hides the dot when the stack is dirty but not a repository', () => {
    renderRow(stack({ isGitRepo: false, gitDirty: true }))

    expect(dirtyDot()).toBeNull()
  })

  it('still renders the row itself in every case', () => {
    renderRow(stack({ isGitRepo: true, gitDirty: true }))

    expect(screen.getByRole('link', { name: 'web - running' })).toBeInTheDocument()
  })
})
