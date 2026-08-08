/**
 * StackUpdatesTab was at 0/48 statements (agent-os-c1gu). It is imported only by
 * StackDetail, which no test rendered.
 *
 * `@/lib/api` is mocked rather than `useUpdateHistory`, so the real query, the
 * real filter-to-query-key plumbing and the real useTextFilter all run. The
 * point of several assertions below is exactly that plumbing: which filters
 * reach the server, and which are applied in the browser.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithProviders } from '../../../test/utils'
import type { UpdateHistoryEntry } from '@/types'

const mockGetUpdateHistory = vi.fn()

vi.mock('@/lib/api', () => ({
  resourcesApi: {
    getUpdateHistory: (...args: unknown[]) => mockGetUpdateHistory(...args),
  },
}))

import { StackUpdatesTab } from '../StackUpdatesTab'

function entry(overrides: Partial<UpdateHistoryEntry> = {}): UpdateHistoryEntry {
  return {
    id: 'u1',
    containerId: 'c1',
    containerName: 'web',
    image: 'nginx:latest',
    oldDigest: 'sha256:aaaaaaaaaaaabbbbbbbbbbbb',
    newDigest: 'sha256:ccccccccccccdddddddddddd',
    status: 'success',
    trigger: 'manual',
    startedAt: new Date().toISOString(),
    durationMs: 4200,
    ...overrides,
  }
}

function page(entries: UpdateHistoryEntry[], extra: Record<string, unknown> = {}) {
  return { entries, total: entries.length, totalPages: 1, ...extra }
}

beforeEach(() => {
  vi.clearAllMocks()
  Element.prototype.hasPointerCapture = () => false
  Element.prototype.setPointerCapture = () => {}
  Element.prototype.releasePointerCapture = () => {}
})

describe('StackUpdatesTab — load states', () => {
  it('asks only for this stack, at the high page limit', async () => {
    mockGetUpdateHistory.mockResolvedValue(page([entry()]))
    renderWithProviders(<StackUpdatesTab stackId="stack-1" />)

    await waitFor(() =>
      expect(mockGetUpdateHistory).toHaveBeenCalledWith({
        page: 1,
        limit: 100,
        stackId: 'stack-1',
      }),
    )
  })

  it('offers a retry when the request fails', async () => {
    mockGetUpdateHistory.mockRejectedValue(new Error('boom'))
    const user = userEvent.setup()
    renderWithProviders(<StackUpdatesTab stackId="stack-1" />)

    // useUpdateHistory sets `retry: 1`, which overrides the test client's
    // `retry: false`, so the error state only lands after react-query's retry
    // delay — past the 1s default here.
    expect(
      await screen.findByText('Failed to Load Update History', {}, { timeout: 5000 }),
    ).toBeInTheDocument()

    mockGetUpdateHistory.mockResolvedValue(page([entry()]))
    await user.click(screen.getByRole('button', { name: /Retry/ }))

    expect(await screen.findByText('web')).toBeInTheDocument()
  })

  it('explains the empty state rather than showing a bare table', async () => {
    mockGetUpdateHistory.mockResolvedValue(page([]))
    renderWithProviders(<StackUpdatesTab stackId="stack-1" />)

    expect(await screen.findByText('No Update History')).toBeInTheDocument()
    expect(screen.queryByRole('table')).not.toBeInTheDocument()
  })
})

describe('StackUpdatesTab — the events table', () => {
  it('renders an event with its container, image, digests, status and trigger', async () => {
    mockGetUpdateHistory.mockResolvedValue(page([entry()]))
    renderWithProviders(<StackUpdatesTab stackId="stack-1" />)

    expect(await screen.findByText('web')).toBeInTheDocument()
    expect(screen.getByText('nginx:latest')).toBeInTheDocument()
    expect(screen.getByText('success')).toBeInTheDocument()
    expect(screen.getByText('manual')).toBeInTheDocument()
    // Digests are shown as 12 chars with the sha256: prefix dropped.
    expect(screen.getByText('aaaaaaaaaaaa → cccccccccccc')).toBeInTheDocument()
  })

  it('shows a dash for a digest it was not given', async () => {
    mockGetUpdateHistory.mockResolvedValue(
      page([entry({ oldDigest: undefined, newDigest: undefined })]),
    )
    renderWithProviders(<StackUpdatesTab stackId="stack-1" />)

    expect(await screen.findByText('- → -')).toBeInTheDocument()
  })

  it('shows a dash for a duration it was not given', async () => {
    mockGetUpdateHistory.mockResolvedValue(page([entry({ durationMs: undefined })]))
    renderWithProviders(<StackUpdatesTab stackId="stack-1" />)

    await screen.findByText('web')
    // A pending update has no duration yet; 0ms would be a lie.
    expect(screen.getByText('-')).toBeInTheDocument()
  })

  it('keeps a zero duration distinct from a missing one', async () => {
    mockGetUpdateHistory.mockResolvedValue(page([entry({ durationMs: 0 })]))
    renderWithProviders(<StackUpdatesTab stackId="stack-1" />)

    await screen.findByText('web')
    // `durationMs != null` rather than a truthiness check, so 0 formats.
    expect(screen.queryByText('-')).not.toBeInTheDocument()
  })

  it('truncates an over-long image reference', async () => {
    const long = 'registry.example.com/team/project/service:v1.2.3'
    mockGetUpdateHistory.mockResolvedValue(page([entry({ image: long })]))
    renderWithProviders(<StackUpdatesTab stackId="stack-1" />)

    expect(await screen.findByText(long.substring(0, 25) + '...')).toBeInTheDocument()
    expect(screen.queryByText(long)).not.toBeInTheDocument()
  })

  it('counts the events, pluralising correctly', async () => {
    mockGetUpdateHistory.mockResolvedValue(page([entry()]))
    const { unmount } = renderWithProviders(<StackUpdatesTab stackId="stack-1" />)
    expect(await screen.findByText('1 event')).toBeInTheDocument()
    unmount()

    mockGetUpdateHistory.mockResolvedValue(page([entry(), entry({ id: 'u2' })]))
    renderWithProviders(<StackUpdatesTab stackId="stack-2" />)
    expect(await screen.findByText('2 events')).toBeInTheDocument()
  })
})

describe('StackUpdatesTab — filtering', () => {
  it('filters in the browser by text, without re-querying', async () => {
    const user = userEvent.setup()
    mockGetUpdateHistory.mockResolvedValue(
      page([entry({ containerName: 'web' }), entry({ id: 'u2', containerName: 'database' })]),
    )
    renderWithProviders(<StackUpdatesTab stackId="stack-1" />)

    await screen.findByText('web')
    const callsBefore = mockGetUpdateHistory.mock.calls.length

    await user.type(screen.getByPlaceholderText('Filter events…'), 'datab')

    await waitFor(() => expect(screen.queryByText('web')).not.toBeInTheDocument())
    expect(screen.getByText('database')).toBeInTheDocument()
    // The high PAGE_LIMIT exists so this stays client-side.
    expect(mockGetUpdateHistory.mock.calls.length).toBe(callsBefore)
  })

  it('says so when the text filter matches nothing', async () => {
    const user = userEvent.setup()
    mockGetUpdateHistory.mockResolvedValue(page([entry({ containerName: 'web' })]))
    renderWithProviders(<StackUpdatesTab stackId="stack-1" />)

    await screen.findByText('web')
    await user.type(screen.getByPlaceholderText('Filter events…'), 'zzz')

    expect(await screen.findByText(/No events match/)).toBeInTheDocument()
    // The count switches to "of" form while filtering.
    expect(screen.getByText('0 of 1')).toBeInTheDocument()
  })

  it('sends the status filter to the server', async () => {
    const user = userEvent.setup()
    mockGetUpdateHistory.mockResolvedValue(page([entry()]))
    renderWithProviders(<StackUpdatesTab stackId="stack-1" />)

    await screen.findByText('web')
    // The two filter Selects carry no accessible name, so they are addressed by
    // position — first is Status, second is Trigger.
    await user.click(screen.getAllByRole('combobox')[0])
    await user.click(await screen.findByRole('option', { name: 'Failed' }))

    await waitFor(() =>
      expect(mockGetUpdateHistory).toHaveBeenCalledWith({
        page: 1,
        limit: 100,
        stackId: 'stack-1',
        status: 'failed',
      }),
    )
  })

  it('sends the trigger filter to the server', async () => {
    const user = userEvent.setup()
    mockGetUpdateHistory.mockResolvedValue(page([entry()]))
    renderWithProviders(<StackUpdatesTab stackId="stack-1" />)

    await screen.findByText('web')
    await user.click(screen.getAllByRole('combobox')[1])
    await user.click(await screen.findByRole('option', { name: 'Auto' }))

    await waitFor(() =>
      expect(mockGetUpdateHistory).toHaveBeenCalledWith({
        page: 1,
        limit: 100,
        stackId: 'stack-1',
        trigger: 'auto',
      }),
    )
  })

  it('omits a filter set back to "all" instead of sending the string "all"', async () => {
    const user = userEvent.setup()
    mockGetUpdateHistory.mockResolvedValue(page([entry()]))
    renderWithProviders(<StackUpdatesTab stackId="stack-1" />)

    await screen.findByText('web')
    await user.click(screen.getAllByRole('combobox')[0])
    await user.click(await screen.findByRole('option', { name: 'Failed' }))
    await waitFor(() =>
      expect(mockGetUpdateHistory).toHaveBeenCalledWith(
        expect.objectContaining({ status: 'failed' }),
      ),
    )

    await user.click(screen.getAllByRole('combobox')[0])
    await user.click(await screen.findByRole('option', { name: 'All Status' }))

    await waitFor(() => {
      const last = mockGetUpdateHistory.mock.calls.at(-1)![0] as Record<string, unknown>
      expect(last).not.toHaveProperty('status')
    })
  })
})

describe('StackUpdatesTab — pagination', () => {
  it('hides the pager when everything fits on one page', async () => {
    mockGetUpdateHistory.mockResolvedValue(page([entry()]))
    renderWithProviders(<StackUpdatesTab stackId="stack-1" />)

    await screen.findByText('web')
    expect(screen.queryByRole('button', { name: /Next/ })).not.toBeInTheDocument()
  })

  it('pages forward and back, and disables the ends', async () => {
    const user = userEvent.setup()
    mockGetUpdateHistory.mockResolvedValue(page([entry()], { total: 150, totalPages: 2 }))
    renderWithProviders(<StackUpdatesTab stackId="stack-1" />)

    await screen.findByText('web')
    expect(screen.getByText('Page 1 of 2')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Previous/ })).toBeDisabled()

    await user.click(screen.getByRole('button', { name: /Next/ }))

    await waitFor(() =>
      expect(mockGetUpdateHistory).toHaveBeenCalledWith(
        expect.objectContaining({ page: 2 }),
      ),
    )
    expect(await screen.findByText('Page 2 of 2')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Next/ })).toBeDisabled()

    await user.click(screen.getByRole('button', { name: /Previous/ }))
    expect(await screen.findByText('Page 1 of 2')).toBeInTheDocument()
  })

  it('returns to page 1 when a filter changes', async () => {
    const user = userEvent.setup()
    mockGetUpdateHistory.mockResolvedValue(page([entry()], { total: 150, totalPages: 2 }))
    renderWithProviders(<StackUpdatesTab stackId="stack-1" />)

    await screen.findByText('web')
    await user.click(screen.getByRole('button', { name: /Next/ }))
    expect(await screen.findByText('Page 2 of 2')).toBeInTheDocument()

    await user.click(screen.getAllByRole('combobox')[0])
    await user.click(await screen.findByRole('option', { name: 'Failed' }))

    // Staying on page 2 of a freshly-filtered result set would show an empty
    // table for no visible reason.
    await waitFor(() =>
      expect(mockGetUpdateHistory).toHaveBeenCalledWith(
        expect.objectContaining({ page: 1, status: 'failed' }),
      ),
    )
  })
})
