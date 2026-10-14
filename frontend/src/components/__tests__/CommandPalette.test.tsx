import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider, onlineManager } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router'
import { CommandPalette } from '../CommandPalette'
import { stacksApi } from '@/lib/api'
import { HELP_INDEX, useHelpStore } from '@/help/helpStore'
import type { HelpDoc } from '@/help/content'

// Wraps the real loader so help tests can hold it pending or make it fail;
// every other test gets the real docs.
const loader = vi.hoisted(() => ({ actual: null as null | (() => Promise<HelpDoc[]>) }))
vi.mock('@/help/content', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/help/content')>()
  loader.actual = actual.loadHelpLibrary
  return { ...actual, loadHelpLibrary: vi.fn(actual.loadHelpLibrary) }
})
const { loadHelpLibrary } = await import('@/help/content')

// Mock react-router's useNavigate
const mockNavigate = vi.fn()
vi.mock('react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('react-router')>()
  return {
    ...actual,
    useNavigate: () => mockNavigate,
  }
})

// Mock stacks API
vi.mock('@/lib/api', () => ({
  stacksApi: {
    list: vi.fn().mockResolvedValue([
      { id: 'stack-1', projectName: 'nginx-proxy', status: 'running', containers: [], directory: '/stacks' },
      { id: 'stack-2', projectName: 'postgres-db', status: 'stopped', containers: [], directory: '/stacks' },
    ]),
  },
}))

function createTestQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0 },
    },
  })
}

function renderPalette() {
  const queryClient = createTestQueryClient()
  return render(
    <MemoryRouter>
      <QueryClientProvider client={queryClient}>
        <CommandPalette />
      </QueryClientProvider>
    </MemoryRouter>,
  )
}

