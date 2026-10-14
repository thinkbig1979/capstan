import { lazy, Suspense, useCallback, useEffect, useLayoutEffect, useRef, useState, type ReactNode, type RefObject } from 'react'
import * as DialogPrimitive from '@radix-ui/react-dialog'
import { ArrowLeft, Keyboard, MapPin, X } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { LoadFailedNotice } from '@/components/LoadFailedNotice'
import { loadHelpLibrary, type HelpDoc } from './content'
import type { HelpRef } from './registry'
import { HELP_INDEX, useHelpStore, usePageHelpRef, type HelpView } from './helpStore'

// react-markdown and the docs stay out of the entry chunk: the renderer is
// lazy here and the docs sit behind loadHelpLibrary()'s dynamic import.
const HelpMarkdown = lazy(() => import('./HelpMarkdown').then((m) => ({ default: m.HelpMarkdown })))

// Developer-facing docs get their own group so operators meet them last (D93-1).
const DEVELOPER_DOCS = new Set(['reference/api', 'explanation/architecture'])

const INDEX_GROUPS: { label: string; matches: (doc: HelpDoc) => boolean }[] = [
  { label: 'Getting started', matches: (d) => d.group === 'start' },
  { label: 'Page help', matches: (d) => d.group === 'help' },
  { label: 'How-to', matches: (d) => d.group === 'how-to' },
  { label: 'Reference', matches: (d) => d.group === 'reference' && !DEVELOPER_DOCS.has(d.id) },
  { label: 'Explanation', matches: (d) => d.group === 'explanation' && !DEVELOPER_DOCS.has(d.id) },
  { label: 'For developers & automation', matches: (d) => DEVELOPER_DOCS.has(d.id) },
]

type LibraryState = { status: 'loading' } | { status: 'error' } | { status: 'ready'; docs: HelpDoc[] }

/** Loads the bundled docs the first time the panel opens; retry() re-runs a failed load. */
function useHelpLibrary(enabled: boolean) {
  const [state, setState] = useState<LibraryState>({ status: 'loading' })
  const [attempt, setAttempt] = useState(0)
  const started = enabled || attempt > 0

  useEffect(() => {
    if (!started) return
    let cancelled = false
    loadHelpLibrary().then(
      (docs) => !cancelled && setState({ status: 'ready', docs }),
      () => !cancelled && setState({ status: 'error' }),
    )
    return () => {
      cancelled = true
    }
  }, [started, attempt])

  const retry = useCallback(() => {
    setState({ status: 'loading' })
    setAttempt((n) => n + 1)
  }, [])

  return { state, retry }
}

function isDocRef(view: HelpView): view is HelpRef {
  return 'doc' in view
}

function LoadingHelp() {
  return (
    <p role="status" className="text-sm text-muted-foreground">
      Loading help…
    </p>
  )
}

/**
 * Scrolls the panel body to the view's heading once the doc has rendered, and
 * moves focus there after an in-panel jump. Rendered inside the same Suspense
 * boundary as the lazy renderer, so its effect runs after the headings exist.
 */
function FocusView({
  bodyRef,
  anchor,
  focusTargetRef,
  focusHeading,
}: {
  bodyRef: RefObject<HTMLDivElement | null>
  anchor: string | undefined
  focusTargetRef: RefObject<HTMLElement | null>
  focusHeading: boolean
}) {
  useLayoutEffect(() => {
    const body = bodyRef.current
    if (!body) return
    const heading = anchor
      ? body.querySelector<HTMLElement>(`[data-help-anchor="${CSS.escape(anchor)}"]`)
      : null
    // scrollTop on the panel body, never scrollIntoView (it scrolls the page too; agent-os-3a4p).
    body.scrollTop = heading ? heading.offsetTop : 0
    if (focusHeading) (heading ?? focusTargetRef.current)?.focus({ preventScroll: true })
  }, [bodyRef, anchor, focusTargetRef, focusHeading])
  return null
}

