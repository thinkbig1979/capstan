import { describe, it, expect, vi, beforeAll, beforeEach } from 'vitest'
import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { HelpHint } from '../help-hint'
import { HELP_INDEX, useHelpStore } from '@/help/helpStore'
import { FIELD_HELP } from '@/help/registry'
import { HelpPanel } from '@/help/HelpPanel'
import { MemoryRouter } from 'react-router'

// Captured once: tests swap openAt for a spy, and setState merges, so it has to be put back.
const realOpenAt = useHelpStore.getState().openAt

// HelpPanel renders docs through a lazy renderer. The first doc a file renders pays
// for it cold (react-markdown + remark-gfm import, first render, React's Suspense
// reveal delay): over 1 s on a loaded machine, past the test's 1 s budget
// (agent-os-ursd). Render one doc here, under the hook timeout, instead.
beforeAll(async () => {
  const { unmount } = render(
    <MemoryRouter>
      <HelpPanel />
    </MemoryRouter>,
  )
  act(() => useHelpStore.getState().openAt(FIELD_HELP['backup.retention']))
  await waitFor(() => expect(document.querySelector('[data-help-anchor="retention-and-pruning"]')).not.toBeNull(), {
    timeout: 9000,
  })
  unmount()
})

beforeEach(() => {
  useHelpStore.setState({ openAt: realOpenAt, open: false, followPage: true, current: HELP_INDEX, history: [], focusHeading: false, shortcutsOpen: false })
})

describe('HelpHint', () => {
  it('renders a "Learn more" link to the given href when one is provided', async () => {
    const user = userEvent.setup()
    render(
      <HelpHint label="Volumes" title="Volumes" href="https://example.com/docs/volumes.md">
        <p>Where containers store data.</p>
      </HelpHint>,
    )

    await user.click(screen.getByRole('button', { name: 'Help: Volumes' }))

    const link = await screen.findByRole('link', { name: /learn more/i })
    expect(link).toHaveAttribute('href', 'https://example.com/docs/volumes.md')
    expect(link).toHaveAttribute('target', '_blank')
    expect(link).toHaveAttribute('rel', expect.stringContaining('noopener'))
  })

  it('renders no link when href is omitted', async () => {
    const user = userEvent.setup()
    render(
      <HelpHint label="Volumes" title="Volumes">
        <p>Where containers store data.</p>
      </HelpHint>,
    )

    await user.click(screen.getByRole('button', { name: 'Help: Volumes' }))

    expect(await screen.findByText('Where containers store data.')).toBeInTheDocument()
    expect(screen.queryByRole('link', { name: /learn more/i })).not.toBeInTheDocument()
  })

  describe('topic', () => {
    it('renders a Learn more button, not a link, and no external href', async () => {
      const user = userEvent.setup()
      render(
        <HelpHint label="Retention" title="Retention" topic="backup.retention">
          <p>Body.</p>
        </HelpHint>,
      )

      await user.click(screen.getByRole('button', { name: 'Help: Retention' }))

      const button = await screen.findByTestId('help-hint-learn-more-button')
      expect(button.tagName).toBe('BUTTON')
      expect(button).toHaveTextContent('Learn more')
      expect(screen.queryByRole('link', { name: /learn more/i })).not.toBeInTheDocument()
    })

    it('closes the popover and opens help at FIELD_HELP[topic] on click', async () => {
      const user = userEvent.setup()
      const openAt = vi.fn()
      useHelpStore.setState({ openAt })
      render(
        <HelpHint label="Retention" title="Retention" topic="backup.retention">
          <p>Body.</p>
        </HelpHint>,
      )

      await user.click(screen.getByRole('button', { name: 'Help: Retention' }))
      await user.click(await screen.findByTestId('help-hint-learn-more-button'))

      expect(openAt).toHaveBeenCalledTimes(1)
      expect(openAt).toHaveBeenCalledWith(FIELD_HELP['backup.retention'])
      await waitFor(() => expect(screen.queryByText('Body.')).not.toBeInTheDocument())
    })

    it('does not let the click reach a parent row handler', async () => {
      const user = userEvent.setup()
      const rowClick = vi.fn()
      useHelpStore.setState({ openAt: vi.fn() })
      render(
        <div onClick={rowClick}>
          <HelpHint label="Retention" topic="backup.retention">
            <p>Body.</p>
          </HelpHint>
        </div>,
      )

      await user.click(screen.getByRole('button', { name: 'Help: Retention' }))
      await user.click(await screen.findByTestId('help-hint-learn-more-button'))

      expect(rowClick).not.toHaveBeenCalled()
    })

    it('lands focus on the topic heading in the panel, not back on the (i) button', async () => {
      const user = userEvent.setup()
      render(
        <MemoryRouter>
          <HelpHint label="Retention" topic="backup.retention">
            <p>Body.</p>
          </HelpHint>
          <HelpPanel />
        </MemoryRouter>,
      )

      const trigger = screen.getByRole('button', { name: 'Help: Retention' })
      await user.click(trigger)
      await user.click(await screen.findByTestId('help-hint-learn-more-button'))

      // Pins onCloseAutoFocus's preventDefault: without it Radix hands focus back
      // to the (i) as the popover closes, and in a browser the exit animation can
      // delay that restore past the heading's focus, so the (i) keeps it.
      expect(trigger).not.toHaveFocus()
      // Wait on the anchor, a cheap selector: a findByRole with a name re-walks the whole
      // rendered doc on every poll, about 0.5 s a poll on a loaded machine, and ran out of
      // its 1 s budget (agent-os-ursd). The role is asserted once, after.
      const heading = await waitFor(() => {
        const el = document.querySelector<HTMLElement>('[data-help-anchor="retention-and-pruning"]')
        expect(el).not.toBeNull()
        return el!
      })
      expect(screen.getByRole('heading', { level: 3, name: 'Retention and pruning' })).toBe(heading)
      await waitFor(() => expect(heading).toHaveFocus())
      expect(trigger).not.toHaveFocus()
    })

    it('rejects href and topic together at compile time', () => {
      // Never rendered: this is a type-level assertion, enforced by tsc -b.
      const both = () => (
        // @ts-expect-error href and topic are mutually exclusive
        <HelpHint label="x" href="https://example.com" topic="backup.retention">
          <p>x</p>
        </HelpHint>
      )
      expect(both).toBeTypeOf('function')
    })
  })
})
