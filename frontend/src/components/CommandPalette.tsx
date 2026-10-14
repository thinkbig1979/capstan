import { useEffect, useMemo, useState, useCallback } from 'react'
import { defaultFilter } from 'cmdk'
import { useNavigate } from 'react-router'
import { useQuery } from '@tanstack/react-query'
import { CircleHelp, LayoutDashboard, Layers, Loader2, Search, Settings } from 'lucide-react'
import { stacksApi } from '@/lib/api'
import {
  CommandDialog,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
  CommandSeparator,
} from '@/components/ui/command'
import { queryKeys } from '@/lib/query-keys'
import { LoadFailedNotice } from '@/components/LoadFailedNotice'
import { loadHelpLibrary, type HelpDoc } from '@/help/content'
import { useHelpStore, usePageHelpRef } from '@/help/helpStore'

type HelpLibrary = { status: 'loading' } | { status: 'error' } | { status: 'ready'; docs: HelpDoc[] }

/**
 * The help library, loaded each time the palette opens (loadHelpLibrary() is
 * memoised, and forgets a failure, so a reopen retries).
 */
function useHelpLibraryWhileOpen(open: boolean): HelpLibrary {
  const [library, setLibrary] = useState<HelpLibrary>({ status: 'loading' })

  useEffect(() => {
    if (!open) return
    let cancelled = false
    loadHelpLibrary().then(
      (docs) => !cancelled && setLibrary({ status: 'ready', docs }),
      () => !cancelled && setLibrary({ status: 'error' }),
    )
    return () => {
      cancelled = true
    }
  }, [open])

  return library
}

// Two docs can share a heading ("Git" in the page help and in the guides), and cmdk
// highlights every item with the same value together, so a repeated heading is qualified.
function sectionValues(docs: HelpDoc[]): { doc: HelpDoc; section: HelpDoc['sections'][number]; value: string }[] {
  const counts = new Map<string, number>()
  for (const d of docs) for (const s of d.sections) counts.set(s.heading, (counts.get(s.heading) ?? 0) + 1)
  return docs.flatMap((doc) =>
    doc.sections.map((section) => ({
      doc,
      section,
      value: counts.get(section.heading) === 1 ? section.heading : `${section.heading} (${doc.title})`,
    })),
  )
}

