import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { UpdateScheduleContent } from '../UpdateScheduleContent'
import { toast } from 'sonner'

/**
 * SettingsPage.test.tsx:40 replaces this panel with an empty div, so nothing
 * rendered it before (0/42 statements, agent-os-m1mu). Rendered for real here
 * with the API layer mocked, so useUpdateSettings, the mutation and its
 * invalidation all run.
 */

const mockGetUpdates = vi.fn()
const mockUpdateUpdates = vi.fn()

vi.mock('@/lib/api', () => ({
  settingsApi: {
    getUpdates: (...args: unknown[]) => mockGetUpdates(...args),
    updateUpdates: (...args: unknown[]) => mockUpdateUpdates(...args),
  },
}))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), info: vi.fn(), warning: vi.fn() },
}))

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

const renderPanel = () => render(<UpdateScheduleContent />, { wrapper: createWrapper() })

function makeSettings(overrides: Record<string, unknown> = {}) {
  return {
    scanIntervalMinutes: 0,
    lastScanAt: null,
    lastScanError: null,
    globalAutoUpdate: false,
    autoUpdateStats: { enabledContainers: 0, updatesLast7Days: 0, updatesLast30Days: 0 },
    ...overrides,
  }
}

beforeEach(() => {
  vi.clearAllMocks()
  mockGetUpdates.mockResolvedValue(makeSettings())
  mockUpdateUpdates.mockResolvedValue({})
})

describe('UpdateScheduleContent — scan interval', () => {
  it('shows a loading line until the settings arrive', () => {
    mockGetUpdates.mockReturnValue(new Promise(() => {}))
    renderPanel()

    expect(screen.getByText('Loading update settings...')).toBeInTheDocument()
  })

  it('shows a preset interval by its label', async () => {
    mockGetUpdates.mockResolvedValue(makeSettings({ scanIntervalMinutes: 360 }))
    renderPanel()

    expect(await screen.findByText('Every 6 hours')).toBeInTheDocument()
    expect(screen.queryByLabelText('Custom interval (minutes)')).not.toBeInTheDocument()
  })

  it('falls back to Custom, prefilled, for an interval that matches no preset', async () => {
    mockGetUpdates.mockResolvedValue(makeSettings({ scanIntervalMinutes: 45 }))
    renderPanel()

    expect(await screen.findByText('Custom')).toBeInTheDocument()
    expect(screen.getByLabelText('Custom interval (minutes)')).toHaveValue(45)
  })

  it('saves immediately when a preset is picked', async () => {
    const user = userEvent.setup()
    renderPanel()

    await user.click(await screen.findByRole('combobox', { name: 'Scan Interval' }))
    await user.click(await screen.findByRole('option', { name: 'Every hour' }))

    await waitFor(() =>
      expect(mockUpdateUpdates).toHaveBeenCalledWith({
        scanIntervalMinutes: 60,
        globalAutoUpdate: false,
      }),
    )
    expect(toast.success).toHaveBeenCalledWith('Settings saved')
  })

  it('does not save when Custom is picked — it waits for a value', async () => {
    const user = userEvent.setup()
    renderPanel()

    await user.click(await screen.findByRole('combobox', { name: 'Scan Interval' }))
    await user.click(await screen.findByRole('option', { name: 'Custom' }))

    expect(await screen.findByLabelText('Custom interval (minutes)')).toBeInTheDocument()
    expect(mockUpdateUpdates).not.toHaveBeenCalled()
  })

  it('saves a custom interval on blur', async () => {
    mockGetUpdates.mockResolvedValue(makeSettings({ scanIntervalMinutes: 45 }))
    renderPanel()

    const input = await screen.findByLabelText('Custom interval (minutes)')
    fireEvent.change(input, { target: { value: '90' } })
    fireEvent.blur(input)

    await waitFor(() =>
      expect(mockUpdateUpdates).toHaveBeenCalledWith({
        scanIntervalMinutes: 90,
        globalAutoUpdate: false,
      }),
    )
  })

  it('refuses a custom interval below the 15-minute floor', async () => {
    mockGetUpdates.mockResolvedValue(makeSettings({ scanIntervalMinutes: 45 }))
    renderPanel()

    const input = await screen.findByLabelText('Custom interval (minutes)')
    fireEvent.change(input, { target: { value: '5' } })
    fireEvent.blur(input)

    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith('Custom interval must be at least 15 minutes'),
    )
    expect(mockUpdateUpdates).not.toHaveBeenCalled()
  })

  it('allows 0 — that is "disabled", not "below the floor"', async () => {
    mockGetUpdates.mockResolvedValue(makeSettings({ scanIntervalMinutes: 45 }))
    renderPanel()

    const input = await screen.findByLabelText('Custom interval (minutes)')
    fireEvent.change(input, { target: { value: '0' } })
    fireEvent.blur(input)

    await waitFor(() =>
      expect(mockUpdateUpdates).toHaveBeenCalledWith({
        scanIntervalMinutes: 0,
        globalAutoUpdate: false,
      }),
    )
    expect(toast.error).not.toHaveBeenCalled()
  })

  it('reports a failed save', async () => {
    mockUpdateUpdates.mockRejectedValue(new Error('boom'))
    const user = userEvent.setup()
    renderPanel()

    await user.click(await screen.findByRole('combobox', { name: 'Scan Interval' }))
    await user.click(await screen.findByRole('option', { name: 'Every hour' }))

    await waitFor(() => expect(toast.error).toHaveBeenCalledWith('Failed to save settings'))
  })
})