function GuideIndex({
  docs,
  onOpen,
  headingRef,
  focusHeading,
  bodyRef,
}: {
  docs: HelpDoc[]
  onOpen: (doc: string) => void
  headingRef: RefObject<HTMLHeadingElement | null>
  focusHeading: boolean
  bodyRef: RefObject<HTMLDivElement | null>
}) {
  return (
    <nav data-testid="help-panel-index" aria-labelledby="help-panel-index-heading" className="space-y-4">
      <h3 id="help-panel-index-heading" ref={headingRef} tabIndex={-1} className="text-lg font-semibold focus:outline-none">
        All guides
      </h3>
      {INDEX_GROUPS.map((group) => {
        const groupDocs = docs.filter(group.matches)
        if (groupDocs.length === 0) return null
        return (
          <section key={group.label} aria-label={group.label} data-testid="help-panel-index-group">
            <h4 className="mb-1 text-xs font-semibold uppercase tracking-wide text-muted-foreground">{group.label}</h4>
            <ul className="space-y-0.5">
              {groupDocs.map((doc) => (
                <li key={doc.id}>
                  <button
                    type="button"
                    className="text-left text-sm text-primary underline-offset-2 hover:underline"
                    onClick={() => onOpen(doc.id)}
                  >
                    {doc.title}
                  </button>
                </li>
              ))}
            </ul>
          </section>
        )
      })}
      <FocusView bodyRef={bodyRef} anchor={undefined} focusTargetRef={headingRef} focusHeading={focusHeading} />
    </nav>
  )
}

/**
 * The help side panel (agent-os-ejjf). Non-modal: the page stays usable while
 * reading, so clicks outside do not close it; Escape and Close do.
 */
