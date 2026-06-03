import { describe, it, expect, beforeEach, beforeAll } from 'vitest'
import { useUIStore } from '../uiStore'

beforeAll(() => {
  Object.defineProperty(window, 'matchMedia', {
    writable: true,
    value: (query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addListener: () => {},
      removeListener: () => {},
      addEventListener: () => {},
      removeEventListener: () => {},
      dispatchEvent: () => false,
    }),
  })
})

beforeEach(() => {
  useUIStore.setState({
    theme: 'system',
    sidebarOpen: true,
    sidebarWidth: 256,
  })
  document.documentElement.classList.remove('dark')
})

describe('uiStore initial state', () => {
  it('has system theme by default', () => {
    expect(useUIStore.getState().theme).toBe('system')
  })

  it('has sidebar open by default', () => {
    expect(useUIStore.getState().sidebarOpen).toBe(true)
  })

  it('has sidebar width of 256 by default', () => {
    expect(useUIStore.getState().sidebarWidth).toBe(256)
  })
})

describe('uiStore setTheme', () => {
  it('sets theme to dark and adds dark class', () => {
    useUIStore.getState().setTheme('dark')

    expect(useUIStore.getState().theme).toBe('dark')
    expect(document.documentElement.classList.contains('dark')).toBe(true)
  })

  it('sets theme to light and removes dark class', () => {
    document.documentElement.classList.add('dark')
    useUIStore.getState().setTheme('light')

    expect(useUIStore.getState().theme).toBe('light')
    expect(document.documentElement.classList.contains('dark')).toBe(false)
  })

  it('sets theme to system', () => {
    useUIStore.getState().setTheme('system')

    expect(useUIStore.getState().theme).toBe('system')
  })
})

describe('uiStore toggleSidebar', () => {
  it('toggles sidebar from open to closed', () => {
    expect(useUIStore.getState().sidebarOpen).toBe(true)
    useUIStore.getState().toggleSidebar()
    expect(useUIStore.getState().sidebarOpen).toBe(false)
  })

  it('toggles sidebar from closed to open', () => {
    useUIStore.setState({ sidebarOpen: false })
    useUIStore.getState().toggleSidebar()
    expect(useUIStore.getState().sidebarOpen).toBe(true)
  })
})

describe('uiStore setSidebarWidth', () => {
  it('sets width within bounds', () => {
    useUIStore.getState().setSidebarWidth(300)
    expect(useUIStore.getState().sidebarWidth).toBe(300)
  })

  it('clamps to minimum of 200', () => {
    useUIStore.getState().setSidebarWidth(150)
    expect(useUIStore.getState().sidebarWidth).toBe(200)
  })

  it('clamps to maximum of 480', () => {
    useUIStore.getState().setSidebarWidth(600)
    expect(useUIStore.getState().sidebarWidth).toBe(480)
  })
})

describe('uiStore logPrefs', () => {
  beforeEach(() => {
    useUIStore.setState({
      logPrefs: {
        showTimestamps: true,
        autoScroll: true,
        wrap: true,
        timeRange: 'all',
        errorsOnly: false,
      },
    })
  })

  it('defaults to sensible log prefs', () => {
    const { logPrefs } = useUIStore.getState()
    expect(logPrefs.showTimestamps).toBe(true)
    expect(logPrefs.autoScroll).toBe(true)
    expect(logPrefs.wrap).toBe(true)
    expect(logPrefs.timeRange).toBe('all')
    expect(logPrefs.errorsOnly).toBe(false)
  })

  it('patches a single pref without clobbering the others', () => {
    useUIStore.getState().setLogPrefs({ wrap: false })
    const { logPrefs } = useUIStore.getState()
    expect(logPrefs.wrap).toBe(false)
    expect(logPrefs.showTimestamps).toBe(true)
    expect(logPrefs.autoScroll).toBe(true)
  })

  it('updates the time range', () => {
    useUIStore.getState().setLogPrefs({ timeRange: '15m' })
    expect(useUIStore.getState().logPrefs.timeRange).toBe('15m')
  })
})

describe('uiStore pinnedStacks', () => {
  beforeEach(() => {
    useUIStore.setState({ pinnedStacks: [] })
  })

  it('pins a stack', () => {
    useUIStore.getState().togglePinnedStack('a')
    expect(useUIStore.getState().pinnedStacks).toEqual(['a'])
    expect(useUIStore.getState().isPinned('a')).toBe(true)
  })

  it('unpins a previously pinned stack', () => {
    useUIStore.getState().togglePinnedStack('a')
    useUIStore.getState().togglePinnedStack('a')
    expect(useUIStore.getState().pinnedStacks).toEqual([])
    expect(useUIStore.getState().isPinned('a')).toBe(false)
  })

  it('keeps multiple pins independent', () => {
    useUIStore.getState().togglePinnedStack('a')
    useUIStore.getState().togglePinnedStack('b')
    useUIStore.getState().togglePinnedStack('a')
    expect(useUIStore.getState().pinnedStacks).toEqual(['b'])
  })
})
