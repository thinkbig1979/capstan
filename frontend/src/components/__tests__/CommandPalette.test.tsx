import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider, onlineManager } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router'
import { CommandPalette } from '../CommandPalette'
import { stacksApi } from '@/lib/api'

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
