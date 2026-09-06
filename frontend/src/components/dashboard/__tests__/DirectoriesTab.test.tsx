import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { DirectoriesTab } from '../DirectoriesTab'
import type { ConfiguredDir } from '@/types'

/**
 * DashboardPage.test.tsx:83 replaces this tab with an empty div, and Radix Tabs
 * unmount inactive content so the Playwright specs never render it either. It
 * was the largest single hole in the frontend at 0/158 statements
 * (agent-os-m1mu). It takes everything as props, so it needs no API mocks —
 * only a Router, for useNavigate.
 */

const mockNavigate = vi.fn()
vi.mock('react-router', async () => {
  const actual = await vi.importActual<typeof import('react-router')>('react-router')
  return { ...actual, useNavigate: () => mockNavigate }
})

const dir = (path: string, over: Partial<ConfiguredDir> = {}): ConfiguredDir => ({
  path,
  name: path.split('/').filter(Boolean).pop() ?? path,
  isDefault: false,
  stackCount: 1,
  ...over,
})

const stack = (id: string, directory: string) => ({ id, directory })

function renderTab(props: {
  directories: ConfiguredDir[]
  stacks?: { id: string; directory: string }[]
  configuredDirs: string[]
}) {
  return render(
    <MemoryRouter>
      <DirectoriesTab
        directories={props.directories}
        stacks={props.stacks ?? []}
        configuredDirs={props.configuredDirs}
      />
    </MemoryRouter>,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  localStorage.clear()
})

describe('DirectoriesTab — empty states', () => {
  it('says so when there are no directories at all', () => {
    renderTab({ directories: [], configuredDirs: ['/srv/stacks'] })

    expect(screen.getByText('No directories found')).toBeInTheDocument()
    expect(screen.getByText('0 directories')).toBeInTheDocument()
  })

  it('drops a directory that sits under no configured root', () => {
    renderTab({ directories: [dir('/elsewhere/orphan')], configuredDirs: ['/srv/stacks'] })

    expect(screen.queryByText('orphan')).not.toBeInTheDocument()
    // Counted in the header, which reports the raw prop, but not rendered.
    expect(screen.getByText('1 directories')).toBeInTheDocument()
  })

  it('hides a configured root that ended up with no directories', () => {
    renderTab({
      directories: [dir('/srv/stacks/web')],
      configuredDirs: ['/srv/stacks', '/mnt/empty'],
    })

    expect(screen.getByText('stacks')).toBeInTheDocument()
    expect(screen.queryByText('empty')).not.toBeInTheDocument()
  })
})

