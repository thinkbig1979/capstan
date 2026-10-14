import { describe, it, expect, vi, beforeEach } from 'vitest'
import { useEffect } from 'react'
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter, useNavigate, type NavigateFunction } from 'react-router'
import { HelpPanel } from '../HelpPanel'
import { HELP_INDEX, useHelpStore, usePageHelpRef } from '../helpStore'
import type { HelpDoc } from '../content'

const loader = vi.hoisted(() => ({ actual: null as null | (() => Promise<HelpDoc[]>) }))

vi.mock('../content', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../content')>()
  loader.actual = actual.loadHelpLibrary
  return { ...actual, loadHelpLibrary: vi.fn(actual.loadHelpLibrary) }
})

const { loadHelpLibrary } = await import('../content')
const loadMock = vi.mocked(loadHelpLibrary)

let navigateTo: NavigateFunction

/** An opener on the page, as the header button is: opens help on the page's topic. */
function Opener() {
  const navigate = useNavigate()
  useEffect(() => {
    navigateTo = navigate
  }, [navigate])
  const ref = usePageHelpRef()
  return (
    <button type="button" onClick={() => useHelpStore.getState().openForPage(ref)}>
      Open help
    </button>
  )
}

function renderPanel(route: string) {
  return render(
    <MemoryRouter initialEntries={[route]}>
      <Opener />
      <HelpPanel />
    </MemoryRouter>,
  )
}

async function openFromButton() {
  const opener = screen.getByRole('button', { name: 'Open help' })
  opener.focus()
  fireEvent.click(opener)
  return { opener, panel: await screen.findByTestId('help-panel') }
}

const topic = () => screen.findByTestId('help-panel-topic-heading')

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((r) => (resolve = r))
  return { promise, resolve }
}

beforeEach(() => {
  loadMock.mockReset()
  loadMock.mockImplementation(() => loader.actual!())
  useHelpStore.setState({ open: false, followPage: true, current: HELP_INDEX, history: [], focusHeading: false, shortcutsOpen: false })
})

describe('HelpPanel topic', () => {
  it('opens on the settings:backup topic with the Backup heading, title focused', async () => {
    renderPanel('/settings/backup')
    const { panel } = await openFromButton()

    expect(await topic()).toHaveTextContent(/^Backup$/)
    // The doc rendered, scrolled to the section (jsdom has no layout; the heading exists).
    expect(await within(panel).findByRole('heading', { level: 2, name: 'Backup' })).toHaveAttribute('data-help-anchor', 'backup')
    // Opened on the page: focus is on the panel title, not the section.
    expect(screen.getByRole('heading', { name: 'Help' })).toHaveFocus()
  })

  it('focuses the section heading when opened at a topic from elsewhere', async () => {
    renderPanel('/')
    act(() => useHelpStore.getState().openAt({ doc: 'help/settings', anchor: 'age-floor' }))
    const heading = await screen.findByRole('heading', { level: 3, name: 'Age floor' })
    await waitFor(() => expect(heading).toHaveFocus())
  })
})

describe('HelpPanel states', () => {
  it('shows a loading status while the library loads, then the topic', async () => {
    const pending = deferred<HelpDoc[]>()
    loadMock.mockImplementation(() => pending.promise)
    renderPanel('/settings/backup')
    await openFromButton()

    expect(screen.getByRole('status')).toHaveTextContent('Loading help…')
    expect(screen.queryByTestId('help-panel-topic-heading')).toBeNull()

    const docs = await loader.actual!()
    await act(async () => pending.resolve(docs))
    expect(await topic()).toHaveTextContent('Backup')
  })

  it('shows Retry when the loader rejects, and Retry recovers', async () => {
    loadMock.mockImplementationOnce(() => Promise.reject(new Error('no docs bundled')))
    renderPanel('/settings/backup')
    await openFromButton()

    expect(await screen.findByText('Could not load help.')).toBeInTheDocument()
    expect(screen.queryByTestId('help-panel-topic-heading')).toBeNull()

    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(await topic()).toHaveTextContent('Backup')
    expect(loadMock).toHaveBeenCalledTimes(2)
    expect(screen.queryByText('Could not load help.')).toBeNull()
  })

  it('shows the whole guide with a notice when the anchor is not found', async () => {
    renderPanel('/')
    act(() => useHelpStore.getState().openAt({ doc: 'help/settings', anchor: 'no-such-section' }))

    expect(await screen.findByTestId('help-panel-anchor-missing')).toHaveTextContent(
      'That section was not found; showing the whole guide.',
    )
    expect(await topic()).toHaveTextContent('Settings help')
    expect(await screen.findByRole('heading', { level: 2, name: 'Backup' })).toBeInTheDocument()
  })

  it('shows no notice when the anchor exists', async () => {
    renderPanel('/settings/backup')
    await openFromButton()
    await topic()
    expect(screen.queryByTestId('help-panel-anchor-missing')).toBeNull()
  })
})

