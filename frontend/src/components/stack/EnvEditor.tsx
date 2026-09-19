import { useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { stacksApi } from '@/lib/api'
import { useEnvUnlockStore } from '@/stores/envUnlockStore'
import { EnvUnlockDialog } from '@/components/EnvUnlockDialog'
import { useAuth } from '@/hooks/useAuth'
import { useEnvHistory } from './env-editor/useEnvHistory'
import { useEnvUnlockRemask } from './env-editor/useEnvUnlockRemask'
import { useEnvMutations } from './env-editor/useEnvMutations'
import { useEnvEntryActions } from './env-editor/useEnvEntryActions'
import { useNextRowId } from './env-editor/useRowId'
import { isSensitiveKey } from './env-editor/sensitiveKey'
import { EnvEditorToolbar } from './env-editor/EnvEditorToolbar'
import { EnvLoadingState, EnvErrorState, EnvNoFileState, EnvLockedNotice } from './env-editor/EnvEditorEmptyStates'
import { EnvTableView } from './env-editor/EnvTableView'
import { EnvRawView } from './env-editor/EnvRawView'
import type { EnvEntryRow } from './env-editor/types'
import { queryKeys } from '@/lib/query-keys'
import { classifyError } from '@/lib/error-handler'
import { RefreshFailedNotice } from '@/components/RefreshFailedNotice'

interface EnvEditorProps {
  stackId: string
}

export function EnvEditor({ stackId }: EnvEditorProps) {
  const queryClient = useQueryClient()
  const { authDisabled } = useAuth()
  const isUnlocked = useEnvUnlockStore((s) => s.isUnlocked)
  const unlockedUntil = useEnvUnlockStore((s) => s.unlockedUntil)
  const [view, setView] = useState<'table' | 'raw'>('table')
  const [entries, setEntries] = useState<EnvEntryRow[]>([])
  const [rawContent, setRawContent] = useState('')
  const [hasUnsavedChanges, setHasUnsavedChanges] = useState(false)
  const [showEnvSection, setShowEnvSection] = useState(false)
  const nextRowId = useNextRowId()

  const { historyIndex, historyLength, pushToHistory, handleUndo, handleRedo, resetHistory } =
    useEnvHistory({ setEntries, setRawContent, setHasUnsavedChanges })

  // Re-masks sensitive-by-name entries when the unlock session ends (manual
  // lock or auto-expiry) — see useEnvUnlockRemask for the render-time detail.
  useEnvUnlockRemask(unlockedUntil, setEntries)

  const {
    unlockDialogOpen,
    handleUnlockDialogOpenChange,
    handleAddEntry,
    handleDeleteEntry,
    handleEntryChange,
    handleRawChange,
    toggleVisibility,
    handleUnlocked,
  } = useEnvEntryActions({
    entries,
    setEntries,
    rawContent,
    setRawContent,
    pushToHistory,
    setHasUnsavedChanges,
    authDisabled,
    isUnlocked,
    nextRowId,
  })

  // No 404 branch. "This stack has no env file" arrives as a 200 carrying
  // `hasEnvFile: false` (agent-os-bt5y), so a catch-404-return-null here would
  // not be dead code — it would be live and wrong. Two states still answer
  // 404: an unknown stack, and a configured env file missing from disk. It
  // would swallow both and render them as "no env file", hiding a real fault
  // behind an empty editor. Only the no-env-file 404 stopped existing; those
  // two belong in isError.
  const { data: envData, isLoading, isError, error } = useQuery({
    queryKey: queryKeys.stack.env(stackId),
    queryFn: () => stacksApi.getEnv(stackId),
  })

  // Hydrate local editable state whenever a new envData query result arrives.
  // Adjusted during render (rather than in an effect) by comparing against the
  // envData reference from the previous render — see
  // https://react.dev/learn/you-might-not-need-an-effect.
  const [prevEnvData, setPrevEnvData] = useState(envData)
  if (envData !== prevEnvData) {
    setPrevEnvData(envData)
    if (envData?.hasEnvFile) {
      // A reveal is stored as `sensitive: false` on the row itself, so a refetch
      // would silently re-mask whatever the user had uncovered. That refetch is
      // now routine: unlocking invalidates this query to swap the blanked values
      // for the real ones (agent-os-7o5s). Carry the reveals across by key so the
      // value the user asked to see survives the payload it was waiting for.
      const revealedKeys = new Set(
        entries.filter((e) => !e.sensitive && isSensitiveKey(e.key)).map((e) => e.key),
      )
      const rows = envData.entries.map((e) => ({
        ...e,
        sensitive: e.sensitive && !revealedKeys.has(e.key),
        _rowId: nextRowId(),
      }))
      setEntries(rows)
      // `raw` is absent while locked — the backend withholds it rather than
      // sending an empty file, so default it instead of writing `undefined` into
      // a textarea (agent-os-7o5s).
      setRawContent(envData.raw ?? '')
      setHasUnsavedChanges(false)
      setShowEnvSection(true)
      resetHistory(rows, envData.raw ?? '')
    } else {
      setShowEnvSection(false)
    }
  }

  const { saveMutation, createEnvMutation, handleSaveTable, handleSaveRaw } = useEnvMutations({
    stackId,
    entries,
    rawContent,
    setEntries,
    setRawContent,
    setHasUnsavedChanges,
    setShowEnvSection,
    resetHistory,
  })

  // The backend redacted this payload: sensitive values arrived blank and there
  // is no raw file. Editing is therefore off the table until the unlock window
  // opens — saving would persist the blanks, and the backend 403s the write.
  const locked = envData?.hasEnvFile === true && envData.locked === true

  // agent-os-wczm. The ISERROR guard is the one that was broken: this component
  // hydrates `entries`, `rawContent` and `hasUnsavedChanges` from the payload
  // during render, so replacing the editor on a bare `isError` threw away
  // UNSAVED USER EDITS on a single 500 from a focus refetch. `&& !envData` still
  // routes a real first-load failure — an unknown stack, or an env file missing
  // from disk — to the error state, which is what the comment above this query
  // is about.
  //
  // The `&& !envData` on the LOADING guard is belt-and-braces and cannot change
  // the result today: query-core sets status "pending" only while `data` is
  // undefined, and `isLoading = isPending && isFetching`, so `isLoading` already
  // implies `!envData` for this query — it declares no placeholderData,
  // initialData or select. It is written out so the two guards read as a pair,
  // and so the day someone adds placeholderData here the loading branch does not
  // quietly start hiding a populated editor.
  if (isLoading && !envData) {
    return <EnvLoadingState />
  }

  if (isError && !envData) {
    return (
      <EnvErrorState
        // agent-os-rtn8: the two 404s the comment above routes into isError --
        // "Stack not found" and "Env file not found on disk" -- reached here and
        // were rendered as one sentence. classifyError is the house reader for
        // this (DashboardPage.tsx, StackPage.tsx do the same), and its 404 arm
        // only stopped replacing the backend's message in agent-os-mc4i, so this
        // prop would have shown "The requested resource was not found" for both
        // states before that landed.
        cause={classifyError(error).message}
        onRetry={() => queryClient.invalidateQueries({ queryKey: queryKeys.stack.env(stackId) })}
      />
    )
  }

  // `hasEnvFile: false` is the backend saying this stack has no env file — a
  // 200, not a 404 (agent-os-bt5y). showEnvSection is set to true either after
  // a successful create or when a file-present payload loads.
  if (!showEnvSection) {
    return (
      <EnvNoFileState
        onCreate={() => createEnvMutation.mutate()}
        creating={createEnvMutation.isPending}
      />
    )
  }

  return (
    <div className="space-y-4">
      <EnvEditorToolbar
        view={view}
        onViewChange={setView}
        canUndo={historyIndex > 0}
        canRedo={historyIndex < historyLength - 1}
        onUndo={handleUndo}
        onRedo={handleRedo}
        hasUnsavedChanges={hasUnsavedChanges}
        locked={locked}
      />
      <EnvUnlockDialog
        open={unlockDialogOpen}
        onOpenChange={handleUnlockDialogOpenChange}
        onUnlocked={handleUnlocked}
      />

      {locked && <EnvLockedNotice onUnlock={() => handleUnlockDialogOpenChange(true)} />}

      {/* Above the editor, where EnvLockedNotice already puts "editing is
          constrained" messaging — both save controls (the table's and the raw
          view's) sit below it. A stale form the operator is about to save from
          is exactly that kind of constraint (agent-os-wczm). */}
      {isError && !!envData && (
        <RefreshFailedNotice
          what="the environment file"
          beforeSave
          onRetry={() =>
            queryClient.invalidateQueries({ queryKey: queryKeys.stack.env(stackId) })
          }
        />
      )}

      <EnvTableView
        visible={view === 'table'}
        entries={entries}
        onEntryChange={handleEntryChange}
        onDeleteEntry={handleDeleteEntry}
        onAddEntry={handleAddEntry}
        onToggleVisibility={toggleVisibility}
        onSaveTable={handleSaveTable}
        saving={saveMutation.isPending}
        hasUnsavedChanges={hasUnsavedChanges}
        locked={locked}
      />

      {view === 'raw' && !locked && (
        <EnvRawView
          rawContent={rawContent}
          onRawChange={handleRawChange}
          onSaveRaw={handleSaveRaw}
          saving={saveMutation.isPending}
          hasUnsavedChanges={hasUnsavedChanges}
        />
      )}
    </div>
  )
}