describe('DirectoriesTab — grouping and the tree', () => {
  it('groups directories under their configured root, showing the root path', () => {
    renderTab({
      directories: [dir('/srv/stacks/web'), dir('/mnt/extra/api')],
      configuredDirs: ['/srv/stacks', '/mnt/extra'],
    })

    expect(screen.getByText('/srv/stacks')).toBeInTheDocument()
    expect(screen.getByText('/mnt/extra')).toBeInTheDocument()
    expect(screen.getByText('web')).toBeInTheDocument()
    expect(screen.getByText('api')).toBeInTheDocument()
  })

  it('matches the longest configured root, not merely a prefix of the path', () => {
    renderTab({
      directories: [dir('/srv/stacks-archive/old')],
      configuredDirs: ['/srv/stacks'],
    })

    // '/srv/stacks-archive/old' must NOT be treated as living under
    // '/srv/stacks' — findRootDir requires an exact match or a '/' boundary.
    expect(screen.queryByText('old')).not.toBeInTheDocument()
  })

  it('collapses a chain of single children into one row', () => {
    renderTab({
      directories: [dir('/srv/stacks/apps/prod/web')],
      configuredDirs: ['/srv/stacks'],
    })

    expect(screen.getByText('apps / prod / web')).toBeInTheDocument()
  })

  it('keeps a branching level as its own row', () => {
    renderTab({
      directories: [dir('/srv/stacks/apps/web'), dir('/srv/stacks/apps/api')],
      configuredDirs: ['/srv/stacks'],
    })

    // 'apps' has two children so it is not collapsed into its child, and it
    // starts collapsed by default — expand it to see them.
    expect(screen.getByText('apps')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Expand folder' }))

    expect(screen.getByText('web')).toBeInTheDocument()
    expect(screen.getByText('api')).toBeInTheDocument()
  })

  it('sums stack counts up the subtree', () => {
    renderTab({
      directories: [
        dir('/srv/stacks/apps/web', { stackCount: 2 }),
        dir('/srv/stacks/apps/api', { stackCount: 3 }),
      ],
      configuredDirs: ['/srv/stacks'],
    })

    expect(screen.getByText('5 stacks')).toBeInTheDocument()
  })

  it('uses singular wording for exactly one stack', () => {
    renderTab({
      directories: [dir('/srv/stacks/web', { stackCount: 1 })],
      configuredDirs: ['/srv/stacks'],
    })

    expect(screen.getByText('1 stack')).toBeInTheDocument()
  })

  it('treats a missing stackCount as zero', () => {
    renderTab({
      directories: [dir('/srv/stacks/web', { stackCount: undefined })],
      configuredDirs: ['/srv/stacks'],
    })

    expect(screen.getByText('0 stacks')).toBeInTheDocument()
  })
})

describe('DirectoriesTab — git badges', () => {
  it('shows the branch for a git-backed directory', () => {
    renderTab({
      directories: [dir('/srv/stacks/web', { isGitRepo: true, gitBranch: 'release' })],
      configuredDirs: ['/srv/stacks'],
    })

    expect(screen.getByText('release')).toBeInTheDocument()
  })

  /**
   * This comment used to record that the scanner leaves gitBranch EMPTY for a
   * detached HEAD, an unreadable HEAD, and a worktree or submodule checkout,
   * and that the em dash was the right answer for all of them. That was true of
   * the scanner and it is no longer true of it (agent-os-jieh): collapsing four
   * different situations into one glyph is exactly the bug that bead fixed,
   * because a detached checkout is deliberate and a read fault is not, and an
   * operator cannot tell them apart from an em dash.
   *
   * What the scanner sends now (backend/internal/services/scanner.go,
   * resolveGitState):
   *
   *   'release'                a branch, attached — unchanged
   *   'detached@abc1234'       HEAD holds an object name
   *   'unknown (read failed)'  HEAD unreadable, .git unstat-able, or a broken
   *                            worktree pointer. Contains a space, which git
   *                            forbids in a ref name, so it cannot be a branch
   *   'feature/login'          a linked worktree or submodule: the gitdir:
   *                            pointer is followed, so this is no longer a
   *                            failure state at all
   *
   * So the em dash below is NOT dead. It is what remains when gitBranch arrives
   * empty or absent, which is two cases and neither of them a scan fault:
   * directories/stacks rows written by a Capstan older than this change and not
   * yet rescanned, and any payload that omits the field at all — ConfiguredDir
   * and Stack both declare `gitBranch?: string` (types/index.ts:20, :104), so
   * absent reaches the same `||`.
   */
  it('renders the unknown marker, not a fabricated "main", when the branch is empty', () => {
    const { unmount } = renderTab({
      directories: [dir('/srv/stacks/web', { isGitRepo: true, gitBranch: '' })],
      configuredDirs: ['/srv/stacks'],
    })

    expect(screen.queryByText('main')).not.toBeInTheDocument()
    expect(screen.getByText('\u2014')).toBeInTheDocument()
    unmount()
    localStorage.clear()

    // ConfiguredDir declares gitBranch?: string, so absent reaches the same ||.
    renderTab({
      directories: [dir('/srv/stacks/web', { isGitRepo: true })],
      configuredDirs: ['/srv/stacks'],
    })

    expect(screen.queryByText('main')).not.toBeInTheDocument()
    expect(screen.getByText('\u2014')).toBeInTheDocument()
  })

  /**
   * The three strings the scanner can now send have to stay VISIBLY different
   * from each other in the badge, which is the whole point of agent-os-jieh.
   * Asserted as whole strings via getByText's exact match, never a substring:
   * a `toContain('detached')` assertion is satisfied by a branch legitimately
   * named 'detached-hotfix', which would put the collapse straight back.
   */
  it('distinguishes a detached HEAD, a read fault and a real branch', () => {
    const cases: [string, string][] = [
      ['detached@abc1234', 'a detached checkout'],
      ['unknown (read failed)', 'a scan that could not read the repo'],
      ['feature/login', 'a branch resolved through a worktree pointer'],
    ]

    const seen = new Set<string>()
    for (const [branch, what] of cases) {
      const { unmount } = renderTab({
        directories: [dir('/srv/stacks/web', { isGitRepo: true, gitBranch: branch })],
        configuredDirs: ['/srv/stacks'],
      })

      expect(screen.getByText(branch), what).toBeInTheDocument()
      // None of them may fall through to the em dash, which is what every one
      // of them rendered as before the scanner learned to tell them apart.
      expect(screen.queryByText('—')).not.toBeInTheDocument()

      seen.add(branch)
      unmount()
      localStorage.clear()
    }

    expect(seen.size).toBe(cases.length)
  })

  it('shows the behind-count only when there is something to pull', () => {
    const { unmount } = renderTab({
      directories: [dir('/srv/stacks/web', { isGitRepo: true, gitBehind: 0 })],
      configuredDirs: ['/srv/stacks'],
    })
    expect(screen.queryByText('3')).not.toBeInTheDocument()
    unmount()
    localStorage.clear()

    renderTab({
      directories: [dir('/srv/stacks/web', { isGitRepo: true, gitBehind: 3 })],
      configuredDirs: ['/srv/stacks'],
    })
    expect(screen.getByText('3')).toBeInTheDocument()
  })

  it('shows no git badge for a plain directory', () => {
    renderTab({
      directories: [dir('/srv/stacks/web', { isGitRepo: false, gitBranch: 'main' })],
      configuredDirs: ['/srv/stacks'],
    })

    expect(screen.queryByText('main')).not.toBeInTheDocument()
  })

  // The fixture above carries a real branch name, so dropping the isGitRepo
  // gate is caught there by 'main' appearing. A non-git directory with NO
  // branch is the case only the unknown marker can catch, so it gets its own
  // arm rather than an extra line on a test that already discriminates.
  it('shows no git badge for a plain directory that has no branch either', () => {
    renderTab({
      directories: [dir('/srv/stacks/web', { isGitRepo: false, gitBranch: '' })],
      configuredDirs: ['/srv/stacks'],
    })

    expect(screen.queryByText('\u2014')).not.toBeInTheDocument()
  })
})

describe('DirectoriesTab — collapsing', () => {
  it('collapses and re-expands a whole root group', () => {
    renderTab({
      directories: [dir('/srv/stacks/web')],
      configuredDirs: ['/srv/stacks'],
    })

    expect(screen.getByText('web')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /stacks/ }))
    expect(screen.queryByText('web')).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /stacks/ }))
    expect(screen.getByText('web')).toBeInTheDocument()
  })

  it('collapses parent folders by default when nothing was saved', () => {
    renderTab({
      directories: [dir('/srv/stacks/apps/web'), dir('/srv/stacks/apps/api')],
      configuredDirs: ['/srv/stacks'],
    })

    // 'apps' branches, so it starts collapsed and its children are hidden.
    expect(screen.getByText('apps')).toBeInTheDocument()
    expect(screen.queryByText('web')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Expand folder' })).toBeInTheDocument()
  })

  it('expands a folder without expanding its grandchildren', () => {
    renderTab({
      directories: [
        dir('/srv/stacks/apps/prod/web'),
        dir('/srv/stacks/apps/prod/api'),
        dir('/srv/stacks/apps/dev/web'),
      ],
      configuredDirs: ['/srv/stacks'],
    })

    fireEvent.click(screen.getByRole('button', { name: 'Expand folder' }))

    expect(screen.getByText('prod')).toBeInTheDocument()
    // 'prod' itself stays collapsed — expanding is one level at a time.
    expect(screen.queryByText('api')).not.toBeInTheDocument()
  })

  it('persists the collapsed set to localStorage', () => {
    renderTab({ directories: [dir('/srv/stacks/web')], configuredDirs: ['/srv/stacks'] })

    fireEvent.click(screen.getByRole('button', { name: /stacks/ }))

    expect(localStorage.getItem('dirs-tab-collapsed:v1')).toContain('/srv/stacks')
  })

  it('restores a saved collapsed set instead of applying the defaults', () => {
    localStorage.setItem('dirs-tab-collapsed:v1', JSON.stringify(['/srv/stacks']))

    renderTab({ directories: [dir('/srv/stacks/web')], configuredDirs: ['/srv/stacks'] })

    expect(screen.queryByText('web')).not.toBeInTheDocument()
  })

  it('honours the pre-versioning key so existing users keep their state', () => {
    localStorage.setItem('dirs-tab-collapsed', JSON.stringify(['/srv/stacks']))

    renderTab({ directories: [dir('/srv/stacks/web')], configuredDirs: ['/srv/stacks'] })

    expect(screen.queryByText('web')).not.toBeInTheDocument()
  })
})

