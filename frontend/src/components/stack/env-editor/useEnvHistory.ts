import { useCallback, useEffect, useEffectEvent, useState, type Dispatch, type SetStateAction } from 'react'
import type { EnvEntry } from '@/types'

const MAX_HISTORY = 50

interface UseEnvHistoryArgs {
  setEntries: Dispatch<SetStateAction<EnvEntry[]>>
  setRawContent: Dispatch<SetStateAction<string>>
  setHasUnsavedChanges: Dispatch<SetStateAction<boolean>>
}

/**
 * Owns the undo/redo history stack for EnvEditor: the table/raw snapshots,
 * push/undo/redo, and the Ctrl+Z / Ctrl+Y (or Ctrl+Shift+Z) keyboard
 * shortcuts. `resetHistory` is what the query-hydration and save/create
 * mutation paths call to collapse the stack back to a single entry.
 */
export function useEnvHistory({ setEntries, setRawContent, setHasUnsavedChanges }: UseEnvHistoryArgs) {
  const [history, setHistory] = useState<{ entries: EnvEntry[]; raw: string }[]>([])
  const [historyIndex, setHistoryIndex] = useState(-1)

  const pushToHistory = useCallback(
    (newEntries: EnvEntry[], newRaw: string) => {
      if (historyIndex >= 0) {
        const currentState = history[historyIndex]
        if (
          JSON.stringify(currentState.entries) === JSON.stringify(newEntries) &&
          currentState.raw === newRaw
        ) {
          return
        }
      }

      const newHistory = history.slice(0, historyIndex + 1)
      newHistory.push({ entries: newEntries, raw: newRaw })
      if (newHistory.length > MAX_HISTORY) {
        newHistory.shift()
      }
      setHistory(newHistory)
      setHistoryIndex(newHistory.length - 1)
    },
    [history, historyIndex],
  )

  const resetHistory = useCallback((entries: EnvEntry[], raw: string) => {
    setHistory([{ entries, raw }])
    setHistoryIndex(0)
  }, [])

  const handleUndo = useCallback(() => {
    if (historyIndex > 0) {
      const prev = history[historyIndex - 1]
      setEntries(prev.entries)
      setRawContent(prev.raw)
      setHistoryIndex((prev) => prev - 1)
      setHasUnsavedChanges(true)
    }
  }, [history, historyIndex, setEntries, setRawContent, setHasUnsavedChanges])

  const handleRedo = useCallback(() => {
    if (historyIndex < history.length - 1) {
      const next = history[historyIndex + 1]
      setEntries(next.entries)
      setRawContent(next.raw)
      setHistoryIndex((prev) => prev + 1)
      setHasUnsavedChanges(true)
    }
  }, [history, historyIndex, setEntries, setRawContent, setHasUnsavedChanges])

  // `handleUndo`/`handleRedo` are only read inside the keydown sub-handler
  // below, so wrapping them in Effect Events keeps this effect from
  // re-subscribing the window listener on every history change — see
  // https://react.dev/reference/react/useEffectEvent
  const onUndoShortcut = useEffectEvent(() => {
    handleUndo()
  })
  const onRedoShortcut = useEffectEvent(() => {
    handleRedo()
  })

  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key === 'z' && !e.shiftKey) {
        e.preventDefault()
        onUndoShortcut()
      } else if ((e.ctrlKey || e.metaKey) && (e.key === 'y' || (e.key === 'z' && e.shiftKey))) {
        e.preventDefault()
        onRedoShortcut()
      }
    }
    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [])

  return {
    historyIndex,
    historyLength: history.length,
    pushToHistory,
    handleUndo,
    handleRedo,
    resetHistory,
  }
}
