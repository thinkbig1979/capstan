import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { DirectoriesSettingsContent } from '../DirectoriesSettingsContent'
import { toast } from 'sonner'

/**
 * SettingsPage.test.tsx:46 replaces this panel with an empty div, so nothing
 * rendered it before (0/42 statements, agent-os-m1mu). This file renders the
 * real component with the API layer mocked, so the queries, the mutation and
 * the query-key invalidation all run.
 */

const mockGetConfig = vi.fn()
const mockGetScanDepth = vi.fn()
const mockUpdateScanDepth = vi.fn()
const mockDirectoryConfigUpdate = vi.fn()

vi.mock('@/lib/api', () => ({
  settingsApi: {
    getConfig: (...args: unknown[]) => mockGetConfig(...args),
    getScanDepth: (...args: unknown[]) => mockGetScanDepth(...args),
    updateScanDepth: (...args: unknown[]) => mockUpdateScanDepth(...args),
  },
  directoryConfigApi: {
    update: (...args: unknown[]) => mockDirectoryConfigUpdate(...args),
  },
}))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), info: vi.fn(), warning: vi.fn() },
}))

// Radix Select drives its popup from Pointer Events, which jsdom does not
// implement. These three stubs are the standard minimum to let it open.
beforeEach(() => {
  Element.prototype.hasPointerCapture = () => false
  Element.prototype.setPointerCapture = () => {}
  Element.prototype.releasePointerCapture = () => {}
})

function createWrapper() {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false, staleTime: 0 },
      mutations: { retry: false },
    },
  })
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  )
}

const renderPanel = () =>
  render(<DirectoriesSettingsContent />, { wrapper: createWrapper() })

const TWO_DIRS = ['/srv/stacks', '/mnt/extra/more-stacks']

beforeEach(() => {
  vi.clearAllMocks()
  mockGetConfig.mockResolvedValue({ stacksDir: '/srv/stacks', stacksDirectories: TWO_DIRS })
  mockGetScanDepth.mockResolvedValue({ scanDepth: 1 })
  mockUpdateScanDepth.mockResolvedValue({})
  mockDirectoryConfigUpdate.mockResolvedValue({})
})

describe('DirectoriesSettingsContent — directory list', () => {
  it('shows a spinner while either query is still loading', () => {
    mockGetScanDepth.mockReturnValue(new Promise(() => {}))
    renderPanel()

    expect(screen.queryByText('Monitored Directories')).not.toBeInTheDocument()
  })

  it('lists each directory by basename with the full path underneath', async () => {
    renderPanel()

    expect(await screen.findByText('stacks')).toBeInTheDocument()
    expect(screen.getByText('more-stacks')).toBeInTheDocument()
    expect(screen.getByText('/srv/stacks')).toBeInTheDocument()
    expect(screen.getByText('/mnt/extra/more-stacks')).toBeInTheDocument()
  })

  it('badges the first directory as the default', async () => {
    renderPanel()

    expect(await screen.findByText('Default')).toBeInTheDocument()
    // Exactly one — the badge marks allDirs[0], not every entry.
    expect(screen.getAllByText('Default')).toHaveLength(1)
  })

  it('says so when no directories are configured', async () => {
    mockGetConfig.mockResolvedValue({ stacksDir: '', stacksDirectories: [] })
    renderPanel()

    expect(await screen.findByText('No directories configured')).toBeInTheDocument()
  })

  it('tolerates a config with no stacksDirectories field at all', async () => {
    mockGetConfig.mockResolvedValue({ stacksDir: '/srv/stacks' })
    renderPanel()

    expect(await screen.findByText('No directories configured')).toBeInTheDocument()
  })
})