describe('DirectoriesTab — filtering', () => {
  const THREE = {
    directories: [dir('/srv/stacks/web'), dir('/srv/stacks/api'), dir('/srv/stacks/db')],
    configuredDirs: ['/srv/stacks'],
  }

  it('filters directories by name', () => {
    renderTab(THREE)

    fireEvent.change(screen.getByPlaceholderText('Filter…'), { target: { value: 'web' } })

    expect(screen.getByText('web')).toBeInTheDocument()
    expect(screen.queryByText('api')).not.toBeInTheDocument()
  })

  it('is case-insensitive', () => {
    renderTab(THREE)

    fireEvent.change(screen.getByPlaceholderText('Filter…'), { target: { value: 'WEB' } })

    expect(screen.getByText('web')).toBeInTheDocument()
  })

  it('also matches on the path of a stack inside the directory', () => {
    renderTab({
      directories: [dir('/srv/stacks/alpha'), dir('/srv/stacks/beta')],
      stacks: [stack('s1', '/srv/stacks/alpha/nginx-proxy')],
      configuredDirs: ['/srv/stacks'],
    })

    fireEvent.change(screen.getByPlaceholderText('Filter…'), { target: { value: 'nginx' } })

    expect(screen.getByText('alpha')).toBeInTheDocument()
    expect(screen.queryByText('beta')).not.toBeInTheDocument()
  })

  it('reports when nothing matches', () => {
    renderTab(THREE)

    fireEvent.change(screen.getByPlaceholderText('Filter…'), { target: { value: 'zzz' } })

    expect(screen.getByText('No directories match your filter.')).toBeInTheDocument()
  })

  it('restores everything when the filter is cleared', () => {
    renderTab(THREE)

    const filter = screen.getByPlaceholderText('Filter…')
    fireEvent.change(filter, { target: { value: 'zzz' } })
    fireEvent.change(filter, { target: { value: '' } })

    expect(screen.getByText('web')).toBeInTheDocument()
    expect(screen.getByText('api')).toBeInTheDocument()
  })

  it('ignores surrounding whitespace', () => {
    renderTab(THREE)

    fireEvent.change(screen.getByPlaceholderText('Filter…'), { target: { value: '  web  ' } })

    expect(screen.getByText('web')).toBeInTheDocument()
    expect(screen.queryByText('api')).not.toBeInTheDocument()
  })
})

