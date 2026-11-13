import { useEffect, useRef, useMemo } from 'react'
// The aggregate `codemirror` package is kept deliberately, not by omission
// (agent-os-bhyn considered dropping it in favour of the eight individual
// @codemirror/* packages this file's neighbours already import).
//
// EditorView is also exported by @codemirror/view, but `basicSetup` exists only
// here and is 18 curated extensions plus a keymap composed from seven of them.
// Inlining it would mean owning that list and tracking upstream changes to it by
// hand, and it would ADD @codemirror/commands and @codemirror/language as direct
// dependencies — a net increase, not a reduction.
//
// It also would not close the version-split hazard it looks like it should.
// Measured 2026-08-08: six installed packages depend on @codemirror/language —
// this aggregate plus autocomplete, commands, lang-json, lang-yaml and
// theme-one-dark — so dropping the aggregate removes one of six participants and
// leaves the failure mode (agent-os-l1hn: two @codemirror/language copies, facet
// identity mismatch, YAML silently unhighlighted) fully reachable. The control for
// that is hooks/__tests__/codemirror-yaml-highlighting.test.ts, which mocks
// nothing on purpose, plus the family-declared-together convention recorded in
// knip.jsonc's ignoreDependencies entry.
import { EditorView, basicSetup } from 'codemirror'
import { Compartment, EditorState, type Extension } from '@codemirror/state'
import { yaml } from '@codemirror/lang-yaml'
import { oneDark } from '@codemirror/theme-one-dark'
import { keymap, ViewPlugin } from '@codemirror/view'
import { search } from '@codemirror/search'
import { autocompletion } from '@codemirror/autocomplete'
import { useUIStore } from '@/stores/uiStore'

interface UseCodeMirrorEditorOptions {
  doc: string
  onSave?: () => boolean
  onChange?: (content: string) => void
  onSelect?: (text: string) => void
  // Accessible name for the editor's textbox; CodeMirror renders an unnamed
  // contenteditable otherwise (axe aria-input-field-name).
  ariaLabel?: string
  /**
   * The document cannot be edited (CodeMirror sets aria-readonly). Switched on
   * the live view, without a rebuild: the compose editor turns read-only while
   * it shows masked secrets (agent-os-w6wx).
   */
  readOnly?: boolean
  /**
   * Changes with every load the caller pushes into the view, so a load whose
   * text equals the previous load still replaces a view that has moved on
   * (load A, edit and save A', A comes back from disk).
   */
  docKey?: unknown
  deps?: React.DependencyList
}

// Editable is CodeMirror's default, so only the read-only side adds anything.
function readOnlyExtension(readOnly: boolean): Extension {
  return readOnly ? [EditorState.readOnly.of(true), EditorView.editable.of(false)] : []
}

// CodeMirror 6 creates its scroller with tabindex="-1" (@codemirror/view 6.43.13
// and 6.43.14, dist/index.js `this.scrollDOM.tabIndex = -1`), which axe reports as
// scrollable-region-focusable (serious). Making the scroller a tab stop is axe's
// own remedy; the cost is one extra Tab stop before the editor content
// (agent-os-25k4, F2). Measured: tabIndex = 0 on `.cm-scroller` clears the finding.
const focusableScroller = ViewPlugin.define((view) => {
  view.scrollDOM.tabIndex = 0
  return {}
})

function themeExtension(isDark: boolean): Extension {
  return isDark ? oneDark : []
}

