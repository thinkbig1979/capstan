import { create } from 'zustand'
import { PAGE_HELP, type HelpRef } from './registry'
import { usePageHelpContext } from './page-context'

/** What the panel body shows: a doc (at an optional heading), the guide index, or search results. */
export type HelpView = HelpRef | { kind: 'index' } | { kind: 'search'; q: string }

export const HELP_INDEX: HelpView = { kind: 'index' }

interface HelpState {
  open: boolean
  /** While true, a route change swaps the topic to the new page's topic. */
  followPage: boolean
  current: HelpView
  /** Views left by in-panel navigation, most recent last; Back pops one. */
  history: HelpView[]
  /**
   * Where focus lands when the view renders: the topic heading after a jump to
   * a section (a link, a field hint), the panel title after opening on the page.
   */
  focusHeading: boolean
  /** The keyboard-shortcuts dialog, lifted here so the panel footer can open it. */
  shortcutsOpen: boolean

  /** Open on the current page's topic (null: the page has none, show the index). */
  openForPage: (ref: HelpRef | null) => void
  /** Open at a topic chosen elsewhere (a field hint, the palette). */
  openAt: (ref: HelpRef) => void
  /** Navigate inside the panel; stops following the page. */
  navigate: (view: HelpView) => void
  /** The page changed: follow it if the panel is still on the page's topic. */
  syncPage: (ref: HelpRef | null) => void
  back: () => void
  close: () => void
  setShortcutsOpen: (open: boolean) => void
}

// Not persisted: help state belongs to this tab's visit, not to the user, so
// there is nothing to clear on logout; it ends when AppShell unmounts or the tab closes.
export const useHelpStore = create<HelpState>()((set) => ({
  open: false,
  followPage: true,
  current: HELP_INDEX,
  history: [],
  focusHeading: false,

  shortcutsOpen: false,

  openForPage: (ref) =>
    set({ open: true, followPage: true, current: ref ?? HELP_INDEX, history: [], focusHeading: false }),

  openAt: (ref) =>
    set((s) => ({
      open: true,
      followPage: false,
      current: ref,
      history: s.open ? [...s.history, s.current] : [],
      focusHeading: true,
    })),

  navigate: (view) =>
    set((s) => ({ followPage: false, current: view, history: [...s.history, s.current], focusHeading: true })),

  syncPage: (ref) =>
    set((s) => (s.open && s.followPage ? { current: ref ?? HELP_INDEX, focusHeading: false } : {})),

  back: () =>
    set((s) => {
      const previous = s.history[s.history.length - 1]
      if (!previous) return {}
      return { current: previous, history: s.history.slice(0, -1), focusHeading: true }
    }),

  close: () => set({ open: false }),

  setShortcutsOpen: (open) => set({ shortcutsOpen: open }),
}))

/** The current page's help topic, or null when the location has none. */
export function usePageHelpRef(): HelpRef | null {
  const key = usePageHelpContext()
  return key ? PAGE_HELP[key] : null
}