export function CommandPalette() {
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')
  const navigate = useNavigate()
  const pageHelpRef = usePageHelpRef()
  const openForPage = useHelpStore((s) => s.openForPage)
  const openAt = useHelpStore((s) => s.openAt)
  const openSearch = useHelpStore((s) => s.openSearch)
  const helpLibrary = useHelpLibraryWhileOpen(open)
  const helpSections = useMemo(() => (helpLibrary.status === 'ready' ? sectionValues(helpLibrary.docs) : []), [helpLibrary])
  const searching = query.trim() !== ''
  // Matches what cmdk will show for the typed text, so the search-all item appears only beside real section items.
  const offerSearchAll =
    searching && helpSections.some(({ doc, value }) => defaultFilter(value, query.trim(), [doc.title]) > 0)

  const { data, isPending, isError, refetch } = useQuery({
    queryKey: queryKeys.stacks(),
    queryFn: stacksApi.list,
  })
  const stacks = data ?? []
  // Without this a failed load leaves only "No results found." for a stack
  // search, as if the stack did not exist (agent-os-kdqm).
  const stacksLoadFailed = isError && !data
  // No `enabled` on this query, so plain isPending is the whole answer: it is
  // true while the first fetch runs AND while it is paused offline, which
  // isLoading is not (agent-os-hzjh, agent-os-7nqa).
  const stacksLoading = isPending

  const handleClose = useCallback(() => {
    setOpen(false)
    setQuery('')
  }, [])

  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === 'k') {
        // Always open the palette on Ctrl-K / Cmd-K, even from inputs
        e.preventDefault()
        setOpen((prev) => !prev)
        setQuery('')
      }
    }

    document.addEventListener('keydown', onKeyDown)
    return () => document.removeEventListener('keydown', onKeyDown)
  }, [])

  const runCommand = useCallback(
    (fn: () => void) => {
      handleClose()
      fn()
    },
    [handleClose],
  )

  return (
    <CommandDialog open={open} onOpenChange={(next) => (next ? setOpen(true) : handleClose())}>
      <CommandInput placeholder="Search stacks, navigate..." value={query} onValueChange={setQuery} />
      <CommandList>
        {stacksLoadFailed && (
          <LoadFailedNotice what="the stack list" onRetry={() => void refetch()} className="m-2" />
        )}
        {stacksLoading ? (
          // "No results found." would claim the stack does not exist when the
          // list has not been read yet, so it is not rendered until it has.
          <div role="status" className="flex items-center justify-center gap-2 py-6 text-sm text-muted-foreground">
            <Loader2 className="h-4 w-4 animate-spin" aria-hidden="true" />
            Loading stacks...
          </div>
        ) : (
          <CommandEmpty>No results found.</CommandEmpty>
        )}

        {stacks.length > 0 && (
          <>
            <CommandGroup heading="Stacks">
              {stacks.map((stack) => (
                <CommandItem
                  key={stack.id}
                  value={stack.projectName}
                  onSelect={() => runCommand(() => navigate(`/stacks/${stack.id}`))}
                >
                  <Layers className="mr-2 h-4 w-4 shrink-0 text-muted-foreground" />
                  {stack.projectName}
                </CommandItem>
              ))}
            </CommandGroup>
            <CommandSeparator />
          </>
        )}

        <CommandGroup heading="Navigation">
          <CommandItem
            value="Dashboard home"
            onSelect={() => runCommand(() => navigate('/'))}
          >
            <LayoutDashboard className="mr-2 h-4 w-4 shrink-0 text-muted-foreground" />
            Dashboard
          </CommandItem>
          <CommandItem
            value="Settings"
            onSelect={() => runCommand(() => navigate('/settings'))}
          >
            <Settings className="mr-2 h-4 w-4 shrink-0 text-muted-foreground" />
            Settings
          </CommandItem>
        </CommandGroup>

        {/*
          cmdk facts this group is built around (observed in cmdk 1.1.1, not in its docs):
          - a group's forceMount reaches every item in it, and an item's explicit
            forceMount={false} overrides that, so the sections stay filtered;
          - a force-mounted item is never rescored, so it sorts before real matches and
            would take the highlight from them. Only the disabled loading/failed rows are
            force-mounted; the actionable items are real, filtered items;
          - a force-mounted group stays visible with no match, so the loading row shows
            while a query is typed. Once the library is ready the group filters normally,
            so "No results found." is not accompanied by an empty "Help" heading.
        */}
        <CommandSeparator />
        <CommandGroup heading="Help" forceMount={helpLibrary.status !== 'ready'}>
          {helpLibrary.status === 'loading' && (
            <CommandItem forceMount disabled value="help-loading">
              <Loader2 className="mr-2 h-4 w-4 shrink-0 animate-spin text-muted-foreground" aria-hidden="true" />
              Loading help…
            </CommandItem>
          )}
          {helpLibrary.status === 'error' && (
            <CommandItem forceMount disabled value="help-load-failed">
              Help could not be loaded
            </CommandItem>
          )}
          <CommandItem
            forceMount={false}
            value="Open help for this page"
            keywords={['help', 'page']}
            onSelect={() => runCommand(() => openForPage(pageHelpRef))}
          >
            <CircleHelp className="mr-2 h-4 w-4 shrink-0 text-muted-foreground" />
            Open help for this page
          </CommandItem>
          {searching &&
            helpSections.map(({ doc, section, value }) => (
              <CommandItem
                key={`${doc.id}#${section.anchor}`}
                forceMount={false}
                value={value}
                keywords={[doc.title]}
                onSelect={() => runCommand(() => openAt({ doc: doc.id, anchor: section.anchor }))}
              >
                <span className="truncate">{section.heading}</span>
                <span className="ml-auto shrink-0 pl-2 text-xs text-muted-foreground">{doc.title}</span>
              </CommandItem>
            ))}
        </CommandGroup>
        {offerSearchAll && (
          // Its own group so cmdk sorts it after the group holding the real matches. Offered
          // only when a section heading matched: a word that appears only in body text has
          // no item to sit beside, and "No results found." stays honest for stacks.
          <CommandGroup>
            <CommandItem
              forceMount={false}
              value="Search all help"
              keywords={[query.trim()]}
              onSelect={() => runCommand(() => openSearch(query.trim()))}
            >
              <Search className="mr-2 h-4 w-4 shrink-0 text-muted-foreground" />
              Search all help for '{query.trim()}'
            </CommandItem>
          </CommandGroup>
        )}
      </CommandList>
    </CommandDialog>
  )
}