export function useCodeMirrorEditor(
  containerRef: React.RefObject<HTMLDivElement | null>,
  options: UseCodeMirrorEditorOptions,
) {
  const viewRef = useRef<EditorView | null>(null)
  const { theme } = useUIStore()
  const onSaveRef = useRef(options.onSave)
  const onChangeRef = useRef(options.onChange)
  const onSelectRef = useRef(options.onSelect)
  // The theme lives in a Compartment so a theme change reconfigures the live
  // view instead of rebuilding it: a rebuild starts from `options.doc` and
  // loses unsaved edits, the cursor and the undo history (agent-os-z91e.41).
  // One Compartment per view, created with it.
  const themeCompartmentRef = useRef<Compartment | null>(null)
  const isDarkRef = useRef(false)
  // The theme the live view was last given, so the reconfigure effect below
  // stays silent on mount and after a rebuild, when the view is already right.
  const appliedDarkRef = useRef(false)
  // The same pair for readOnly: the view is built with readOnlyRef's value
  // and reconfigured when the option changes.
  const readOnlyCompartmentRef = useRef<Compartment | null>(null)
  const readOnlyRef = useRef(options.readOnly ?? false)
  const appliedReadOnlyRef = useRef(false)
  // Builds a state for the live view with the given doc and selection: the
  // view's extensions, its compartments as currently applied, and a fresh
  // undo history.
  const makeStateRef = useRef<((doc: string, selection?: { anchor: number; head: number }) => EditorState) | null>(null)

  useEffect(() => {
    readOnlyRef.current = options.readOnly ?? false
    onSaveRef.current = options.onSave
    onChangeRef.current = options.onChange
    onSelectRef.current = options.onSelect
  })

  const isDark = useMemo(
    () =>
      theme === 'dark' ||
      (theme === 'system' &&
        typeof window !== 'undefined' &&
        window.matchMedia('(prefers-color-scheme: dark)').matches),
    [theme],
  )

  useEffect(() => {
    isDarkRef.current = isDark
  })

  useEffect(() => {
    if (!containerRef.current) return

    const themeCompartment = new Compartment()
    themeCompartmentRef.current = themeCompartment

    const extensions = [
      basicSetup,
      yaml(),
      keymap.of([
        {
          key: 'Mod-s',
          run: () => onSaveRef.current?.() ?? true,
        },
      ]),
      search({ top: true }),
      focusableScroller,
      autocompletion(),
      EditorView.contentAttributes.of({
        'aria-label': options.ariaLabel ?? 'Code editor',
      }),
      EditorView.theme({
        '&': { fontSize: '14px' },
        '.cm-scroller': {
          fontFamily:
            'ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace',
        },
      }),
    ]

    appliedDarkRef.current = isDarkRef.current

    const readOnlyCompartment = new Compartment()
    readOnlyCompartmentRef.current = readOnlyCompartment
    appliedReadOnlyRef.current = readOnlyRef.current

    if (onChangeRef.current || onSelectRef.current) {
      extensions.push(
        EditorView.updateListener.of((update) => {
          if (update.docChanged && onChangeRef.current) {
            onChangeRef.current(update.state.doc.toString())
          }
          if (update.selectionSet || update.docChanged) {
            const sel = update.state.selection.main
            if (onSelectRef.current) {
              onSelectRef.current(
                sel.from !== sel.to
                  ? update.state.sliceDoc(sel.from, sel.to)
                  : '',
              )
            }
          }
        }),
      )
    }

    // The compartments are given what is applied now, so a state built
    // after a theme or readOnly change keeps it.
    const makeState = (doc: string, selection?: { anchor: number; head: number }) =>
      EditorState.create({
        doc,
        selection,
        extensions: [
          ...extensions,
          themeCompartment.of(themeExtension(appliedDarkRef.current)),
          readOnlyCompartment.of(readOnlyExtension(appliedReadOnlyRef.current)),
        ],
      })
    makeStateRef.current = makeState

    const view = new EditorView({
      state: makeState(options.doc),
      parent: containerRef.current,
    })

    viewRef.current = view

    return () => {
      view.destroy()
      viewRef.current = null
      themeCompartmentRef.current = null
      readOnlyCompartmentRef.current = null
      makeStateRef.current = null
    }
    // The view is rebuilt only when the caller's deps change (a different file,
    // a remounted container); the theme, doc and callbacks reach the live view
    // through the Compartment, the effect below and refs.
  }, [...(options.deps || [])]) // eslint-disable-line react-hooks/exhaustive-deps -- the deps are the caller's list, spread; doc, theme and callbacks reach the live view without a rebuild

  useEffect(() => {
    const view = viewRef.current
    const compartment = themeCompartmentRef.current
    if (!view || !compartment || appliedDarkRef.current === isDark) return
    appliedDarkRef.current = isDark
    view.dispatch({ effects: compartment.reconfigure(themeExtension(isDark)) })
  }, [isDark])

  const readOnly = options.readOnly ?? false
  useEffect(() => {
    const view = viewRef.current
    const compartment = readOnlyCompartmentRef.current
    if (!view || !compartment || appliedReadOnlyRef.current === readOnly) return
    appliedReadOnlyRef.current = readOnly
    view.dispatch({ effects: compartment.reconfigure(readOnlyExtension(readOnly)) })
  }, [readOnly])

  // A doc pushed in from outside (a load from the server, a re-lock, an
  // external change) replaces the view's state rather than editing it, so
  // the replacement is not an undo step: undo after an unlock must never
  // bring masked "***" text back into an editable editor (agent-os-w6wx).
  // The cursor (clamped) and the scroll position carry over. A selection
  // does not: it was made in the old text (possibly masked "***"), so it is
  // collapsed and onSelect told, or the caller keeps acting on stale text
  // (review r2 R2-D2: Extract to .env after unlock wrote VAR=***).
  useEffect(() => {
    const view = viewRef.current
    const makeState = makeStateRef.current
    if (!view || !makeState) return
    if (options.doc === view.state.doc.toString()) return
    const cursor = Math.min(view.state.selection.main.head, options.doc.length)
    const scrollTop = view.scrollDOM.scrollTop
    view.setState(makeState(options.doc, { anchor: cursor, head: cursor }))
    view.scrollDOM.scrollTop = scrollTop
    onSelectRef.current?.('')
  }, [options.doc, options.docKey])

  return { viewRef, isDark }
}