describe('DirectoriesSettingsContent — filtering', () => {
  it('offers a filter only when there is more than one directory', async () => {
    mockGetConfig.mockResolvedValue({ stacksDir: '/srv/stacks', stacksDirectories: ['/srv/stacks'] })
    renderPanel()

    await screen.findByText('Monitored Directories')
    expect(screen.queryByPlaceholderText('Filter directories…')).not.toBeInTheDocument()
  })

  it('filters on the basename as well as the full path', async () => {
    renderPanel()

    const filter = await screen.findByPlaceholderText('Filter directories…')
    fireEvent.change(filter, { target: { value: 'more' } })

    expect(screen.getByText('more-stacks')).toBeInTheDocument()
    expect(screen.queryByText('/srv/stacks')).not.toBeInTheDocument()

    fireEvent.change(filter, { target: { value: '/mnt' } })
    expect(screen.getByText('more-stacks')).toBeInTheDocument()
  })

  it('reports when the filter matches nothing, quoting the query', async () => {
    renderPanel()

    fireEvent.change(await screen.findByPlaceholderText('Filter directories…'), {
      target: { value: 'zzz' },
    })

    expect(screen.getByText('No directories match "zzz".')).toBeInTheDocument()
  })
})

describe('DirectoriesSettingsContent — scan depth', () => {
  it('shows the depth the server reports', async () => {
    mockGetScanDepth.mockResolvedValue({ scanDepth: 3 })
    renderPanel()

    expect(await screen.findByText('3 levels deep')).toBeInTheDocument()
  })

  it('disables Save Scan Depth until the value actually changes', async () => {
    renderPanel()

    expect(await screen.findByRole('button', { name: 'Save Scan Depth' })).toBeDisabled()
  })

  it('saves a changed depth and confirms with the rescan reminder', async () => {
    const user = userEvent.setup()
    renderPanel()

    await user.click(await screen.findByRole('combobox', { name: 'Directory Recursion Depth' }))
    await user.click(await screen.findByRole('option', { name: '4 levels deep' }))

    const save = screen.getByRole('button', { name: 'Save Scan Depth' })
    await waitFor(() => expect(save).toBeEnabled())
    await user.click(save)

    await waitFor(() => expect(mockUpdateScanDepth).toHaveBeenCalledWith(4))
    expect(toast.success).toHaveBeenCalledWith(
      'Scan depth updated. Rescan directories to discover nested stacks.',
    )
  })

  it('reports a failed depth save', async () => {
    mockUpdateScanDepth.mockRejectedValue(new Error('boom'))
    const user = userEvent.setup()
    renderPanel()

    await user.click(await screen.findByRole('combobox', { name: 'Directory Recursion Depth' }))
    await user.click(await screen.findByRole('option', { name: '2 levels deep' }))
    await user.click(screen.getByRole('button', { name: 'Save Scan Depth' }))

    await waitFor(() => expect(toast.error).toHaveBeenCalledWith('Failed to update scan depth'))
  })
})

describe('DirectoriesSettingsContent — default directory', () => {
  it('hides the whole section when only one directory is configured', async () => {
    mockGetConfig.mockResolvedValue({ stacksDir: '/srv/stacks', stacksDirectories: ['/srv/stacks'] })
    renderPanel()

    await screen.findByText('Monitored Directories')
    expect(screen.queryByText('Default Stack Directory')).not.toBeInTheDocument()
  })

  it('disables the save button while the selection matches the server', async () => {
    renderPanel()

    expect(await screen.findByRole('button', { name: 'Save Default Directory' })).toBeDisabled()
  })

  it('saves a newly chosen default directory', async () => {
    const user = userEvent.setup()
    renderPanel()

    await user.click(
      await screen.findByRole('combobox', { name: 'Default Directory for New Stacks' }),
    )
    await user.click(await screen.findByRole('option', { name: /more-stacks/ }))

    const save = screen.getByRole('button', { name: 'Save Default Directory' })
    await waitFor(() => expect(save).toBeEnabled())
    await user.click(save)

    await waitFor(() =>
      expect(mockDirectoryConfigUpdate).toHaveBeenCalledWith({
        defaultDir: '/mnt/extra/more-stacks',
      }),
    )
    await waitFor(() =>
      expect(toast.success).toHaveBeenCalledWith('Default directory updated'),
    )
  })

  it('reports a failed default-directory save', async () => {
    mockDirectoryConfigUpdate.mockRejectedValue(new Error('boom'))
    const user = userEvent.setup()
    renderPanel()

    await user.click(
      await screen.findByRole('combobox', { name: 'Default Directory for New Stacks' }),
    )
    await user.click(await screen.findByRole('option', { name: /more-stacks/ }))
    await user.click(screen.getByRole('button', { name: 'Save Default Directory' }))

    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith('Failed to update default directory'),
    )
  })
})
