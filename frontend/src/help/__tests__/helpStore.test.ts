import { describe, it, expect, beforeEach } from 'vitest'
import { HELP_INDEX, useHelpStore } from '../helpStore'

const reset = () =>
  useHelpStore.setState({ open: false, followPage: true, current: HELP_INDEX, history: [], focusHeading: false, shortcutsOpen: false })

beforeEach(reset)

describe('openSearch', () => {
  it('opens a closed panel in search mode with the query, stops following the page, keeps no history', () => {
    useHelpStore.setState({ history: [{ doc: 'stale' }] })
    useHelpStore.getState().openSearch('retention')
    expect(useHelpStore.getState()).toMatchObject({
      open: true,
      followPage: false,
      current: { kind: 'search', q: 'retention' },
      history: [],
    })
  })

  it('keeps the view it leaves for Back on the first call only; later calls replace the query', () => {
    const doc = { doc: 'help/settings', anchor: 'backup' }
    useHelpStore.getState().openForPage(doc)

    useHelpStore.getState().openSearch('r')
    useHelpStore.getState().openSearch('rc')
    useHelpStore.getState().openSearch('rclone')

    expect(useHelpStore.getState().current).toEqual({ kind: 'search', q: 'rclone' })
    expect(useHelpStore.getState().history).toEqual([doc])

    useHelpStore.getState().back()
    expect(useHelpStore.getState().current).toEqual(doc)
  })

  it('searching from the palette over an open doc keeps that doc for Back', () => {
    const doc = { doc: 'getting-started' }
    useHelpStore.getState().openAt(doc)
    useHelpStore.getState().openSearch('backup')
    expect(useHelpStore.getState().history).toEqual([doc])
  })
})
