import { useEffect, useEffectEvent, useRef, useState, useCallback } from 'react'
import { SearchAddon } from '@xterm/addon-search'
import { Button } from '@/components/ui/button'
import { Search, ChevronUp, ChevronDown, X } from 'lucide-react'

interface TerminalSearchBarProps {
  searchAddon: SearchAddon | null
  onClose: () => void
}

export function TerminalSearchBar({ searchAddon, onClose }: TerminalSearchBarProps) {
  const [query, setQuery] = useState('')
  const [matchCount, setMatchCount] = useState<number | null>(null)
  const [currentMatch, setCurrentMatch] = useState<number | null>(null)
  const inputRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    inputRef.current?.focus()
  }, [])

  const doSearch = useCallback(() => {
    if (!searchAddon || !query) return
    searchAddon.findNext(query, {
      regex: false,
      wholeWord: false,
      caseSensitive: false,
      incremental: true,
      decorations: {
        matchBackground: '#613214',
        matchBorder: '#e07a36',
        matchOverviewRuler: '#e07a36',
        activeMatchBackground: '#515c6a',
        activeMatchBorder: '#a8b4c4',
        activeMatchColorOverviewRuler: '#a8b4c4',
      },
    })
  }, [searchAddon, query])

  const findNext = useCallback(() => {
    if (!searchAddon || !query) return
    searchAddon.findNext(query)
  }, [searchAddon, query])

  const findPrev = useCallback(() => {
    if (!searchAddon || !query) return
    searchAddon.findPrevious(query)
  }, [searchAddon, query])

  useEffect(() => {
    if (!searchAddon) return
    const disposable = searchAddon.onDidChangeResults((e) => {
      setMatchCount(e?.resultCount ?? null)
      setCurrentMatch(e?.resultIndex !== undefined && e.resultIndex >= 0 ? e.resultIndex + 1 : null)
    })
    return () => disposable.dispose()
  }, [searchAddon])

  const clearSearch = useCallback(() => {
    searchAddon?.clearDecorations()
    searchAddon?.clearActiveDecoration()
  }, [searchAddon])

  // `doSearch` is only read inside the setTimeout sub-handler below, so
  // wrapping it in an Effect Event keeps this effect from re-running the
  // debounce timer on every render that changes doSearch's own deps — see
  // https://react.dev/reference/react/useEffectEvent
  const onSearchTimeout = useEffectEvent(() => {
    doSearch()
  })

  useEffect(() => {
    if (!query) {
      clearSearch()
      return
    }
    const timer = setTimeout(() => onSearchTimeout(), 150)
    return () => clearTimeout(timer)
  }, [query, searchAddon, clearSearch])

  return (
    <div className="flex items-center gap-2 rounded-lg border bg-background px-3 py-1.5">
      <label htmlFor="terminal-search-input" className="sr-only">Find in terminal</label>
      <Search className="h-4 w-4 shrink-0 text-muted-foreground" />
      <input
        id="terminal-search-input"
        ref={inputRef}
        type="text"
        value={query}
        onChange={(e) => setQuery(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Enter') {
            if (e.shiftKey) { findPrev() } else { findNext() }
          }
          if (e.key === 'Escape') {
            onClose()
          }
        }}
        placeholder="Find in terminal..."
        className="flex-1 bg-transparent text-sm outline-hidden placeholder:text-muted-foreground"
      />
      {matchCount !== null && (
        <span className="shrink-0 text-xs text-muted-foreground tabular-nums">
          {currentMatch ?? 0}/{matchCount}
        </span>
      )}
      <Button variant="ghost" size="sm" onClick={findPrev} disabled={!query} className="h-6 w-6 p-0" title="Previous match (Shift+Enter)">
        <ChevronUp className="h-3.5 w-3.5" />
      </Button>
      <Button variant="ghost" size="sm" onClick={findNext} disabled={!query} className="h-6 w-6 p-0" title="Next match (Enter)">
        <ChevronDown className="h-3.5 w-3.5" />
      </Button>
      <Button variant="ghost" size="sm" onClick={onClose} className="h-6 w-6 p-0" title="Close (Esc)">
        <X className="h-3.5 w-3.5" />
      </Button>
    </div>
  )
}