describe('CommandPalette', () => {
  beforeEach(() => {
    mockNavigate.mockClear()
  })

  it('is not visible before Ctrl-K is pressed', () => {
    renderPalette()
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('opens on Ctrl-K', async () => {
    renderPalette()
    fireEvent.keyDown(document, { key: 'k', ctrlKey: true })
    await waitFor(() => {
      expect(screen.getByRole('dialog')).toBeInTheDocument()
    })
  })

  it('opens on Cmd-K (metaKey)', async () => {
    renderPalette()
    fireEvent.keyDown(document, { key: 'k', metaKey: true })
    await waitFor(() => {
      expect(screen.getByRole('dialog')).toBeInTheDocument()
    })
  })

  it('lists stacks when open', async () => {
    renderPalette()
    fireEvent.keyDown(document, { key: 'k', ctrlKey: true })
    await waitFor(() => {
      expect(screen.getByText('nginx-proxy')).toBeInTheDocument()
      expect(screen.getByText('postgres-db')).toBeInTheDocument()
    })
  })

  it('shows navigation items when open', async () => {
    renderPalette()
    fireEvent.keyDown(document, { key: 'k', ctrlKey: true })
    await waitFor(() => {
      expect(screen.getByText('Dashboard')).toBeInTheDocument()
      expect(screen.getByText('Settings')).toBeInTheDocument()
    })
  })

  it('filters stacks by search input', async () => {
    renderPalette()
    fireEvent.keyDown(document, { key: 'k', ctrlKey: true })
    await waitFor(() => expect(screen.getByRole('dialog')).toBeInTheDocument())

    const input = screen.getByPlaceholderText('Search stacks, navigate...')
    fireEvent.change(input, { target: { value: 'nginx' } })

    await waitFor(() => {
      expect(screen.getByText('nginx-proxy')).toBeInTheDocument()
    })
    // postgres-db should be filtered out by cmdk
    expect(screen.queryByText('postgres-db')).not.toBeInTheDocument()
  })

  it('navigates to a stack when selected', async () => {
    renderPalette()
    fireEvent.keyDown(document, { key: 'k', ctrlKey: true })
    await waitFor(() => expect(screen.getByText('nginx-proxy')).toBeInTheDocument())

    fireEvent.click(screen.getByText('nginx-proxy'))

    await waitFor(() => {
      expect(mockNavigate).toHaveBeenCalledWith('/stacks/stack-1')
    })
  })

  it('navigates to dashboard when Dashboard selected', async () => {
    renderPalette()
    fireEvent.keyDown(document, { key: 'k', ctrlKey: true })
    await waitFor(() => expect(screen.getByText('Dashboard')).toBeInTheDocument())

    fireEvent.click(screen.getByText('Dashboard'))

    await waitFor(() => {
      expect(mockNavigate).toHaveBeenCalledWith('/')
    })
  })

  it('navigates to settings when Settings selected', async () => {
    renderPalette()
    fireEvent.keyDown(document, { key: 'k', ctrlKey: true })
    await waitFor(() => expect(screen.getByText('Settings')).toBeInTheDocument())

    fireEvent.click(screen.getByText('Settings'))

    await waitFor(() => {
      expect(mockNavigate).toHaveBeenCalledWith('/settings')
    })
  })

  it('closes after a navigation action', async () => {
    renderPalette()
    fireEvent.keyDown(document, { key: 'k', ctrlKey: true })
    await waitFor(() => expect(screen.getByText('Dashboard')).toBeInTheDocument())

    fireEvent.click(screen.getByText('Dashboard'))

    await waitFor(() => {
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    })
  })

  it('ignores key presses without Ctrl or Meta', () => {
    renderPalette()
    fireEvent.keyDown(document, { key: 'k' })
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  // agent-os-kdqm: `data: stacks = []` dropped the error, so a failed stacks
  // load left the palette with no stacks and a search answered only
  // "No results found." — as if no such stack existed.
  it('first load fails: says the stacks could not be loaded, with Retry', async () => {
    vi.mocked(stacksApi.list).mockRejectedValueOnce(new Error('boom'))
    renderPalette()
    fireEvent.keyDown(document, { key: 'k', ctrlKey: true })

    expect(await screen.findByText('Could not load the stack list.')).toBeInTheDocument()
    // A search that matches nothing still carries the failure next to "No results found."
    fireEvent.change(screen.getByPlaceholderText('Search stacks, navigate...'), {
      target: { value: 'nginx' },
    })
    await waitFor(() => expect(screen.getByText('No results found.')).toBeInTheDocument())
    expect(screen.getByText('Could not load the stack list.')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(await screen.findByText('nginx-proxy')).toBeInTheDocument()
    expect(screen.queryByText('Could not load the stack list.')).not.toBeInTheDocument()
  })

  it('a healthy load shows no load error (control)', async () => {
    renderPalette()
    fireEvent.keyDown(document, { key: 'k', ctrlKey: true })
    expect(await screen.findByText('nginx-proxy')).toBeInTheDocument()
    expect(screen.queryByText('Could not load the stack list.')).not.toBeInTheDocument()
  })

  // agent-os-hzjh: the stack search had no first-load gate, so while the first
  // fetch was running (online) or paused (offline) a search answered
  // "No results found." about a list that had not been read yet.
  describe('first load of the stack list (agent-os-hzjh)', () => {
    const search = (value: string) =>
      fireEvent.change(screen.getByPlaceholderText('Search stacks, navigate...'), {
        target: { value },
      })

    afterEach(() => {
      onlineManager.setOnline(true)
    })

    it('online, first fetch pending: a search shows a loading row, not "No results found."', async () => {
      vi.mocked(stacksApi.list).mockReturnValueOnce(new Promise(() => {}))
      renderPalette()
      fireEvent.keyDown(document, { key: 'k', ctrlKey: true })
      await waitFor(() => expect(screen.getByRole('dialog')).toBeInTheDocument())

      search('nginx')

      expect(screen.queryByText('No results found.')).not.toBeInTheDocument()
      expect(await screen.findByRole('status')).toHaveTextContent('Loading stacks')
    })

    it('offline, first fetch paused: a search shows a loading row, not "No results found."', async () => {
      onlineManager.setOnline(false)
      renderPalette()
      fireEvent.keyDown(document, { key: 'k', ctrlKey: true })
      await waitFor(() => expect(screen.getByRole('dialog')).toBeInTheDocument())

      search('nginx')

      expect(screen.queryByText('No results found.')).not.toBeInTheDocument()
      expect(await screen.findByRole('status')).toHaveTextContent('Loading stacks')
    })

    it('a loaded empty list still says "No results found." and shows no loading row (control)', async () => {
      vi.mocked(stacksApi.list).mockResolvedValueOnce([])
      renderPalette()
      fireEvent.keyDown(document, { key: 'k', ctrlKey: true })
      await waitFor(() => expect(screen.getByRole('dialog')).toBeInTheDocument())

      search('nginx')

      expect(await screen.findByText('No results found.')).toBeInTheDocument()
      expect(screen.queryByRole('status')).not.toBeInTheDocument()
    })

    it('the loading row goes away once the list loads', async () => {
      renderPalette()
      fireEvent.keyDown(document, { key: 'k', ctrlKey: true })
      expect(await screen.findByText('nginx-proxy')).toBeInTheDocument()
      expect(screen.queryByRole('status')).not.toBeInTheDocument()
    })
  })
})

describe('CommandPalette help group', () => {
  const openAt = vi.fn()
  const openForPage = vi.fn()
  const openSearch = vi.fn()

  function renderAt(route: string) {
    const queryClient = createTestQueryClient()
    return render(
      <MemoryRouter initialEntries={[route]}>
        <QueryClientProvider client={queryClient}>
          <CommandPalette />
        </QueryClientProvider>
      </MemoryRouter>,
    )
  }

  const openPalette = async (route = '/settings/backup') => {
    renderAt(route)
    fireEvent.keyDown(document, { key: 'k', ctrlKey: true })
    await waitFor(() => expect(screen.getByRole('dialog')).toBeInTheDocument())
    return screen.getByPlaceholderText('Search stacks, navigate...')
  }

  const helpGroup = () => {
    const heading = screen.getByText('Help', { selector: '[cmdk-group-heading]' })
    return heading.closest('[cmdk-group]') as HTMLElement
  }
  const helpItems = () => Array.from(helpGroup().querySelectorAll<HTMLElement>('[cmdk-item]'))
  const libraryLoaded = () => waitFor(() => expect(screen.queryByText('Loading help…')).not.toBeInTheDocument())

  beforeEach(() => {
    mockNavigate.mockClear()
    openAt.mockClear()
    openForPage.mockClear()
    openSearch.mockClear()
    vi.mocked(loadHelpLibrary).mockReset()
    vi.mocked(loadHelpLibrary).mockImplementation(() => loader.actual!())
    useHelpStore.setState({
      open: false,
      followPage: true,
      current: HELP_INDEX,
      history: [],
      focusHeading: false,
      shortcutsOpen: false,
      openAt,
      openForPage,
      openSearch,
    })
  })

  it('typing retention shows a Help group item "Retention and pruning"', async () => {
    const input = await openPalette()
    fireEvent.change(input, { target: { value: 'retention' } })

    expect(await screen.findByText('Retention and pruning')).toBeInTheDocument()
    expect(helpGroup()).toContainElement(screen.getByText('Retention and pruning'))
  })

  it('Enter on it closes the palette and opens help/settings at retention-and-pruning', async () => {
    const input = await openPalette()
    fireEvent.change(input, { target: { value: 'retention' } })
    const item = (await screen.findByText('Retention and pruning')).closest('[cmdk-item]')
    // cmdk moves the highlight to the best match after it re-sorts; Enter acts on the highlight.
    await waitFor(() => expect(item).toHaveAttribute('aria-selected', 'true'))

    fireEvent.keyDown(input, { key: 'Enter' })

    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(openAt).toHaveBeenCalledTimes(1)
    expect(openAt).toHaveBeenCalledWith({ doc: 'help/settings', anchor: 'retention-and-pruning' })
  })

  it('"Open help for this page" is the first help item and opens the current page topic', async () => {
    await openPalette('/settings/backup')
    await libraryLoaded()

    const first = helpItems()[0]
    expect(first).toHaveTextContent('Open help for this page')
    fireEvent.click(first)

    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(openForPage).toHaveBeenCalledWith({ doc: 'help/settings', anchor: 'backup' })
    expect(openAt).not.toHaveBeenCalled()
  })

  it('on a page with no topic, "Open help for this page" opens the guide index (null)', async () => {
    await openPalette('/somewhere/else')
    await libraryLoaded()
    fireEvent.click(screen.getByText('Open help for this page'))
    await waitFor(() => expect(openForPage).toHaveBeenCalledWith(null))
  })

  it('"Search all help for \'retention\'" comes after the matching section and opens search mode with the query', async () => {
    const input = await openPalette()
    fireEvent.change(input, { target: { value: 'retention' } })
    const section = (await screen.findByText('Retention and pruning')).closest('[cmdk-item]')!

    const searchAll = screen.getByText("Search all help for 'retention'").closest('[cmdk-item]')!
    expect(section.compareDocumentPosition(searchAll) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    fireEvent.click(searchAll)

    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(openSearch).toHaveBeenCalledWith('retention')
  })

  it('shows no section items and no search-all item before anything is typed', async () => {
    await openPalette()
    await waitFor(() => expect(loadHelpLibrary).toHaveBeenCalled())
    expect(helpItems().map((i) => i.textContent)).toEqual(['Open help for this page'])
  })

  // The stack-list tests above type "nginx"; the docs mention nginx in body text only. No heading
  // matches, so the palette must keep saying "No results found." rather than offer a help search.
  it('keeps "No results found." for a query no stack, page or help heading matches', async () => {
    const input = await openPalette()
    await libraryLoaded()
    fireEvent.change(input, { target: { value: 'zzqxnomatch' } })

    expect(await screen.findByText('No results found.')).toBeInTheDocument()
    expect(screen.queryByText(/Search all help for/)).not.toBeInTheDocument()
    expect(helpGroup()).not.toBeVisible()
  })

  it('gives every help item its own cmdk value, even where two docs share a heading', async () => {
    const input = await openPalette()
    fireEvent.change(input, { target: { value: 'git' } })
    await screen.findByText("Search all help for 'git'")
    expect(helpItems().length).toBeGreaterThan(2)

    const values = helpItems().map((i) => i.getAttribute('data-value'))
    expect(values.length).toBeGreaterThan(2)
    expect(new Set(values).size).toBe(values.length)
  })

  it('shows a disabled "Loading help…" item while the library loads, then replaces it', async () => {
    let resolve!: (docs: HelpDoc[]) => void
    vi.mocked(loadHelpLibrary).mockImplementation(() => new Promise<HelpDoc[]>((r) => (resolve = r)))
    const input = await openPalette()
    fireEvent.change(input, { target: { value: 'retention' } })

    const loading = await screen.findByText('Loading help…')
    expect(loading.closest('[cmdk-item]')).toHaveAttribute('aria-disabled', 'true')
    expect(screen.queryByText('Retention and pruning')).not.toBeInTheDocument()

    resolve(await loader.actual!())
    expect(await screen.findByText('Retention and pruning')).toBeInTheDocument()
    expect(screen.queryByText('Loading help…')).not.toBeInTheDocument()
  })

  // cmdk leaves the highlight where it is when items arrive late. Nothing actionable is
  // force-mounted, so a query typed while the library loads must still end on the best match.
  it('a query typed before the library loads highlights the best match once it arrives', async () => {
    let resolve!: (docs: HelpDoc[]) => void
    vi.mocked(loadHelpLibrary).mockImplementation(() => new Promise<HelpDoc[]>((r) => (resolve = r)))
    const input = await openPalette()
    fireEvent.change(input, { target: { value: 'retention' } })
    await screen.findByText('Loading help…')

    resolve(await loader.actual!())
    const item = (await screen.findByText('Retention and pruning')).closest('[cmdk-item]')
    await waitFor(() => expect(item).toHaveAttribute('aria-selected', 'true'))
    fireEvent.keyDown(input, { key: 'Enter' })
    expect(openAt).toHaveBeenCalledWith({ doc: 'help/settings', anchor: 'retention-and-pruning' })
  })

  it('shows a disabled "Help could not be loaded" item when the loader rejects', async () => {
    vi.mocked(loadHelpLibrary).mockRejectedValue(new Error('no docs bundled'))
    const input = await openPalette()
    fireEvent.change(input, { target: { value: 'retention' } })

    const failed = await screen.findByText('Help could not be loaded')
    expect(failed.closest('[cmdk-item]')).toHaveAttribute('aria-disabled', 'true')
    expect(screen.queryByText('Loading help…')).not.toBeInTheDocument()
    expect(screen.queryByText('Retention and pruning')).not.toBeInTheDocument()
  })

  it('retries the load the next time the palette opens after a failure', async () => {
    vi.mocked(loadHelpLibrary).mockRejectedValueOnce(new Error('no docs bundled'))
    const input = await openPalette()
    await screen.findByText('Help could not be loaded')

    fireEvent.keyDown(document, { key: 'k', ctrlKey: true })
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    fireEvent.keyDown(document, { key: 'k', ctrlKey: true })
    const reopened = await screen.findByPlaceholderText('Search stacks, navigate...')
    expect(reopened).not.toBe(input)
    fireEvent.change(reopened, { target: { value: 'retention' } })

    expect(await screen.findByText('Retention and pruning')).toBeInTheDocument()
    expect(screen.queryByText('Help could not be loaded')).not.toBeInTheDocument()
  })

  it('clears the query when the palette closes', async () => {
    const input = await openPalette()
    fireEvent.change(input, { target: { value: 'nginx' } })
    fireEvent.keyDown(document, { key: 'k', ctrlKey: true })
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())

    fireEvent.keyDown(document, { key: 'k', ctrlKey: true })
    expect(await screen.findByPlaceholderText('Search stacks, navigate...')).toHaveValue('')
  })
})
