import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor, within } from '@testing-library/react'
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

// Same render, but hands back the QueryClient so a test can drive a REFETCH.
// Needed because the component exposes refetch only from inside the branch
// under test, which would make the assertion circular.
function renderPanelWithClient() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: 0 }, mutations: { retry: false } },
  })
  const view = render(
    <QueryClientProvider client={queryClient}>
      <UpdateScheduleContent />
    </QueryClientProvider>,
  )
  return { ...view, queryClient }
}

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

/**
 * Apply-time schedule. The three apply fields are optional on the wire, so the
 * assertions below always come in pairs: the case that must send them, and the
 * case that must not. A one-sided check here would pass for a screen that sends
 * nothing at all.
 */
const scheduledSettings = (overrides: Record<string, unknown> = {}) =>
  makeSettings({
    scanIntervalMinutes: 720,
    globalAutoUpdate: true,
    applyMode: 'scheduled',
    applyTime: '02:30',
    applyDays: [1, 3],
    serverTimezone: 'UTC',
    serverTimeOffset: '+00:00',
    ...overrides,
  })

describe('UpdateScheduleContent — apply schedule', () => {
  it('renders the schedule fields only while auto-update is on', async () => {
    mockGetUpdates.mockResolvedValue(makeSettings({ globalAutoUpdate: false }))
    const off = renderPanel()

    await screen.findByText(/Auto-update is off/)
    expect(screen.queryByRole('radio', { name: 'Every so often' })).not.toBeInTheDocument()
    expect(screen.queryByRole('radio', { name: 'At a set time' })).not.toBeInTheDocument()
    off.unmount()

    mockGetUpdates.mockResolvedValue(makeSettings({ globalAutoUpdate: true }))
    renderPanel()

    expect(await screen.findByRole('radio', { name: 'Every so often' })).toBeInTheDocument()
    expect(screen.getByRole('radio', { name: 'At a set time' })).toBeInTheDocument()
  })

  it('hydrates the mode, time and days the server reports', async () => {
    mockGetUpdates.mockResolvedValue(scheduledSettings())
    renderPanel()

    expect(await screen.findByRole('radio', { name: 'At a set time' })).toHaveAttribute(
      'aria-checked',
      'true',
    )
    expect(screen.getByLabelText('Time of day')).toHaveValue('02:30')
    const days = within(screen.getByRole('group', { name: 'Days' }))
    expect(days.getByRole('button', { name: 'Monday' })).toHaveAttribute('aria-pressed', 'true')
    expect(days.getByRole('button', { name: 'Sunday' })).toHaveAttribute('aria-pressed', 'false')
  })

  it('sends applyMode, applyTime and applyDays when the mode is switched to scheduled', async () => {
    mockGetUpdates.mockResolvedValue(
      scheduledSettings({ applyMode: 'immediate', applyTime: '04:00', applyDays: [2, 5] }),
    )
    renderPanel()

    fireEvent.click(await screen.findByRole('radio', { name: 'At a set time' }))

    await waitFor(() =>
      expect(mockUpdateUpdates).toHaveBeenCalledWith({
        scanIntervalMinutes: 720,
        globalAutoUpdate: true,
        applyMode: 'scheduled',
        applyTime: '04:00',
        applyDays: [2, 5],
      }),
    )
  })

  it('sends the new time, and the new days sorted ascending', async () => {
    mockGetUpdates.mockResolvedValue(scheduledSettings())
    renderPanel()

    fireEvent.change(await screen.findByLabelText('Time of day'), { target: { value: '05:45' } })

    await waitFor(() =>
      expect(mockUpdateUpdates).toHaveBeenCalledWith({
        scanIntervalMinutes: 720,
        globalAutoUpdate: true,
        applyMode: 'scheduled',
        applyTime: '05:45',
        applyDays: [1, 3],
      }),
    )

    const days = within(screen.getByRole('group', { name: 'Days' }))
    fireEvent.click(days.getByRole('button', { name: 'Sunday' }))

    await waitFor(() =>
      expect(mockUpdateUpdates).toHaveBeenLastCalledWith({
        scanIntervalMinutes: 720,
        globalAutoUpdate: true,
        applyMode: 'scheduled',
        applyTime: '05:45',
        applyDays: [0, 1, 3],
      }),
    )
  })

  it('leaves the apply fields out of a save the admin never touched', async () => {
    mockGetUpdates.mockResolvedValue(scheduledSettings())
    const user = userEvent.setup()
    renderPanel()

    await user.click(await screen.findByRole('combobox', { name: 'Scan Interval' }))
    await user.click(await screen.findByRole('option', { name: 'Every hour' }))

    await waitFor(() =>
      expect(mockUpdateUpdates).toHaveBeenCalledWith({
        scanIntervalMinutes: 60,
        globalAutoUpdate: true,
      }),
    )
  })

  it('says when the next scheduled update is due, and stays quiet when none is', async () => {
    mockGetUpdates.mockResolvedValue(scheduledSettings({ nextApplyAt: '2026-09-02T02:30:00Z' }))
    const withNext = renderPanel()

    expect(await screen.findByText(/^Next scheduled update: /)).toBeInTheDocument()
    withNext.unmount()

    mockGetUpdates.mockResolvedValue(scheduledSettings())
    renderPanel()

    await screen.findByRole('radio', { name: 'At a set time' })
    expect(screen.queryByText(/^Next scheduled update: /)).not.toBeInTheDocument()
  })

  it('warns about the scheduled time in scheduled mode, and about scans in immediate mode', async () => {
    mockGetUpdates.mockResolvedValue(scheduledSettings())
    const scheduled = renderPanel()

    expect(await screen.findByText(/applied at the scheduled time/)).toBeInTheDocument()
    scheduled.unmount()

    mockGetUpdates.mockResolvedValue(scheduledSettings({ applyMode: 'immediate' }))
    renderPanel()

    expect(await screen.findByText(/detected during scans/)).toBeInTheDocument()
    expect(screen.queryByText(/applied at the scheduled time/)).not.toBeInTheDocument()
  })

  it('reports the server clock, which is UTC on a default install', async () => {
    mockGetUpdates.mockResolvedValue(scheduledSettings())
    renderPanel()

    expect(
      await screen.findByText(/Times are in UTC \(\+00:00\), the server's own clock\./),
    ).toBeInTheDocument()
  })

  it('warns that a scheduled apply cannot run while scanning is disabled, and stays quiet once an interval is set', async () => {
    mockGetUpdates.mockResolvedValue(scheduledSettings({ scanIntervalMinutes: 0 }))
    const disabled = renderPanel()

    expect(
      await screen.findByText(/Scheduled updates can't run while scanning is disabled/),
    ).toBeInTheDocument()
    disabled.unmount()

    mockGetUpdates.mockResolvedValue(scheduledSettings())
    renderPanel()

    await screen.findByRole('radio', { name: 'At a set time' })
    expect(
      screen.queryByText(/Scheduled updates can't run while scanning is disabled/),
    ).not.toBeInTheDocument()
  })
})

/**
 * agent-os-zlw0. This panel's onError took NO parameter, so the sentence the
 * server sent could not be read even in principle — there was no unused
 * variable and no type error for anyone to notice. UpdateUpdateSettings
 * (backend/internal/handlers/settings.go:647) answers with FIVE distinct
 * VALIDATION_ERROR 400s — bad body, scan interval below 15, invalid apply
 * mode, unparseable apply time, bad weekday list — and the operator was told
 * only "Failed to save settings" for all five.
 */
describe('UpdateScheduleContent — why the save failed', () => {
  it('names what the server rejected', async () => {
    mockUpdateUpdates.mockRejectedValue({
      error: 'Bad Request',
      code: 'VALIDATION_ERROR',
      message: 'Scan interval must be 0 (disabled) or at least 15 minutes',
      status: 400,
    })
    const user = userEvent.setup()
    renderPanel()

    await user.click(await screen.findByRole('combobox', { name: 'Scan Interval' }))
    await user.click(await screen.findByRole('option', { name: 'Every hour' }))

    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith('Failed to save settings', {
        description: 'Scan interval must be 0 (disabled) or at least 15 minutes',
      }),
    )
  })

  // The MUST-PASS side, on the same instrument. A network failure carries a
  // non-empty axios `message` ("Network Error", lib/api.ts:131) that never came
  // from the backend, so a helper keyed on "did something carry a message"
  // would present an axios internal string as though the server had said it.
  // Asserted on the second argument rather than on the call arity, so this arm
  // is a guard on the network case and not an echo of the signature change.
  it('shows the generic sentence alone when the failure carries no server cause', async () => {
    mockUpdateUpdates.mockRejectedValue({
      error: 'Unknown error',
      code: 'ERR_NETWORK',
      message: 'Network Error',
    })
    const user = userEvent.setup()
    renderPanel()

    await user.click(await screen.findByRole('combobox', { name: 'Scan Interval' }))
    await user.click(await screen.findByRole('option', { name: 'Every hour' }))

    await waitFor(() => expect(toast.error).toHaveBeenCalled())
    const call = vi.mocked(toast.error).mock.calls.find((c) => c[0] === 'Failed to save settings')
    expect(call).toBeDefined()
    expect(call?.[1]).toBeUndefined()
  })
})

// ─── agent-os-fxhl: a FAILED update-settings query ──────────────────────────
//
// The component destructured `data` and `isLoading` but never `isError`, and
// the only early return was the loading one. So a failed GET fell through to
//   effectiveAutoUpdate = initialized ? globalAutoUpdate : (settings?.globalAutoUpdate ?? false)
// where `?? false` turned "not known" into "deliberately off".
//
// That is not only a mis-render. save() builds its payload from
// effectiveAutoUpdate, effectiveScanMinutes, effectiveApplyMode,
// effectiveApplyTime AND effectiveApplyDays — every one of them a `?? <default>`
// over `settings` — so any toggle the operator touched would write back FIVE
// fields that were never read, silently replacing the stored schedule with this
// screen's defaults.
//
// DECISION (recorded on the bead before implementing): an ERROR EARLY RETURN
// that REPLACES the form, carrying the cause and a Retry wired to refetch. Not
// a banner over a live form (the switch would still read `?? false` and still
// submit) and not a disabled form (same false values, greyed — "not editable
// now" is a different statement from "this was never read").
describe('UpdateScheduleContent — a failed settings query', () => {
  it('does not present the master switch as deliberately off', async () => {
    mockGetUpdates.mockRejectedValue({ status: 503, message: 'Docker is not running' })
    renderPanel()

    await screen.findByText(/Could not load update settings/i)
    // The switch is not rendered at all, so there is no position to misread.
    expect(screen.queryByLabelText('Enable Auto-Update')).not.toBeInTheDocument()
    expect(screen.queryByRole('switch')).not.toBeInTheDocument()
  })

  it('carries the backend cause and offers a Retry', async () => {
    mockGetUpdates.mockRejectedValue({ status: 503, message: 'Docker is not running' })
    renderPanel()

    await screen.findByText(/Could not load update settings/i)
    // classifyError's 5xx arm keeps the status, which is itself diagnostic.
    expect(screen.getByText('503: Docker is not running')).toBeInTheDocument()

    // Retry must actually re-run the query, not merely exist.
    expect(mockGetUpdates).toHaveBeenCalledTimes(1)
    mockGetUpdates.mockResolvedValue(makeSettings({ globalAutoUpdate: true }))
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))

    await waitFor(() => expect(mockGetUpdates).toHaveBeenCalledTimes(2))
    expect(await screen.findByLabelText('Enable Auto-Update')).toBeInTheDocument()
  })

  // AC4. Three consumers read effectiveAutoUpdate on the RENDER path and they
  // render OPPOSITE content off the same value, so a test that asserts only the
  // switch leaves both branches unpinned.
  it('renders NEITHER dependent branch in the failed state', async () => {
    mockGetUpdates.mockRejectedValue({ status: 503, message: 'Docker is not running' })
    renderPanel()

    await screen.findByText(/Could not load update settings/i)
    // The `!effectiveAutoUpdate` branch — the one a `?? false` used to select.
    expect(screen.queryByText(/Auto-update is off\./)).not.toBeInTheDocument()
    // The `effectiveAutoUpdate` branch — the schedule fields. Absent in the
    // RED state too (because `?? false` selected the other branch), so the
    // arm that gives this assertion meaning is the RESOLVED-true control
    // below, which requires the same text to be PRESENT.
    expect(screen.queryByText('Schedule')).not.toBeInTheDocument()
    // And the form itself: the scan-interval select IS rendered in the RED
    // state, so this half discriminates on its own.
    expect(screen.queryByRole('combobox', { name: 'Scan Interval' })).not.toBeInTheDocument()
  })

  // AC4, the FOURTH consumer, on the WRITE path: `const autoUpdate =
  // updates.globalAutoUpdate ?? effectiveAutoUpdate` inside save(). The bead's
  // own AC pins only the two render branches; this is the one that submits.
  it('makes the five-field write unreachable in the failed state', async () => {
    mockGetUpdates.mockRejectedValue({ status: 503, message: 'Docker is not running' })
    renderPanel()

    await screen.findByText(/Could not load update settings/i)
    // Nothing that calls save() is on screen, so no payload built from five
    // never-read defaults can leave this component.
    expect(screen.queryByRole('combobox', { name: 'Scan Interval' })).not.toBeInTheDocument()
    expect(mockUpdateUpdates).not.toHaveBeenCalled()
  })

  // TWO-SIDED on the same instrument. A fix that turns every falsy value into
  // an error state is worse than the bug, so the genuinely-off case must still
  // present as off.
  it('still presents a RESOLVED globalAutoUpdate:false as off', async () => {
    mockGetUpdates.mockResolvedValue(makeSettings({ globalAutoUpdate: false }))
    renderPanel()

    const toggle = await screen.findByLabelText('Enable Auto-Update')
    expect(toggle).toHaveAttribute('aria-checked', 'false')
    expect(screen.getByText(/Auto-update is off\./)).toBeInTheDocument()
    expect(screen.queryByText(/Could not load update settings/i)).not.toBeInTheDocument()
  })

  // agent-os-5g8a, defect found in review, and the four arms above are BLIND to
  // it: every one of them rejects the FIRST fetch, so `settings` is undefined in
  // all four and they stay green whether the guard reads `isError` or
  // `isError && !settings`.
  //
  // TanStack sets status 'error' when a REFETCH fails on a query that ALREADY
  // HAS DATA -- that is why the library ships isLoadingError (error, no data)
  // alongside isRefetchError (error, data present), with isError true for both.
  // This repo makes that path routine rather than theoretical: query-client.ts
  // sets staleTime 30_000 and refetchOnWindowFocus true, and its retry predicate
  // is isAutoRetryable, which is FALSE for a 500. So an operator loads Settings,
  // tabs away for half a minute, tabs back, the focus refetch 500s -- and a bare
  // isError guard would replace a fully populated, CORRECT form with an error
  // box. That is strictly worse than the bug fxhl was filed for, in the opposite
  // direction: before this change that operator kept a working form.
  it('keeps the populated form when a REFETCH fails', async () => {
    mockGetUpdates.mockResolvedValue(makeSettings({ globalAutoUpdate: true }))
    const { queryClient } = renderPanelWithClient()

    const toggle = await screen.findByLabelText('Enable Auto-Update')
    expect(toggle).toHaveAttribute('aria-checked', 'true')

    mockGetUpdates.mockRejectedValue({ status: 500, message: 'boom' })
    await waitFor(() => expect(mockGetUpdates).toHaveBeenCalledTimes(1))
    await queryClient.refetchQueries({ queryKey: ['settings', 'updates'] })

    // The query IS in the error state now -- that is the point of the arm.
    await waitFor(() =>
      expect(queryClient.getQueryState(['settings', 'updates'])?.status).toBe('error'),
    )
    // ...and the real values it already has are still on screen.
    expect(screen.queryByText(/Could not load update settings/i)).not.toBeInTheDocument()
    expect(screen.getByLabelText('Enable Auto-Update')).toHaveAttribute('aria-checked', 'true')
    expect(screen.getByRole('combobox', { name: 'Scan Interval' })).toBeInTheDocument()
  })

  it('still presents a RESOLVED globalAutoUpdate:true as on', async () => {
    mockGetUpdates.mockResolvedValue(makeSettings({ globalAutoUpdate: true }))
    renderPanel()

    const toggle = await screen.findByLabelText('Enable Auto-Update')
    expect(toggle).toHaveAttribute('aria-checked', 'true')
    expect(screen.queryByText(/Auto-update is off\./)).not.toBeInTheDocument()
    // The positive branch's own text, which the failed-state arm requires to
    // be absent. Without this the two would agree for the wrong reason.
    expect(screen.getByText('Schedule')).toBeInTheDocument()
  })
})
