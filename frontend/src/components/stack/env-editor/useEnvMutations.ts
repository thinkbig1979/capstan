import type { Dispatch, SetStateAction } from 'react'
import { stacksApi } from '@/lib/api'
import { isActionResult } from '@/lib/action-result'
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
      const raw = await stacksApi.updateEnv(stackId, wireBody)
      // Migration bridge: legacy backend returns {saved, filename}; new backend
      // returns ActionResult. Map the legacy shape so toastForResult works either way.
      if (isActionResult(raw)) {
        return raw
      }
      const legacy = raw as { saved: boolean; filename?: string }
      if (legacy.saved) {
        return { outcome: 'success' as const, reason: 'Environment variables saved' }
      }
      return { outcome: 'failed' as const, reason: 'Failed to save environment variables' }
    },
    invalidate: [queryKeys.stack.detail(stackId)],
    successTitle: 'Environment variables saved',
    onResult: (result) => {
      if (result.outcome === 'success' || result.outcome === 'no_change') {
        setHasUnsavedChanges(false)
        const body = saveMutation.variables
        if (body) {
          resetHistory(body.entries || entries, body.raw || rawContent)
        }
      }
    },
  })

  const createEnvMutation = useActionMutation<void>({
    mutationFn: async (_vars: void) => {
      const raw = await stacksApi.createEnv(stackId)
      if (isActionResult(raw)) return raw
      return { outcome: 'success' as const, reason: 'Environment file created' }
    },
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
    saveMutation.mutate({ entries })
  }

  const handleSaveRaw = () => {
    saveMutation.mutate({ raw: rawContent })
  }

  return { saveMutation, createEnvMutation, handleSaveTable, handleSaveRaw }
}
