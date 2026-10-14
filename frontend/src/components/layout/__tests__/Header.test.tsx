import { describe, it, expect, vi, beforeEach } from 'vitest'
import { fireEvent, screen, within } from '@testing-library/react'
import { renderWithProviders } from '@/test/utils'
import { Header } from '../Header'
import { HelpPanel } from '@/help/HelpPanel'
import { HELP_INDEX, useHelpStore } from '@/help/helpStore'

// agent-os-ejjf: the header (?) opens help, `?` toggles it outside text
// fields, and Ctrl+/ still opens the keyboard-shortcuts dialog.

vi.mock('@/components/layout/HeaderVitals', () => ({ HeaderVitals: () => null }))
vi.mock('@/hooks/useAuth', () => ({ useAuth: () => ({ user: { username: 'admin' }, logout: vi.fn() }) }))

function renderHeader(route = '/settings/backup') {
  return renderWithProviders(
    <>
      <Header />
      <HelpPanel />
    </>,
    { route },
  )
}

const isOpen = () => useHelpStore.getState().open

function pressQuestionMark(target: Element) {
  fireEvent.keyDown(target, { key: '?', shiftKey: true })
}

beforeEach(() => {
  useHelpStore.setState({ open: false, followPage: true, current: HELP_INDEX, history: [], focusHeading: false, shortcutsOpen: false })
})

describe('Header help entry points', () => {
  it('the header help button opens the panel on the page topic', async () => {
    renderHeader('/settings/backup')
    fireEvent.click(screen.getByTestId('header-help-button'))
    expect(await screen.findByTestId('help-panel')).toBeInTheDocument()
    expect(await screen.findByTestId('help-panel-topic-heading')).toHaveTextContent(/^Backup$/)
  })

  it('? on the page body toggles the panel', async () => {
    renderHeader()
    pressQuestionMark(document.body)
    expect(isOpen()).toBe(true)
    expect(await screen.findByTestId('help-panel')).toBeInTheDocument()
    pressQuestionMark(document.body)
    expect(isOpen()).toBe(false)
  })

  it.each([
    ['an input', () => document.createElement('input')],
    ['a textarea', () => document.createElement('textarea')],
    [
      'a contentEditable element',
      () => {
        const el = document.createElement('div')
        el.contentEditable = 'true'
        // jsdom does not implement isContentEditable; a browser derives it from the attribute.
        Object.defineProperty(el, 'isContentEditable', { value: true })
        return el
      },
    ],
  ])('? typed into %s does not open the panel', (_name, make) => {
    renderHeader()
    const field = make()
    document.body.appendChild(field)
    try {
      const event = new KeyboardEvent('keydown', { key: '?', shiftKey: true, bubbles: true, cancelable: true })
      field.dispatchEvent(event)
      expect(isOpen()).toBe(false)
      // The character is not swallowed.
      expect(event.defaultPrevented).toBe(false)
    } finally {
      field.remove()
    }
  })

  it('Ctrl+/ opens the keyboard shortcuts dialog, which lists ?', async () => {
    renderHeader()
    fireEvent.keyDown(document.body, { key: '/', ctrlKey: true })
    const dialog = await screen.findByRole('dialog', { name: 'Keyboard Shortcuts' })
    const row = within(dialog).getByText('Open help for this page').parentElement!
    expect(within(row).getByText('?').tagName).toBe('KBD')
    expect(isOpen()).toBe(false)
  })
})