describe('HelpPanel follow page', () => {
  it('swaps the topic on route change, stops after in-panel navigation, and This page restores it', async () => {
    renderPanel('/settings/backup')
    await openFromButton()
    expect(await topic()).toHaveTextContent(/^Backup$/)

    act(() => navigateTo('/settings/git'))
    expect(await topic()).toHaveTextContent(/^Git$/)

    // Navigate inside the panel: the guide index, then a guide.
    fireEvent.click(screen.getByRole('button', { name: 'All guides' }))
    fireEvent.click(await screen.findByRole('button', { name: 'Configuration Reference' }))
    expect(await topic()).toHaveTextContent('Configuration Reference')

    act(() => navigateTo('/settings/about'))
    // The route changed, the guide stays.
    expect(await topic()).toHaveTextContent('Configuration Reference')

    fireEvent.click(screen.getByTestId('help-panel-this-page-button'))
    expect(await topic()).toHaveTextContent(/^About$/)
  })

  it('Back returns through in-panel navigation and is disabled with no history', async () => {
    renderPanel('/settings/backup')
    await openFromButton()
    await topic()
    const back = screen.getByTestId('help-panel-back-button')
    expect(back).toBeDisabled()

    fireEvent.click(screen.getByRole('button', { name: 'All guides' }))
    fireEvent.click(await screen.findByRole('button', { name: 'Getting Started' }))
    expect(await topic()).toHaveTextContent('Getting Started')

    fireEvent.click(back)
    expect(await screen.findByTestId('help-panel-index')).toBeInTheDocument()
    fireEvent.click(back)
    expect(await topic()).toHaveTextContent(/^Backup$/)
    expect(back).toBeDisabled()
  })
})

describe('HelpPanel keyboard and focus', () => {
  it('Escape closes the panel and focus returns to the opener', async () => {
    renderPanel('/settings/backup')
    const { opener } = await openFromButton()
    const title = screen.getByRole('heading', { name: 'Help' })
    expect(title).toHaveFocus()

    fireEvent.keyDown(title, { key: 'Escape' })
    await waitFor(() => expect(screen.queryByTestId('help-panel')).toBeNull())
    await waitFor(() => expect(opener).toHaveFocus())
  })

  it('a click outside the panel does not close it', async () => {
    renderPanel('/settings/backup')
    const { opener } = await openFromButton()
    fireEvent.pointerDown(opener)
    fireEvent.click(document.body)
    expect(screen.getByTestId('help-panel')).toBeInTheDocument()
  })

  it('closes when it unmounts with AppShell, so it does not reopen after the next login', async () => {
    const { unmount } = renderPanel('/settings/backup')
    await openFromButton()
    unmount()
    expect(useHelpStore.getState().open).toBe(false)
  })

  it('the footer opens the keyboard shortcuts dialog', async () => {
    renderPanel('/settings/backup')
    await openFromButton()
    fireEvent.click(screen.getByRole('button', { name: 'Keyboard shortcuts' }))
    expect(useHelpStore.getState().shortcutsOpen).toBe(true)
  })
})

describe('HelpPanel guide index', () => {
  it('groups api and architecture only under For developers & automation', async () => {
    renderPanel('/settings/backup')
    await openFromButton()
    fireEvent.click(screen.getByRole('button', { name: 'All guides' }))
    const index = await screen.findByTestId('help-panel-index')

    const groups = within(index).getAllByTestId('help-panel-index-group')
    const byLabel = Object.fromEntries(
      groups.map((g) => [g.getAttribute('aria-label'), within(g).getAllByRole('button').map((b) => b.textContent)]),
    )
    expect(Object.keys(byLabel)).toEqual([
      'Getting started',
      'Page help',
      'How-to',
      'Reference',
      'Explanation',
      'For developers & automation',
    ])
    expect(byLabel['For developers & automation']).toEqual(['Architecture', 'API Reference'])
    const elsewhere = Object.entries(byLabel)
      .filter(([label]) => label !== 'For developers & automation')
      .flatMap(([, titles]) => titles)
    expect(elsewhere).not.toContain('API Reference')
    expect(elsewhere).not.toContain('Architecture')
    expect(byLabel['Reference']).toEqual(['Configuration Reference'])
    expect(byLabel['Explanation']).toEqual(['Security Model'])
    // Every bundled doc is listed once.
    const docs = await loader.actual!()
    expect(Object.values(byLabel).flat()).toHaveLength(docs.length)
  })
})
