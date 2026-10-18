import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithProviders } from '../../../test/utils'

/**
 * The per-directory "Allow commit and push" switch (agent-os-k81p.3): it
 * shows the stored value, stays off until that value has loaded, and each
 * click sends PUT /directories/push {path, enabled}.
 */

const mockList = vi.fn()
const mockUpdatePush = vi.fn()

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), warning: vi.fn() },
}))

vi.mock('@/lib/api', () => ({
  directoriesApi: {
    list: (...args: unknown[]) => mockList(...args),
    updateCredentials: vi.fn(),
    credentialStatus: vi.fn().mockResolvedValue({ path: '/opt/stacks/app', status: 'none' }),
    updatePush: (...args: unknown[]) => mockUpdatePush(...args),
  },
}))

import { GitSettingsSection } from '../GitSettingsSection'

const DIR = '/opt/stacks/app'

function renderSection() {
  return renderWithProviders(<GitSettingsSection directoryPath={DIR} stackId="s1" open onToggle={() => {}} />)
}

describe('GitSettingsSection: Allow commit and push', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockList.mockResolvedValue([{ path: DIR, name: 'app', gitAuthType: 'inherit', gitPushEnabled: false }])
    mockUpdatePush.mockImplementation((path: string, enabled: boolean) =>
      Promise.resolve({ path, name: 'app', gitPushEnabled: enabled }),
    )
  })

  it('turns pushing on, then off, sending {path, enabled} each time', async () => {
    const user = userEvent.setup()
    renderSection()
    const sw = screen.getByRole('switch', { name: 'Allow commit and push' })
    await waitFor(() => expect(sw).toBeEnabled())
    expect(sw).not.toBeChecked()

    await user.click(sw)
    await waitFor(() => expect(sw).toBeChecked())
    expect(mockUpdatePush).toHaveBeenNthCalledWith(1, DIR, true)

    await waitFor(() => expect(sw).toBeEnabled())
    await user.click(sw)
    await waitFor(() => expect(sw).not.toBeChecked())
    expect(mockUpdatePush).toHaveBeenNthCalledWith(2, DIR, false)
    expect(mockUpdatePush).toHaveBeenCalledTimes(2)
  })

  it('shows the stored value, and is disabled until it has loaded', async () => {
    let resolve: (v: unknown) => void = () => {}
    mockList.mockReturnValue(new Promise((r) => { resolve = r }))
    renderSection()
    const sw = screen.getByRole('switch', { name: 'Allow commit and push' })
    expect(sw).toBeDisabled()
    resolve([{ path: DIR, name: 'app', gitAuthType: 'inherit', gitPushEnabled: true }])
    await waitFor(() => expect(sw).toBeEnabled())
    expect(sw).toBeChecked()
  })
})
