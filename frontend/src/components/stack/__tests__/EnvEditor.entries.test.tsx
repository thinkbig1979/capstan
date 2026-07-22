/**
 * Coverage for EnvEditor's entry-level table behaviours that the original
 * regression suite (EnvEditor.test.tsx) didn't exercise directly: add/delete,
 * key-driven sensitive auto-detection, comment-row rendering, the disabled
 * "Visible" checkbox for sensitive-pattern keys, the search filter, the
 * error/retry path, and the exact payload shape posted on table save.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, waitFor, fireEvent } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithProviders } from '../../../test/utils'

// ─── Mocks ────────────────────────────────────────────────────────────────────

const mockGetEnv = vi.fn()
const mockUpdateEnv = vi.fn()
const mockCreateEnv = vi.fn()

vi.mock('@/lib/api', () => ({
  stacksApi: {
    getEnv: (...args: unknown[]) => mockGetEnv(...args),
    updateEnv: (...args: unknown[]) => mockUpdateEnv(...args),
    createEnv: (...args: unknown[]) => mockCreateEnv(...args),
    updateComposeAndEnv: vi.fn(),
  },
  apiClient: {
    get: vi.fn(),
    put: vi.fn(),
    post: vi.fn(),
  },
}))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), warning: vi.fn(), info: vi.fn() },
}))

import { EnvEditor } from '../EnvEditor'

// ─── Helpers ─────────────────────────────────────────────────────────────────

const twoEntryEnvData = {
  filename: '.env',
  entries: [
    { key: 'PORT', value: '8080', sensitive: false, comment: false, line: 1 },
    { key: 'HOST', value: 'localhost', sensitive: false, comment: false, line: 2 },
  ],
  raw: 'PORT=8080\nHOST=localhost\n',
}

describe('EnvEditor entry editing', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('Add Entry appends a blank row and enables Save', async () => {
    const user = userEvent.setup()
    mockGetEnv.mockResolvedValue(twoEntryEnvData)
    renderWithProviders(<EnvEditor stackId="test-stack" />)

    await waitFor(() => {
      expect(screen.getAllByLabelText(/Environment variable key/).length).toBe(4) // 2 entries × desktop+mobile
    })
    expect(screen.getByText('Save')).toBeDisabled()

    await user.click(screen.getByText('Add Entry'))

    await waitFor(() => {
      expect(screen.getAllByLabelText(/Environment variable key/).length).toBe(6) // 3 entries × desktop+mobile
    })
    expect(screen.getByText('Save')).not.toBeDisabled()
  })

  it('Delete removes the targeted row from both desktop and mobile renderings', async () => {
    const user = userEvent.setup()
    mockGetEnv.mockResolvedValue(twoEntryEnvData)
    renderWithProviders(<EnvEditor stackId="test-stack" />)

    await waitFor(() => {
      expect(screen.getAllByLabelText(/Environment variable key/).length).toBe(4)
    })

    const deleteButtons = screen.getAllByLabelText('Delete entry 1')
    await user.click(deleteButtons[0])

    await waitFor(() => {
      expect(screen.queryAllByDisplayValue('8080')).toHaveLength(0)
    })
    // HOST survives, re-indexed as entry 1
    expect(screen.getAllByDisplayValue('localhost').length).toBe(2)
  })

  it('editing a key to match a sensitive pattern flips the row to masked rendering', async () => {
    mockGetEnv.mockResolvedValue({
      filename: '.env',
      entries: [{ key: 'FOO', value: 'bar', sensitive: false, comment: false, line: 1 }],
      raw: 'FOO=bar\n',
    })
    renderWithProviders(<EnvEditor stackId="test-stack" />)

    const keyInput = (await screen.findAllByLabelText(/Environment variable key/))[0]
    // fireEvent.change (single commit) rather than simulated per-keystroke
    // typing: each entries.map(...) row is keyed by the live entry.key value
    // (see EnvEditor.tsx's `key={entry.key || `entry-${i}`}`), so typing a new
    // key character-by-character remounts the row — and loses the input focus
    // that user-event's typed keystrokes rely on — after the very first
    // keystroke. This is a pre-existing quirk of the component, not something
    // introduced here; see the Phase 1 checkpoint report.
    fireEvent.change(keyInput, { target: { value: 'FOO_SECRET' } })

    await waitFor(() => {
      const valueInputs = screen.getAllByDisplayValue('bar') as HTMLInputElement[]
      valueInputs.forEach((input) => expect(input.type).toBe('password'))
    })
    expect(screen.getAllByLabelText(/Toggle visibility for entry/).length).toBeGreaterThan(0)
  })

  it('renders comment rows with a disabled, non-editable key input and italic value', async () => {
    mockGetEnv.mockResolvedValue({
      filename: '.env',
      entries: [
        { key: '', value: '# a leading comment', sensitive: false, comment: true, line: 1 },
        { key: 'PORT', value: '8080', sensitive: false, comment: false, line: 2 },
      ],
      raw: '# a leading comment\nPORT=8080\n',
    })
    renderWithProviders(<EnvEditor stackId="test-stack" />)

    await waitFor(() => {
      // Rendered once for desktop, once for mobile.
      expect(screen.getAllByText('# a leading comment').length).toBeGreaterThan(0)
    })
    const commentKeyInput = screen.getAllByLabelText('Environment variable key 1')[0]
    expect(commentKeyInput).toBeDisabled()
    screen.getAllByText('# a leading comment').forEach((el) => expect(el).toHaveClass('italic'))
    expect(screen.getAllByText('comment').length).toBeGreaterThan(0)
  })

  it('disables the Visible checkbox whenever the key matches a sensitive naming pattern, regardless of the stored flag', async () => {
    mockGetEnv.mockResolvedValue({
      filename: '.env',
      entries: [{ key: 'DB_PASSWORD', value: 'x', sensitive: false, comment: false, line: 1 }],
      raw: 'DB_PASSWORD=x\n',
    })
    renderWithProviders(<EnvEditor stackId="test-stack" />)

    await waitFor(() => {
      expect(screen.getAllByLabelText('Toggle visibility for entry 1').length).toBeGreaterThan(0)
    })
    const checkboxes = screen
      .getAllByLabelText('Toggle visibility for entry 1')
      .filter((el) => el.getAttribute('type') === 'checkbox')
    expect(checkboxes.length).toBeGreaterThan(0)
    checkboxes.forEach((cb) => expect(cb).toBeDisabled())
  })

  it('filters rows via the search input and shows a no-matches hint', async () => {
    const user = userEvent.setup()
    mockGetEnv.mockResolvedValue(twoEntryEnvData)
    renderWithProviders(<EnvEditor stackId="test-stack" />)

    await waitFor(() => {
      expect(screen.getAllByDisplayValue('8080').length).toBe(2)
    })

    const search = screen.getByPlaceholderText('Filter env vars…')
    await user.type(search, 'host')

    await waitFor(() => {
      expect(screen.queryAllByDisplayValue('8080')).toHaveLength(0)
      expect(screen.getAllByDisplayValue('localhost').length).toBe(2)
    })

    await user.clear(search)
    await user.type(search, 'nomatch')
    await waitFor(() => {
      expect(screen.getByText('No matches.')).toBeInTheDocument()
    })
  })

  it('shows the error state with a Retry button that re-fetches the env file', async () => {
    const user = userEvent.setup()
    mockGetEnv.mockRejectedValue(new Error('boom'))
    renderWithProviders(<EnvEditor stackId="test-stack" />)

    await waitFor(() => {
      expect(screen.getByText('Failed to load environment file')).toBeInTheDocument()
    })
    expect(mockGetEnv).toHaveBeenCalledTimes(1)

    await user.click(screen.getByText('Retry'))

    await waitFor(() => {
      expect(mockGetEnv).toHaveBeenCalledTimes(2)
    })
  })

  it('Save (table view) posts the current entries array, not raw', async () => {
    const user = userEvent.setup()
    mockGetEnv.mockResolvedValue(twoEntryEnvData)
    mockUpdateEnv.mockResolvedValue({ outcome: 'success', reason: 'Saved' })
    renderWithProviders(<EnvEditor stackId="test-stack" />)

    const keyInput = (await screen.findAllByLabelText('Environment variable key 1'))[0]
    fireEvent.change(keyInput, { target: { value: 'PORT_NUM' } })

    await user.click(screen.getByText('Save'))

    await waitFor(() => {
      expect(mockUpdateEnv).toHaveBeenCalledWith(
        'test-stack',
        expect.objectContaining({
          entries: expect.arrayContaining([
            expect.objectContaining({ key: 'PORT_NUM', value: '8080' }),
          ]),
        }),
      )
      const [, body] = mockUpdateEnv.mock.calls[0]
      expect(body.raw).toBeUndefined()
    })
  })
})
