/**
 * Regression coverage for agent-os-382: both the desktop TableRow and the
 * mobile card row in env-editor/EnvTableView.tsx used to key off the LIVE
 * key-field content (`entry.key || `entry-${i}``), so editing a key
 * character-by-character changed the React key every keystroke and
 * unmounted/remounted the row mid-edit. That loses focus in a real browser
 * and breaks userEvent.type() in tests (its held element reference goes
 * stale after the first keystroke's remount). See EnvEditor.entries.test.tsx
 * and EnvEditor.history.test.tsx for the fireEvent.change workaround this
 * bug previously required.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen } from '@testing-library/react'
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

describe('EnvEditor row identity stability', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('typing a key character-by-character keeps the row mounted and completes every keystroke', async () => {
    const user = userEvent.setup()
    mockGetEnv.mockResolvedValue({
      filename: '.env',
      entries: [{ key: 'FOO', value: 'bar', sensitive: false, comment: false, line: 1 }],
      raw: 'FOO=bar\n',
    })
    renderWithProviders(<EnvEditor stackId="test-stack" />)

    const keyInput = (await screen.findAllByLabelText(/Environment variable key/))[0] as HTMLInputElement

    await user.clear(keyInput)
    await user.type(keyInput, 'FOO_SECRET')

    expect(keyInput).toHaveValue('FOO_SECRET')
    // The DOM node userEvent typed into must still be attached — a remount
    // mid-edit would have detached it from the document.
    expect(document.body.contains(keyInput)).toBe(true)
  })

  it("a row's underlying DOM node survives editing its own key field (desktop table row)", async () => {
    const user = userEvent.setup()
    mockGetEnv.mockResolvedValue({
      filename: '.env',
      entries: [{ key: 'FOO', value: 'bar', sensitive: false, comment: false, line: 1 }],
      raw: 'FOO=bar\n',
    })
    renderWithProviders(<EnvEditor stackId="test-stack" />)

    const keyInputs = await screen.findAllByLabelText(/Environment variable key/)
    const desktopKeyInput = keyInputs[0] as HTMLInputElement
    const row = desktopKeyInput.closest('tr')
    expect(row).not.toBeNull()

    await user.type(desktopKeyInput, 'X')

    const rowAfter = document.querySelectorAll('tr')[1] // header is tr[0]
    expect(rowAfter).toBe(row)
  })
})