export function HelpPanel() {
  const { open, current, history, focusHeading, followPage, close, navigate, back, openForPage, syncPage, setShortcutsOpen } =
    useHelpStore()
  const pageRef = usePageHelpRef()
  const { state, retry } = useHelpLibrary(open)

  const titleRef = useRef<HTMLHeadingElement>(null)
  const topicRef = useRef<HTMLParagraphElement>(null)
  const indexHeadingRef = useRef<HTMLHeadingElement>(null)
  const bodyRef = useRef<HTMLDivElement>(null)
  const openerRef = useRef<HTMLElement | null>(null)

  // Follow the page: a route change swaps the topic while followPage is on.
  useEffect(() => {
    syncPage(pageRef)
  }, [pageRef, syncPage])

  const onNavigate = useCallback((view: HelpView) => navigate(view), [navigate])

  const docs = state.status === 'ready' ? state.docs : []
  const doc = isDocRef(current) ? docs.find((d) => d.id === current.doc) : undefined
  const section = isDocRef(current) && current.anchor ? doc?.sections.find((s) => s.anchor === current.anchor) : undefined
  const anchorMissing = Boolean(doc && isDocRef(current) && current.anchor && !section)
  const docMissing = state.status === 'ready' && isDocRef(current) && !doc

  let body: ReactNode
  if (state.status === 'loading') {
    body = <LoadingHelp />
  } else if (state.status === 'error') {
    body = <LoadFailedNotice what="help" onRetry={retry} />
  } else if (doc && isDocRef(current)) {
    body = (
      <>
        {anchorMissing && (
          <p data-testid="help-panel-anchor-missing" role="status" className="mb-3 rounded-md border bg-muted px-3 py-2 text-sm">
            That section was not found; showing the whole guide.
          </p>
        )}
        <Suspense fallback={<LoadingHelp />}>
          <HelpMarkdown markdown={doc.markdown} docId={doc.id} onNavigate={onNavigate} />
          <FocusView
            key={JSON.stringify(current)}
            bodyRef={bodyRef}
            anchor={section?.anchor}
            focusTargetRef={topicRef}
            focusHeading={focusHeading}
          />
        </Suspense>
      </>
    )
  } else {
    body = (
      <>
        {docMissing && (
          <p data-testid="help-panel-doc-missing" role="status" className="mb-3 rounded-md border bg-muted px-3 py-2 text-sm">
            That guide was not found; showing all guides.
          </p>
        )}
        <GuideIndex
          docs={docs}
          onOpen={(id) => navigate({ doc: id })}
          headingRef={indexHeadingRef}
          focusHeading={focusHeading}
          bodyRef={bodyRef}
        />
      </>
    )
  }

  return (
    <DialogPrimitive.Root modal={false} open={open} onOpenChange={(next) => !next && close()}>
      <DialogPrimitive.Portal>
        <DialogPrimitive.Content
          data-testid="help-panel"
          aria-describedby={undefined}
          className="fixed inset-y-0 right-0 z-50 flex w-full flex-col border-l bg-background text-foreground shadow-lg focus:outline-none sm:max-w-md"
          onOpenAutoFocus={(e) => {
            // The opener, for focus return: Radix only returns focus to a Dialog.Trigger,
            // and this panel has several openers and no Trigger.
            openerRef.current = document.activeElement instanceof HTMLElement ? document.activeElement : null
            e.preventDefault()
            titleRef.current?.focus()
          }}
          onCloseAutoFocus={(e) => {
            e.preventDefault()
            const opener = openerRef.current
            openerRef.current = null
            // Only when focus was in the panel (and so fell to body as it closed);
            // never pull focus back from where the user has since moved it.
            const active = document.activeElement
            if (opener?.isConnected && (!active || active === document.body)) opener.focus()
          }}
          onInteractOutside={(e) => e.preventDefault()}
        >
          <div className="border-b px-4 py-3 pr-14">
            <DialogPrimitive.Title ref={titleRef} tabIndex={-1} className="text-base font-semibold focus:outline-none">
              Help
            </DialogPrimitive.Title>
            {doc && (
              <p
                ref={topicRef}
                tabIndex={-1}
                data-testid="help-panel-topic-heading"
                className="truncate text-sm text-muted-foreground focus:outline-none"
              >
                {section?.heading ?? doc.title}
              </p>
            )}
            <div className="mt-2 flex flex-wrap gap-2">
              <Button
                type="button"
                variant="outline"
                size="sm"
                data-testid="help-panel-this-page-button"
                aria-pressed={followPage}
                onClick={() => openForPage(pageRef)}
              >
                <MapPin className="h-4 w-4" aria-hidden="true" />
                This page
              </Button>
              <Button
                type="button"
                variant="outline"
                size="sm"
                data-testid="help-panel-back-button"
                disabled={history.length === 0}
                onClick={back}
              >
                <ArrowLeft className="h-4 w-4" aria-hidden="true" />
                Back
              </Button>
              <Button type="button" variant="outline" size="sm" onClick={() => navigate(HELP_INDEX)}>
                All guides
              </Button>
            </div>
          </div>

          <div ref={bodyRef} data-testid="help-panel-body" className="relative flex-1 overflow-y-auto px-4 py-4">
            {body}
          </div>

          <div className="border-t px-4 py-2">
            <Button type="button" variant="ghost" size="sm" onClick={() => setShortcutsOpen(true)}>
              <Keyboard className="h-4 w-4" aria-hidden="true" />
              Keyboard shortcuts
            </Button>
          </div>

          {/* Last in DOM order so Tab reaches it after the footer; shown top right. */}
          <DialogPrimitive.Close
            data-testid="help-panel-close-button"
            className="absolute right-1 top-1 inline-flex h-11 w-11 items-center justify-center rounded-md text-muted-foreground hover:bg-accent hover:text-foreground focus:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          >
            <X className="h-4 w-4" aria-hidden="true" />
            <span className="sr-only">Close help</span>
          </DialogPrimitive.Close>
        </DialogPrimitive.Content>
      </DialogPrimitive.Portal>
    </DialogPrimitive.Root>
  )
}
