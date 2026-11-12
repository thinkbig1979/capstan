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
import { FileChangedNotice } from '@/components/FileChangedNotice'
import { useExternalFileChange } from '@/hooks/useExternalFileChange'
import { fillRedactedBlanks } from '@/lib/env-unlock-rebase'
import type { EnvEntryDraft, EnvFileResponse } from '@/types'

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
  // The rows and raw text as last loaded. Table and raw are edited
  // independently, so this is what tells which of them the user changed.
  const [loadedEnv, setLoadedEnv] = useState<{ rows: EnvEntryRow[]; raw: string }>({ rows: [], raw: '' })
  const [showEnvSection, setShowEnvSection] = useState(false)
  const nextRowId = useNextRowId()

  const { historyIndex, historyLength, pushToHistory, handleUndo, handleRedo, resetHistory, rebaseHistory } =
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
  const { data: envData, isPending, isError, error } = useQuery({
    queryKey: queryKeys.stack.env(stackId),
    queryFn: () => stacksApi.getEnv(stackId),
  })

  const { saveMutation, createEnvMutation, handleSaveTable, handleSaveRaw, sent, clearSent } = useEnvMutations({
    stackId,
    entries,
    rawContent,
    setEntries,
    setRawContent,
    setHasUnsavedChanges,
    setShowEnvSection,
    resetHistory,
  })

  const hydrate = (next: EnvFileResponse) => {
    clearSent()
    if (next.hasEnvFile) {
      // A reveal is stored as `sensitive: false` on the row itself, so a refetch
      // would silently re-mask whatever the user had uncovered. That refetch is
      // now routine: unlocking invalidates this query to swap the blanked values
      // for the real ones (agent-os-7o5s). Carry the reveals across by key so the
      // value the user asked to see survives the payload it was waiting for.
      const revealedKeys = new Set(
        entries.filter((e) => !e.sensitive && isSensitiveKey(e.key)).map((e) => e.key),
      )
      const rows = next.entries.map((e) => ({
        ...e,
        sensitive: e.sensitive && !revealedKeys.has(e.key),
        _rowId: nextRowId(),
      }))
      setEntries(rows)
      setLoadedEnv({ rows, raw: next.raw ?? '' })
      // `raw` is absent while locked — the backend withholds it rather than
      // sending an empty file, so default it instead of writing `undefined` into
      // a textarea (agent-os-7o5s).
      setRawContent(next.raw ?? '')
      setHasUnsavedChanges(false)
      setShowEnvSection(true)
      resetHistory(rows, next.raw ?? '')
    } else {
      setShowEnvSection(false)
    }
  }


  // Unsaved edits survive a new payload (agent-os-umig), and a lock change
  // too (agent-os-vqf5): a lock keeps them on screen (Save stays disabled while
  // locked, EnvTableView.tsx), and an unlock puts the real value back into any
  // row that only held the redacted blank, so a save can never write a blank
  // over a secret. A file appearing or going away keeps the plain hydrate.
  const { changedOnDisk, reload } = useExternalFileChange(envData, {
    isDirty: hasUnsavedChanges,
    hydrate,
    rebaseWhileDirty: (next, prev) => {
      if (!next.hasEnvFile || !prev?.hasEnvFile) return false
      if (Boolean(next.locked) === Boolean(prev.locked)) return false
      // Locked: the payload is redacted and carries nothing to rebase onto.
      if (next.locked) return true
      const redacted = new Set(
        prev.entries.filter((e) => prev.locked && e.sensitive && e.value === '').map((e) => e.key),
      )
      const fill = (rows: EnvEntryRow[]) =>
        fillRedactedBlanks(rows, loadedEnv.rows, redacted, next.entries)
      const baseRows = fill(loadedEnv.rows)
      // A raw text the user did not touch (absent while locked) follows the file.
      const nextRaw = next.raw ?? ''
      const rawEdited = rawContent !== loadedEnv.raw
      const baseRaw = rawEdited ? loadedEnv.raw : nextRaw
      setEntries(fill(entries))
      if (!rawEdited) setRawContent(nextRaw)
      // Undo and Redo restore old snapshots, which still hold the blanks (and the
      // empty locked raw): fill them too, or one keystroke brings a blank back.
      rebaseHistory((snapshot) => ({
        entries: fill(snapshot.entries),
        raw: !rawEdited && snapshot.raw === loadedEnv.raw ? nextRaw : snapshot.raw,
      }))
      setLoadedEnv({ rows: baseRows, raw: baseRaw })
      // Nothing else changed on disk since the baseline: the edits stay, no
      // notice. Otherwise the standard rule below decides.
      return sameEntries(next.entries, baseRows) && nextRaw === baseRaw
    },
    // The refetch after a save brings back what was sent. An editor that kept
    // typing past that save stays dirty and takes it without a notice
    // (agent-os-3761); the baseline moves to what was saved, so a later lock
    // change compares against the file as it now is.
    isOwnSave: (next) => {
      if (!sent || !next.hasEnvFile) return false
      return sent.entries ? sameEntries(next.entries, sent.entries) : next.raw === sent.raw
    },
    onOwnSave: (next) => {
      setLoadedEnv((prev) => ({ rows: sent?.entries ?? prev.rows, raw: (next.hasEnvFile ? next.raw : undefined) ?? prev.raw }))
      clearSent()
    },
    canHydrateWhileDirty: (next, prev) => {
      if (!next.hasEnvFile || !prev?.hasEnvFile) return true
      // Every representation the user edited must already equal the new
      // file. Matching only one of them hydrates over edits in the other.
      const rawEdited = rawContent !== loadedEnv.raw
      const rowsEdited = !sameEntries(entries, loadedEnv.rows)
      return (!rawEdited || next.raw === rawContent) && (!rowsEdited || sameEntries(next.entries, entries))
    },
  })


  // The backend redacted this payload: sensitive values arrived blank and there
  // is no raw file. Editing is therefore off the table until the unlock window
  // opens — saving would persist the blanks, and the backend 403s the write.
  const locked = envData?.hasEnvFile === true && envData.locked === true
  // The Raw tab and its view go away while locked. Showing the table then keeps
  // the edits the user kept visible, whichever view the lock found them in.
  const activeView = locked ? 'table' : view

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
  // undefined, so `isPending` implies `!envData` for this query — it declares no placeholderData,
  // initialData or select. It is written out so the two guards read as a pair,
  // and so the day someone adds placeholderData here the loading branch does not
  // quietly start hiding a populated editor.
  if (isPending && !envData) {
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
        view={activeView}
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

      {locked && (
        <EnvLockedNotice
          onUnlock={() => handleUnlockDialogOpenChange(true)}
          hasUnsavedEdits={hasUnsavedChanges}
        />
      )}

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

      {changedOnDisk && <FileChangedNotice onReload={reload} />}

      <EnvTableView
        visible={activeView === 'table'}
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

/**
 * Two row lists say the same file, field for field. Quote and inline comment
 * count: a table save writes them back (agent-os-wcrq). `sensitive` does not,
 * it is display state.
 */
function sameEntries(a: EnvEntryDraft[], b: EnvEntryDraft[]) {
  return (
    a.length === b.length &&
    a.every(
      (e, i) =>
        e.key === b[i].key &&
        e.value === b[i].value &&
        !!e.comment === !!b[i].comment &&
        (e.quote ?? '') === (b[i].quote ?? '') &&
        (e.inlineComment ?? '') === (b[i].inlineComment ?? ''),
    )
  )
}
