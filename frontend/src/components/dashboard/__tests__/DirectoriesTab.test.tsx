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

  it('falls back to "main" when the branch is unknown', () => {
    renderTab({
      directories: [dir('/srv/stacks/web', { isGitRepo: true })],
      configuredDirs: ['/srv/stacks'],
    })

    expect(screen.getByText('main')).toBeInTheDocument()
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