describe('DirectoriesTab — navigation', () => {
  it('opens the first stack in a directory when its row is clicked', () => {
    renderTab({
      directories: [dir('/srv/stacks/web')],
      stacks: [stack('stack-1', '/srv/stacks/web')],
      configuredDirs: ['/srv/stacks'],
    })

    fireEvent.click(screen.getByText('web'))

    expect(mockNavigate).toHaveBeenCalledWith('/stacks/stack-1')
  })

  it('does nothing when the directory holds no stack', () => {
    renderTab({
      directories: [dir('/srv/stacks/web')],
      stacks: [],
      configuredDirs: ['/srv/stacks'],
    })

    fireEvent.click(screen.getByText('web'))

    expect(mockNavigate).not.toHaveBeenCalled()
  })

  it('does not navigate when the chevron is used to expand', () => {
    renderTab({
      directories: [dir('/srv/stacks/apps/web'), dir('/srv/stacks/apps/api')],
      stacks: [stack('stack-1', '/srv/stacks/apps')],
      configuredDirs: ['/srv/stacks'],
    })

    // The chevron stops propagation so expanding never doubles as a navigation.
    fireEvent.click(screen.getByRole('button', { name: 'Expand folder' }))

    expect(mockNavigate).not.toHaveBeenCalled()
  })
})

describe('DirectoriesTab — header', () => {
  it('reports the raw directory count', () => {
    renderTab({
      directories: [dir('/srv/stacks/web'), dir('/srv/stacks/api')],
      configuredDirs: ['/srv/stacks'],
    })

    const bar = screen.getByText('2 directories')
    expect(within(bar.closest('div')!).getByText('2 directories')).toBeInTheDocument()
  })
})
