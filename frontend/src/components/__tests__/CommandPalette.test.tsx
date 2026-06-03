import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { CommandPalette } from '../CommandPalette'

// Mock react-router-dom's useNavigate
const mockNavigate = vi.fn()
vi.mock('react-router-dom', async (importOriginal) => {
  const actual = await importOriginal<typeof import('react-router-dom')>()
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
})
