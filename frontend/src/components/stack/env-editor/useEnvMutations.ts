import { useEffect, useRef, type Dispatch, type SetStateAction } from 'react'
import { stacksApi } from '@/lib/api'
import { useActionMutation } from '@/hooks/useActionMutation'
import type { EnvEntryRow } from './types'
import { queryKeys } from '@/lib/query-keys'

interface UseEnvMutationsArgs {
  stackId: string
  entries: EnvEntryRow[]
  rawContent: string
  setEntries: Dispatch<SetStateAction<EnvEntryRow[]>>
  setRawContent: Dispatch<SetStateAction<string>>
  setHasUnsavedChanges: Dispatch<SetStateAction<boolean>>
  setShowEnvSection: Dispatch<SetStateAction<boolean>>
  resetHistory: (entries: EnvEntryRow[], raw: string) => void
}

/**
 * Save + create-env-file mutations, wired through useActionMutation so real
 * ActionResult outcomes (success/no_change/partial/failed) surface correctly
 * instead of a blanket "Saved" toast (audit finding #15), and so the
 * previously-dead "Create Environment File" button actually reveals the
 * editor on success (audit finding #16).
 */
export function useEnvMutations({
  stackId,
  entries,
  rawContent,
  setEntries,
  setRawContent,
  setHasUnsavedChanges,
  setShowEnvSection,
  resetHistory,
}: UseEnvMutationsArgs) {
  // What the editor held when Save was clicked, and what was sent. onResult
  // closes over the render that clicked, so "does the editor still hold the
  // saved text" needs the latest render's state, read through a ref.
  const latest = useRef({ entries, rawContent })
  useEffect(() => {
    latest.current = { entries, rawContent }
  })
  const savedRef = useRef<{
    body: { entries?: EnvEntryRow[]; raw?: string }
    snapshot: { entries: EnvEntryRow[]; rawContent: string }
  } | null>(null)

  const saveMutation = useActionMutation({
    mutationFn: async (body: { entries?: EnvEntryRow[]; raw?: string }) => {
      // _rowId is a client-only synthetic key for React row identity (see
      // env-editor/types.ts) — strip it here, right before the network call,
      // so it never reaches the saved env output. `body` itself (with
      // _rowId intact) is what onResult's resetHistory snapshots below, so
      // undo/redo after a save keeps stable row ids.
      const wireBody = body.entries
        ? { ...body, entries: body.entries.map(({ _rowId, ...rest }) => rest) }
        : body
      return stacksApi.updateEnv(stackId, wireBody)
    },
    invalidate: [queryKeys.stack.detail(stackId)],
    successTitle: 'Environment variables saved',
    onResult: (result) => {
      if (result.outcome === 'success' || result.outcome === 'no_change') {
        // Clean only if neither the rows nor the raw text moved since Save was
        // clicked (agent-os-3761). Typing that landed while the save was in
        // flight is not on disk: the editor stays dirty, and the save's own
        // refetch is then recognised through savedRef (EnvEditor.tsx).
        const saved = savedRef.current
        if (
          saved &&
          latest.current.entries === saved.snapshot.entries &&
          latest.current.rawContent === saved.snapshot.rawContent
        ) {
          setHasUnsavedChanges(false)
          resetHistory(saved.body.entries || saved.snapshot.entries, saved.body.raw || saved.snapshot.rawContent)
        }
      }
    },
  })

  const createEnvMutation = useActionMutation<void>({
    mutationFn: (_vars: void) => stacksApi.createEnv(stackId),
    invalidate: [queryKeys.stack.env(stackId), queryKeys.stack.detail(stackId)],
    successTitle: 'Environment file created',
    onResult: (result) => {
      if (result.outcome === 'success' || result.outcome === 'no_change') {
        // Reveal the editor immediately — the query invalidation above will
        // re-fetch and populate entries/raw.
        setShowEnvSection(true)
        setEntries([])
        setRawContent('')
        resetHistory([], '')
        setHasUnsavedChanges(false)
      }
    },
  })

  const handleSaveTable = () => {
    savedRef.current = { body: { entries }, snapshot: { entries, rawContent } }
    saveMutation.mutate({ entries })
  }

  const handleSaveRaw = () => {
    savedRef.current = { body: { raw: rawContent }, snapshot: { entries, rawContent } }
    saveMutation.mutate({ raw: rawContent })
  }

  return { saveMutation, createEnvMutation, handleSaveTable, handleSaveRaw, savedRef }
}
