/**
 * Coverage for EnvEditor's undo/redo history stack and raw-editor sync, which
 * the original regression suite (EnvEditor.test.tsx) never exercised: button
 * enablement, click-driven undo/redo, the Ctrl+Z/Ctrl+Y keyboard shortcuts,
 * raw-textarea editing, the history dedup guard (identical states collapse
 * into one step), and the exact payload shape posted on raw-view save.
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

const baseEnvData = {
  filename: '.env',
  entries: [{ key: 'PORT', value: '8080', sensitive: false, comment: false, line: 1 }],
  raw: 'PORT=8080\n',
}

function getUndoButton() {
  return screen.getByTitle('Undo (Ctrl+Z)')
}
function getRedoButton() {
  return screen.getByTitle('Redo (Ctrl+Y)')
}

describe('EnvEditor undo/redo history', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('Undo and Redo are both disabled immediately after load (single history entry)', async () => {
    mockGetEnv.mockResolvedValue(baseEnvData)
    renderWithProviders(<EnvEditor stackId="test-stack" />)

    await waitFor(() => expect(getUndoButton()).toBeInTheDocument())
    expect(getUndoButton()).toBeDisabled()
    expect(getRedoButton()).toBeDisabled()
  })

  it('an edit enables Undo; clicking Undo reverts and enables Redo; clicking Redo restores the edit', async () => {
    const user = userEvent.setup()
    mockGetEnv.mockResolvedValue(baseEnvData)
    renderWithProviders(<EnvEditor stackId="test-stack" />)

    const keyInput = (await screen.findAllByLabelText('Environment variable key 1'))[0]
    // Single-commit change, not simulated typing: the row is keyed by the
    // live entry.key (EnvEditor.tsx's `key={entry.key || `entry-${i}`}`), so
    // per-keystroke typing remounts the row mid-edit. See the entries test
    // file and the Phase 1 checkpoint report for detail.
    fireEvent.change(keyInput, { target: { value: 'PORT_2' } })

    await waitFor(() => expect(getUndoButton()).not.toBeDisabled())
    expect(getRedoButton()).toBeDisabled()

    await user.click(getUndoButton())
    await waitFor(() => {
      expect(screen.getAllByDisplayValue('PORT')[0]).toBeInTheDocument()
    })
    expect(getUndoButton()).toBeDisabled()
    expect(getRedoButton()).not.toBeDisabled()

    await user.click(getRedoButton())
    await waitFor(() => {
      expect(screen.getAllByDisplayValue('PORT_2')[0]).toBeInTheDocument()
    })
  })

  it('Ctrl+Z undoes; both Ctrl+Y and Ctrl+Shift+Z redo — same effect as the buttons', async () => {
    mockGetEnv.mockResolvedValue(baseEnvData)
    renderWithProviders(<EnvEditor stackId="test-stack" />)

    const keyInput = (await screen.findAllByLabelText('Environment variable key 1'))[0]
    fireEvent.change(keyInput, { target: { value: 'PORT_2' } })
    await waitFor(() => expect(getUndoButton()).not.toBeDisabled())

    // Ctrl+Z (no shift) → undo.
    fireEvent.keyDown(window, { key: 'z', ctrlKey: true })
    await waitFor(() => {
      expect(screen.getAllByDisplayValue('PORT')[0]).toBeInTheDocument()
    })

    // Ctrl+Y → redo.
    fireEvent.keyDown(window, { key: 'y', ctrlKey: true })
    await waitFor(() => {
      expect(screen.getAllByDisplayValue('PORT_2')[0]).toBeInTheDocument()
    })

    fireEvent.keyDown(window, { key: 'z', ctrlKey: true })
    await waitFor(() => {
      expect(screen.getAllByDisplayValue('PORT')[0]).toBeInTheDocument()
    })

    // Ctrl+Shift+Z → the alternate redo binding (per EnvEditor.tsx's handler,
    // `e.key === 'y' || (e.key === 'z' && e.shiftKey)` is the redo branch,
    // not undo — this is not the "undo redo" convention some apps use).
    fireEvent.keyDown(window, { key: 'z', ctrlKey: true, shiftKey: true })
    await waitFor(() => {
      expect(screen.getAllByDisplayValue('PORT_2')[0]).toBeInTheDocument()
    })
  })

  it('typing in the raw editor updates its content and enables Save', async () => {
    const user = userEvent.setup()
    mockGetEnv.mockResolvedValue(baseEnvData)
    renderWithProviders(<EnvEditor stackId="test-stack" />)

    await waitFor(() => expect(screen.getByText('Table View')).toBeInTheDocument())
    await user.click(screen.getByText('Raw Editor'))

    const textarea = await screen.findByPlaceholderText('KEY=value')
    expect(screen.getByText('Save')).toBeDisabled()

    fireEvent.change(textarea, { target: { value: 'PORT=9090\n' } })

    expect(textarea).toHaveValue('PORT=9090\n')
    expect(screen.getByText('Save')).not.toBeDisabled()
  })

  it('pushing an identical raw state twice collapses into a single undo step', async () => {
    const user = userEvent.setup()
    mockGetEnv.mockResolvedValue(baseEnvData)
    renderWithProviders(<EnvEditor stackId="test-stack" />)

    await waitFor(() => expect(screen.getByText('Table View')).toBeInTheDocument())
    await user.click(screen.getByText('Raw Editor'))
    const textarea = await screen.findByPlaceholderText('KEY=value')

    fireEvent.change(textarea, { target: { value: 'X=1\n' } })
    fireEvent.change(textarea, { target: { value: 'X=1\n' } })

    fireEvent.click(getUndoButton())

    // A single Undo click must land back on the original raw content — if the
    // duplicate push had not been deduped, one Undo would land on an
    // intermediate state instead of the original.
    expect(textarea).toHaveValue('PORT=8080\n')
    expect(getUndoButton()).toBeDisabled()
  })

  it('Save (raw view) posts the raw string, not entries', async () => {
    const user = userEvent.setup()
    mockGetEnv.mockResolvedValue(baseEnvData)
    mockUpdateEnv.mockResolvedValue({ outcome: 'success', reason: 'Saved' })
    renderWithProviders(<EnvEditor stackId="test-stack" />)

    await waitFor(() => expect(screen.getByText('Table View')).toBeInTheDocument())
    await user.click(screen.getByText('Raw Editor'))
    const textarea = await screen.findByPlaceholderText('KEY=value')

    fireEvent.change(textarea, { target: { value: 'PORT=9090\n' } })
    await user.click(screen.getByText('Save'))

    await waitFor(() => {
      expect(mockUpdateEnv).toHaveBeenCalledWith('test-stack', { raw: 'PORT=9090\n' })
    })
  })
})
