import { useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { stacksApi } from '@/lib/api'
import { useEnvUnlockStore } from '@/stores/envUnlockStore'
import { useEnvLockedNow } from '@/hooks/useEnvLockedNow'
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
import {
  alignRedactedRows,
  blindEditedRowIds,
  holdsBlindEdit,
  refillRedactedRows,
  NO_REDACTED_ROWS,
} from '@/lib/env-unlock-rebase'
import type { EnvEntryDraft, EnvFileResponse } from '@/types'

/** The fields of a row the locked view can hide, so an unlock can put them back. */
const REDACTABLE_FIELDS = ['key', 'value', 'comment', 'quote', 'inlineComment'] as const

// Placeholder copy (agent-os-yt2f): wording to be confirmed.
const SAVE_REFUSED_COPY = {
  unaligned:
    "The unlocked file does not line up with the rows you edited while locked, so they can't be saved safely. Reload to load the file; your edits will be lost.",
  blind:
    "You changed or deleted rows that were hidden while locked, so they can't be saved safely. Undo those changes, or Reload to load the file; your edits will be lost.",
  removed:
    "The env file was removed on disk, so your edits can't be saved. Copy them before you Reload; Reload closes the editor.",
}

/** Where a row sits in the file: what a reveal follows across a refetch (agent-os-0b4m). */
const rowSlot = (e: { line?: number; key: string }) => `${e.line ?? ''}\n${e.key}`

interface EnvEditorProps {
  stackId: string
}

export function EnvEditor({ stackId }: EnvEditorProps) {
  const queryClient = useQueryClient()
  const { authDisabled } = useAuth()
  const isUnlocked = useEnvUnlockStore((s) => s.isUnlocked)
  const unlockedUntil = useEnvUnlockStore((s) => s.unlockedUntil)
  const lockedNow = useEnvLockedNow()
  const [view, setView] = useState<'table' | 'raw'>('table')
  const [entries, setEntries] = useState<EnvEntryRow[]>([])
  const [rawContent, setRawContent] = useState('')
  const [hasUnsavedChanges, setHasUnsavedChanges] = useState(false)
  // The rows and raw text as last loaded. Table and raw are edited
  // independently, so this is what tells which of them the user changed.
  // `redacted` holds the ids of rows the server redacted, when the baseline is
  // a locked payload: those are the rows an unlock has to fill.
  const [loadedEnv, setLoadedEnv] = useState<{ rows: EnvEntryRow[]; raw: string; redacted: ReadonlySet<number> }>({
    rows: [],
    raw: '',
    redacted: NO_REDACTED_ROWS,
  })
  // An unlock whose payload could not be lined up with the rows kept from the
  // locked view (agent-os-yt2f): saving would write rows that hold the locked
  // shapes, so both saves are refused until Reload.
  const [saveRefused, setSaveRefused] = useState(false)
  // Rows whose key the locked payload blanked while a dirty lock kept the
  // unlocked one: a body line that parsed as KEY= (agent-os-y6e8). A masked row
  // in this set shows no key.
  const [hiddenKeyIds, setHiddenKeyIds] = useState<ReadonlySet<number>>(NO_REDACTED_ROWS)
  // Rows the user edited or deleted while the locked view hid them (agent-os-yt2f
  // review S1, S2), with the unlocked rows they were lined up with. Save stays
  // refused while any of them holds neither its real content nor what the user
  // last typed into it with its value in view (`informed`, agent-os-ocnh).
  const [blindGuard, setBlindGuard] = useState<{
    ids: ReadonlySet<number>
    aligned: ReadonlyMap<number, { key: string; value: string }>
    informed: ReadonlyMap<number, { key: string; value: string }>
  } | null>(null)
  const [showEnvSection, setShowEnvSection] = useState(false)
  const nextRowId = useNextRowId()

  const { historyIndex, historyLength, snapshots, pushToHistory, handleUndo, handleRedo, resetHistory, rebaseHistory } =
    useEnvHistory({ setEntries, setRawContent, setHasUnsavedChanges })

  // Re-masks revealed entries when the unlock session ends (manual lock or
  // auto-expiry) — see useEnvUnlockRemask for which ones and the render-time
  // detail.
  const { rememberMasked, wasMasked } = useEnvUnlockRemask(unlockedUntil, setEntries, entries, { snapshots, rebase: rebaseHistory })

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
      // for the real ones (agent-os-7o5s). Carry the reveals across so the value
      // the user asked to see survives the payload it was waiting for: a row
      // shown in the clear that was ever masked, the server's flag included (a
      // DATABASE_URL masked for its credential, agent-os-0b4m), onto the new row
      // on the same line under the same key.
      const revealedRows = new Set(
        entries.filter((e) => !e.sensitive && (isSensitiveKey(e.key) || wasMasked(e._rowId))).map(rowSlot),
      )
      const rows = next.entries.map((e) => ({
        ...e,
        sensitive: e.sensitive && !revealedRows.has(rowSlot(e)),
        _rowId: nextRowId(),
      }))
      setEntries(rows)
      // The server's flag, not the carried reveal, decides what a lock re-masks.
      rememberMasked(rows.filter((_, i) => next.entries[i].sensitive).map((r) => r._rowId))
      setLoadedEnv({
        rows,
        raw: next.raw ?? '',
        redacted: next.locked
          ? new Set(rows.filter((_, i) => next.entries[i].sensitive).map((r) => r._rowId))
          : NO_REDACTED_ROWS,
      })
      setSaveRefused(false)
      setBlindGuard(null)
      setHiddenKeyIds(NO_REDACTED_ROWS)
      // `raw` is absent while locked — the backend withholds it rather than
      // sending an empty file, so default it instead of writing `undefined` into
      // a textarea (agent-os-7o5s).
      setRawContent(next.raw ?? '')
      setHasUnsavedChanges(false)
      setShowEnvSection(true)
      resetHistory(rows, next.raw ?? '')
    } else {
      setSaveRefused(false)
      setBlindGuard(null)
      setShowEnvSection(false)
    }
  }


  /**
   * A dirty lock keeps the unlocked rows, and the unlocked payload flags only
   * what is secret by its own key or value: a multi-line secret's body line
   * that parsed as KEY= arrives as a plain variable (agent-os-y6e8). The locked
   * payload flags it, so line the two up by file line and comment flag (the
   * key is not compared: the locked one is blank) and mask what it flags, and
   * hide the key it blanked. When they do not line up, the file changed on
   * disk: mask every variable row and hide its key (comments are masked by the
   * lock already).
   */
  const maskRowsTheLockFlags = (locked: EnvEntryDraft[]) => {
    const base = loadedEnv.rows
    const lined =
      base.length === locked.length &&
      base.every((row, i) => row.line === locked[i].line && !!row.comment === !!locked[i].comment)
    const flagged = new Set<number>()
    const keys = new Set<number>()
    if (lined) {
      base.forEach((row, i) => {
        if (!locked[i].sensitive) return
        flagged.add(row._rowId)
        if (locked[i].key === '' && row.key !== '') keys.add(row._rowId)
      })
    } else {
      for (const row of [entries, ...snapshots.map((s) => s.entries)].flat()) {
        if (row.comment) continue
        flagged.add(row._rowId)
        keys.add(row._rowId)
      }
    }
    if (flagged.size === 0) return
    const mask = (rows: EnvEntryRow[]) =>
      rows.some((r) => flagged.has(r._rowId) && !r.sensitive)
        ? rows.map((r) => (flagged.has(r._rowId) && !r.sensitive ? { ...r, sensitive: true } : r))
        : rows
    setEntries(mask)
    rebaseHistory((snapshot) => {
      const next = mask(snapshot.entries)
      return next === snapshot.entries ? snapshot : { ...snapshot, entries: next }
    })
    rememberMasked([...flagged])
    if (keys.size > 0) setHiddenKeyIds((prev) => new Set([...prev, ...keys]))
  }

  // Unsaved edits survive a new payload (agent-os-umig), and a lock change
  // too (agent-os-vqf5): a lock keeps them on screen (Save stays disabled while
  // locked, EnvTableView.tsx), and an unlock puts the real content back into
  // every redacted row the user left as it was, so a save can never write a
  // locked shape over a secret. When the unlocked payload does not line up
  // with the locked one, nothing is filled and Save is refused until Reload
  // (agent-os-yt2f). A file appearing or going away keeps the plain hydrate.
  const { changedOnDisk, reload } = useExternalFileChange(envData, {
    isDirty: hasUnsavedChanges,
    lockedNow,
    hydrate,
    rebaseWhileDirty: (next, prev) => {
      // The removed file is back as it was loaded (agent-os-wn5h review F1):
      // the held edits are edits to it again, with no notice.
      if (next.hasEnvFile && prev && !prev.hasEnvFile && showEnvSection) {
        return sameEntries(next.entries, loadedEnv.rows) && (next.raw ?? '') === loadedEnv.raw
      }
      if (!next.hasEnvFile || !prev?.hasEnvFile) return false
      if (Boolean(next.locked) === Boolean(prev.locked)) return false
      // Locked: the payload is redacted and carries nothing to rebase onto, but
      // it says which kept rows are secret.
      if (next.locked) {
        maskRowsTheLockFlags(next.entries)
        return true
      }
      const aligned = alignRedactedRows(loadedEnv.rows, loadedEnv.redacted, next.entries)
      if (!aligned) {
        setSaveRefused(true)
        return false
      }
      setSaveRefused(false)
      // Only an unlock against a locked baseline lines anything up. A later one
      // (locked again, unlocked again) keeps the guard it set.
      if (aligned.size > 0) {
        const ids = blindEditedRowIds([entries, ...snapshots.map((s) => s.entries)], loadedEnv.rows, aligned)
        setBlindGuard(ids.size > 0 ? { ids, aligned, informed: new Map() } : null)
      }
      const fill = (rows: EnvEntryRow[]) =>
        refillRedactedRows(rows, loadedEnv.rows, aligned, REDACTABLE_FIELDS)
      const baseRows = fill(loadedEnv.rows)
      // A raw text the user did not touch (absent while locked) follows the file.
      const nextRaw = next.raw ?? ''
      const rawEdited = rawContent !== loadedEnv.raw
      const baseRaw = rawEdited ? loadedEnv.raw : nextRaw
      setEntries(fill(entries))
      if (!rawEdited) setRawContent(nextRaw)
      // Undo and Redo restore old snapshots, which still hold the locked shapes
      // (and the empty locked raw): fill them too, or one keystroke brings one back.
      rebaseHistory((snapshot) => ({
        entries: fill(snapshot.entries),
        raw: !rawEdited && snapshot.raw === loadedEnv.raw ? nextRaw : snapshot.raw,
      }))
      setLoadedEnv({ rows: baseRows, raw: baseRaw, redacted: NO_REDACTED_ROWS })
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
      const savedRaw = next.hasEnvFile ? next.raw : undefined
      // The raw text the user never touched follows the file, as the baseline
      // does; otherwise rawContent !== loadedEnv.raw would read as a raw edit.
      if (savedRaw !== undefined && rawContent === loadedEnv.raw) setRawContent(savedRaw)
      // Only a table save writes the rows, and it is refused while a blind edit
      // is held. A raw save leaves them as they are, blind edit and all.
      if (sent?.entries) setBlindGuard(null)
      setLoadedEnv((prev) => ({
        rows: sent?.entries ?? prev.rows,
        raw: savedRaw ?? prev.raw,
        redacted: NO_REDACTED_ROWS,
      }))
      clearSent()
    },
    canHydrateWhileDirty: (next, prev) => {
      // The file went away under unsaved edits (agent-os-wn5h): hydrating would
      // hide them behind the no-file state. Hold it; Save is refused below. When
      // it comes back, the edits are still to that file, so the rule below
      // decides, against the baseline the edits started from (review F1).
      if (!next.hasEnvFile) return !showEnvSection
      if (!prev?.hasEnvFile && !showEnvSection) return true
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
  // The unlock store counts too: after a lock the last payload is still the
  // unlocked one until the lock's refetch lands, and for good if it fails, so
  // the payload alone would leave the whole file on screen (agent-os-wm7t).
  const rawHidden = locked || lockedNow
  const activeView = rawHidden ? 'table' : view

  // The payload says the file is gone while the editor still holds edits to it:
  // the backend has no file to write them to (agent-os-wn5h).
  const fileRemoved = showEnvSection && hasUnsavedChanges && envData?.hasEnvFile === false
  const refusal = fileRemoved
    ? 'removed'
    : saveRefused
    ? 'unaligned'
    : blindGuard && holdsBlindEdit(entries, blindGuard.ids, blindGuard.aligned, blindGuard.informed)
      ? 'blind'
      : null
  // A raw save never reads the table rows, so a blind table edit cannot reach
  // it (agent-os-2vyw). An unlock that could not be lined up refilled nothing,
  // the raw text included (it is still the locked view's empty one), so that
  // refusal holds for both, as does a file removed on disk.
  const rawRefusal = refusal === 'unaligned' || refusal === 'removed' ? refusal : null
  // The notice names the refusal of the save on screen: the Raw view's Save
  // works under a blind table edit, so the table's copy would be false there
  // (review F5). The table refusal itself stays.
  const shownRefusal = activeView === 'raw' ? rawRefusal : refusal
  // Refused in the handlers too, not only by the disabled buttons.
  const saveTable = () => {
    if (!refusal) handleSaveTable()
  }
  const saveRaw = () => {
    if (!rawRefusal) handleSaveRaw()
  }
  // A key or value typed into a blind-edited row while its value is in view is
  // the user's choice, not a blind edit (agent-os-ocnh). A masked value, a lock,
  // or a change to anything else (the visibility flag) leaves the guard as is.
  const onEntryChange: typeof handleEntryChange = (index, field, value) => {
    const row = entries[index]
    // `!locked && !lockedNow` cannot change the result today: a lock re-masks
    // every row ever masked in the render that turns lockedNow on
    // (useEnvUnlockRemask), and a blind row came in masked. It stays so the gate
    // does not lean on that (review F3; pinned by the ocnh lock-window test).
    const visible = row !== undefined && !row.sensitive && !locked && !lockedNow
    if (blindGuard?.ids.has(row?._rowId) && visible && (field === 'key' || field === 'value')) {
      const typed = { key: row.key, value: row.value, [field]: value as string }
      setBlindGuard({ ...blindGuard, informed: new Map(blindGuard.informed).set(row._rowId, typed) })
    }
    handleEntryChange(index, field, value)
  }
  // A blind edit can be refused with nothing held back (the unlock lined up), so
  // Reload loads the payload on screen when no newer one is pending.
  const reloadFile = () => {
    if (changedOnDisk) reload()
    else if (envData) hydrate(envData)
  }

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
        locked={rawHidden}
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

      {shownRefusal && !locked ? (
        <FileChangedNotice onReload={reloadFile}>{SAVE_REFUSED_COPY[shownRefusal]}</FileChangedNotice>
      ) : (
        changedOnDisk && <FileChangedNotice onReload={reload} />
      )}

      <EnvTableView
        visible={activeView === 'table'}
        entries={entries}
        onEntryChange={onEntryChange}
        onDeleteEntry={handleDeleteEntry}
        onAddEntry={handleAddEntry}
        onToggleVisibility={toggleVisibility}
        onSaveTable={saveTable}
        saving={saveMutation.isPending}
        hasUnsavedChanges={hasUnsavedChanges}
        locked={locked}
        saveRefused={refusal !== null}
        hiddenKeyIds={hiddenKeyIds}
        lockedNow={lockedNow}
      />

      {view === 'raw' && !rawHidden && (
        <EnvRawView
          rawContent={rawContent}
          onRawChange={handleRawChange}
          onSaveRaw={saveRaw}
          saving={saveMutation.isPending}
          hasUnsavedChanges={hasUnsavedChanges}
          saveRefused={rawRefusal !== null}
        />
      )}
    </div>
  )
}

/**
 * Two row lists say the same file, field for field. Quote, inline comment and
 * the export prefix count: a table save writes them back (agent-os-wcrq,
 * agent-os-m5uj). `sensitive` does not, it is display state.
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
        (e.inlineComment ?? '') === (b[i].inlineComment ?? '') &&
        !!e.export === !!b[i].export,
    )
  )
}
