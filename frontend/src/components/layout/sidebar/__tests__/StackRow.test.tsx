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
  envFile: '',
  gitBranch: '',
  gitCommit: '',
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

const renderRow = (s: Stack, pinned = false) =>
  render(
    <MemoryRouter>
      <StackRow
        stack={s}
        selecting={false}
        selected={false}
        onToggleSelect={vi.fn()}
        pinned={pinned}
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

describe('StackRow — the status dot', () => {
  // agent-os-n97z: 'paused' had no statusDotColor entry and fell back to the
  // unknown grey, indistinguishable from a stopped stack.
  it('gives a paused stack the warning dot, not the unknown grey', () => {
    renderRow(stack({ status: 'paused' }))

    const dot = screen.getByRole('link', { name: 'web - paused' }).querySelector('span[aria-hidden="true"]')
    expect(dot?.className).toContain('bg-warning')
    expect(dot?.className).not.toContain('bg-muted-foreground')
  })
})

// agent-os-eqif: statusStale means the live Docker read failed and the status on
// the wire is the last stored one. The sidebar has no page-level notice, so the
// row itself must not present that status as live.
describe('StackRow — a stale status', () => {
  const dotOf = (el: HTMLElement) => el.querySelector('span[aria-hidden="true"]')

  it('shows a neutral dot and discloses the stored status on the link', () => {
    renderRow(stack({ status: 'running', statusStale: true }))

    const link = screen.getByRole('link', {
      name: 'web - status may be out of date (last recorded: running)',
    })
    expect(link).toHaveAttribute('title', 'Status may be out of date (last recorded: running)')
    expect(dotOf(link)?.className).toContain('bg-muted-foreground')
    expect(dotOf(link)?.className).not.toContain('bg-success')
  })

  it('shows a neutral dot in selection mode too', () => {
    render(
      <MemoryRouter>
        <StackRow
          stack={stack({ status: 'running', statusStale: true })}
          selecting
          selected={false}
          onToggleSelect={vi.fn()}
          pinned={false}
          onTogglePin={vi.fn()}
        />
      </MemoryRouter>,
    )

    const row = screen.getByRole('button', { pressed: false })
    expect(row).toHaveAttribute('title', 'Status may be out of date (last recorded: running)')
    const dot = row.querySelector('span.rounded-full')
    expect(dot?.className).toContain('bg-muted-foreground')
    expect(dot?.className).not.toContain('bg-success')
  })

  // Control: a live status (flag absent or false) is unchanged.
  it.each([undefined, false])('keeps the live dot when statusStale is %s', (statusStale) => {
    renderRow(stack({ status: 'running', statusStale }))

    const link = screen.getByRole('link', { name: 'web - running' })
    expect(link).not.toHaveAttribute('title')
    expect(dotOf(link)?.className).toContain('bg-success')
  })
})

/**
 * agent-os-eldv, part 1. The control's words all say "pin" (aria-label, title)
 * and it used to draw a Star, so a screen-reader user and a sighted user got
 * two different models of one button. The words are the decided vocabulary;
 * the icon follows them. lucide stamps `lucide-<name>` on every icon's svg.
 */
describe('StackRow — the pin control draws a pin', () => {
  it.each([
    [false, 'Pin web', 'Pin to top'],
    [true, 'Unpin web', 'Unpin'],
  ])('pinned=%s: named %j, titled %j, drawn as a Pin', (pinned, name, title) => {
    renderRow(stack(), pinned)

    const button = screen.getByRole('button', { name })
    expect(button).toHaveAttribute('title', title)
    const svg = button.querySelector('svg')
    expect(svg?.classList.contains('lucide-pin')).toBe(true)
    expect(svg?.classList.contains('lucide-star')).toBe(false)
  })
})