describe('UpdateScheduleContent — last scan', () => {
  it('says Never when there has been no scan', async () => {
    renderPanel()

    expect(await screen.findByText('Last scanned: Never')).toBeInTheDocument()
  })

  it('shows the last scan time when there has been one', async () => {
    mockGetUpdates.mockResolvedValue(makeSettings({ lastScanAt: '2026-08-08T10:00:00Z' }))
    renderPanel()

    expect(await screen.findByText(/^Last scanned: /)).toBeInTheDocument()
    expect(screen.queryByText('Last scanned: Never')).not.toBeInTheDocument()
  })

  it('surfaces the last scan error when the server reports one', async () => {
    mockGetUpdates.mockResolvedValue(makeSettings({ lastScanError: 'registry unreachable' }))
    renderPanel()

    expect(await screen.findByText('Last scan error: registry unreachable')).toBeInTheDocument()
  })
})

describe('UpdateScheduleContent — auto-update', () => {
  it('warns that per-container toggles are locked while the master switch is off', async () => {
    renderPanel()

    expect(await screen.findByText(/Auto-update is off/)).toBeInTheDocument()
    expect(screen.getByRole('switch', { name: 'Enable Auto-Update' })).not.toBeChecked()
  })

  it('swaps to the interruption warning once it is on', async () => {
    mockGetUpdates.mockResolvedValue(makeSettings({ globalAutoUpdate: true }))
    renderPanel()

    expect(await screen.findByText(/brief service interruption/)).toBeInTheDocument()
    expect(screen.queryByText(/Auto-update is off/)).not.toBeInTheDocument()
    expect(screen.getByRole('switch', { name: 'Enable Auto-Update' })).toBeChecked()
  })

  it('saves the master switch immediately, keeping the current interval', async () => {
    mockGetUpdates.mockResolvedValue(makeSettings({ scanIntervalMinutes: 720 }))
    renderPanel()

    fireEvent.click(await screen.findByRole('switch', { name: 'Enable Auto-Update' }))

    await waitFor(() =>
      expect(mockUpdateUpdates).toHaveBeenCalledWith({
        scanIntervalMinutes: 720,
        globalAutoUpdate: true,
      }),
    )
  })
})

describe('UpdateScheduleContent — statistics', () => {
  it('uses singular wording for a count of one', async () => {
    mockGetUpdates.mockResolvedValue(
      makeSettings({
        autoUpdateStats: { enabledContainers: 1, updatesLast7Days: 1, updatesLast30Days: 3 },
      }),
    )
    renderPanel()

    expect(await screen.findByText('1 container with auto-update enabled')).toBeInTheDocument()
    expect(
      screen.getByText('1 update in the last 7 days, 3 in the last 30 days'),
    ).toBeInTheDocument()
  })

  it('uses plural wording otherwise, including for zero', async () => {
    mockGetUpdates.mockResolvedValue(
      makeSettings({
        autoUpdateStats: { enabledContainers: 0, updatesLast7Days: 0, updatesLast30Days: 0 },
      }),
    )
    renderPanel()

    expect(await screen.findByText('0 containers with auto-update enabled')).toBeInTheDocument()
    expect(
      screen.getByText('0 updates in the last 7 days, 0 in the last 30 days'),
    ).toBeInTheDocument()
  })

  it('omits the statistics block when the server sends none', async () => {
    mockGetUpdates.mockResolvedValue(makeSettings({ autoUpdateStats: undefined }))
    renderPanel()

    await screen.findByText('Auto-Update')
    expect(screen.queryByText('Statistics')).not.toBeInTheDocument()
  })
})
