import { create } from 'zustand'
import { persist } from 'zustand/middleware'

export type Theme = 'light' | 'dark' | 'system'

export type LogTimeRange = 'all' | '5m' | '15m' | '1h' | 'custom'

// User-tweakable log viewer preferences, persisted so they survive reloads and
// apply across every stack's log view (previously these were per-mount useState
// that reset on navigation).
interface LogPrefs {
  showTimestamps: boolean
  autoScroll: boolean
  /** Soft-wrap long lines (vs horizontal scroll). */
  wrap: boolean
  timeRange: LogTimeRange
  /** Show only error/warning level lines. */
  errorsOnly: boolean
}

const DEFAULT_LOG_PREFS: LogPrefs = {
  showTimestamps: true,
  autoScroll: true,
  wrap: true,
  timeRange: 'all',
  errorsOnly: false,
}

interface UIState {
  theme: Theme
  sidebarOpen: boolean
  sidebarWidth: number
  logPrefs: LogPrefs
  /** Stack ids the user pinned to the top of the sidebar list. */
  pinnedStacks: string[]
  setTheme: (theme: Theme) => void
  toggleSidebar: () => void
  closeSidebar: () => void
  setSidebarWidth: (width: number) => void
  setLogPrefs: (patch: Partial<LogPrefs>) => void
  togglePinnedStack: (id: string) => void
  isPinned: (id: string) => boolean
  resetLayout: () => void
}

const DEFAULT_SIDEBAR_OPEN = true
const DEFAULT_SIDEBAR_WIDTH = 256

const LAYOUT_STORAGE_KEYS = [
  'sidebar-search',
  'sidebar-filter',
  'sidebar-sort',
  'sidebar-collapsed',
  'dashboard-sort',
  'dashboard-filter',
  'settings-section-states',
] as const

function getSystemTheme(): 'light' | 'dark' {
  if (typeof window === 'undefined') return 'light'
  return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
}

function applyTheme(theme: Theme) {
  const root = document.documentElement
  const isDark = theme === 'dark' || (theme === 'system' && getSystemTheme() === 'dark')

  if (isDark) {
    root.classList.add('dark')
  } else {
    root.classList.remove('dark')
  }
}

export const useUIStore = create<UIState>()(
  persist(
    (set, get) => ({
      theme: 'system',
      sidebarOpen: true,
      sidebarWidth: 256,
      logPrefs: DEFAULT_LOG_PREFS,
      pinnedStacks: [],

      setTheme: (theme) => {
        set({ theme })
        applyTheme(theme)
      },

      toggleSidebar: () => {
        set((state) => ({ sidebarOpen: !state.sidebarOpen }))
      },

      closeSidebar: () => {
        set({ sidebarOpen: false })
      },

      setSidebarWidth: (width: number) => {
        set({ sidebarWidth: Math.min(Math.max(width, 200), 480) })
      },

      setLogPrefs: (patch) => {
        set((state) => ({ logPrefs: { ...state.logPrefs, ...patch } }))
      },

      togglePinnedStack: (id) => {
        set((state) => ({
          pinnedStacks: state.pinnedStacks.includes(id)
            ? state.pinnedStacks.filter((p) => p !== id)
            : [...state.pinnedStacks, id],
        }))
      },

      isPinned: (id) => get().pinnedStacks.includes(id),

      resetLayout: () => {
        set({ sidebarOpen: DEFAULT_SIDEBAR_OPEN, sidebarWidth: DEFAULT_SIDEBAR_WIDTH })
        if (typeof window !== 'undefined') {
          for (const key of LAYOUT_STORAGE_KEYS) {
            localStorage.removeItem(key)
          }
        }
      },
    }),
    {
      name: 'ui-storage',
      // Deep-merge logPrefs so a stored blob from an older build (missing a
      // newly-added pref) still picks up the default for that key instead of
      // dropping it.
      merge: (persisted, current) => {
        const p = (persisted ?? {}) as Partial<UIState>
        return {
          ...current,
          ...p,
          logPrefs: { ...current.logPrefs, ...(p.logPrefs ?? {}) },
          pinnedStacks: p.pinnedStacks ?? current.pinnedStacks,
        }
      },
      onRehydrateStorage: () => (state) => {
        if (state) {
          applyTheme(state.theme)
        }
      },
    },
  ),
)
