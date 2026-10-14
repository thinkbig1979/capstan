import { expect, vi } from 'vitest'
import { screen } from '@testing-library/react'
import type { UserEvent } from '@testing-library/user-event'
import { useHelpStore } from '@/help/helpStore'
import { FIELD_HELP, type FieldHelpId } from '@/help/registry'

/**
 * Proves a field's (i) hint is wired to its help topic: opens "Help: <label>",
 * clicks Learn more, and expects the help panel to be asked for FIELD_HELP[id]
 * exactly once. The store's real openAt is swapped for a spy and put back, so
 * the check does not leak into the next test.
 */
export async function expectHintOpensHelp(user: UserEvent, label: string, id: FieldHelpId) {
  const realOpenAt = useHelpStore.getState().openAt
  const openAt = vi.fn()
  useHelpStore.setState({ openAt })
  try {
    await user.click(await screen.findByRole('button', { name: `Help: ${label}` }))
    await user.click(await screen.findByTestId('help-hint-learn-more-button'))
    expect(openAt).toHaveBeenCalledTimes(1)
    expect(openAt).toHaveBeenCalledWith(FIELD_HELP[id])
  } finally {
    useHelpStore.setState({ openAt: realOpenAt })
  }
}
