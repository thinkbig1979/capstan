import { useMemo, useState, type Dispatch, type SetStateAction } from 'react'
import { toast } from 'sonner'
import { useTextFilter } from '@/hooks/useTextFilter'
import { useGlobalEnv, useUpdateGlobalEnv } from '@/hooks/useResources'
import { useExternalFileChange } from '@/hooks/useExternalFileChange'
import { useStillHoldsSent } from '@/hooks/useStillHoldsSent'
import { presentError } from '@/lib/error-handler'
import {
  alignRedactedRows,
  blindEditedRowIds,
  holdsBlindEdit,
  refillRedactedRows,
  NO_REDACTED_ROWS,
} from '@/lib/env-unlock-rebase'
import { useNextRowId } from '@/components/stack/env-editor/useRowId'
import { isSensitiveKey } from '@/components/stack/env-editor/sensitiveKey'
import { useEnvLockedNow } from '@/hooks/useEnvLockedNow'
import { searchable } from '@/components/stack/env-editor/envSearch'
import type { EditableField, EnvRow, EnvVar, IndexedEnvVar } from './types'

/** The fields of a variable the locked view can hide, so an unlock can put them back. */
const REDACTABLE_FIELDS = ['key', 'value'] as const


/**
 * Local edit buffer for the global env vars table: syncs from the server
 * query, tracks unsaved-changes (dirty), and wraps the CRUD handlers plus the
 * save mutation. `setVisible` is threaded in from useGlobalEnvReveal so the
 * data-sync effect and handleDelete can clear reveal state for the right
 * rows (keyed by `_rowId`) without owning that state themselves.
 */
export function useGlobalEnvVars(
  setVisible: Dispatch<SetStateAction<Record<number, boolean>>>,
  visible: Readonly<Record<number, boolean>>,
) {
  const { data, isPending, isLoadingError, isRefetchError, refetch } = useGlobalEnv()
  const updateGlobalEnv = useUpdateGlobalEnv()

  const nextRowId = useNextRowId()
  const [vars, setVars] = useState<EnvRow[]>([])
  const [dirty, setDirty] = useState(false)
  // The list as last loaded or saved: what a kept edit is compared against.
  const [loaded, setLoaded] = useState<EnvRow[]>([])
  // The ids of the rows the server blanked, when `loaded` is a locked payload.
  // The payload carries no sensitive flag, so every blank value counts: a
  // DATABASE_URL blanked for its URL credential has no secret-looking key
  // (agent-os-yt2f), and a value that really is empty refills as empty.
  const [redacted, setRedacted] = useState<ReadonlySet<number>>(NO_REDACTED_ROWS)
  // An unlock whose payload could not be lined up with the rows kept from the
  // locked view (agent-os-yt2f): Save is refused until Reload.
  const [saveRefused, setSaveRefused] = useState(false)
  // Rows the user edited or deleted while the locked view hid them, keys and all
  // (agent-os-yt2f review S1, S2), with the unlocked rows they were lined up with
  // and what the user last typed into each with its value in view (`informed`,
  // agent-os-tthh). Save stays refused while any of them holds neither.
  const [blindGuard, setBlindGuard] = useState<{
    ids: ReadonlySet<number>
    aligned: ReadonlyMap<number, EnvVar>
    informed: ReadonlyMap<number, EnvVar>
  } | null>(null)
  // Rows whose key the locked payload blanked while a dirty lock kept the
  // unlocked one: a multi-line secret's body line read as a key (agent-os-f3am).
  const [hiddenKeyIds, setHiddenKeyIds] = useState<ReadonlySet<number>>(NO_REDACTED_ROWS)
  const lockedNow = useEnvLockedNow()

  // The list a save last sent. Its own refetch comes back carrying exactly this,
  // which must not read as a change made elsewhere (agent-os-lqcw). State, not a
  // ref, because isOwnSave reads it while rendering. It is dropped once that
  // refetch is consumed, the form is clean again, or the save failed.
  const [sent, setSent] = useState<EnvRow[] | null>(null)
  // The rows a save sent, against the form as it is when the save lands.
  const sentRows = useStillHoldsSent(vars, (latest, rows: EnvRow[]) => sameVars(cleanRows(latest), rows))

  // A new payload replaces the form only when the form is clean. A dirty form
  // keeps what the user typed and offers Reload (agent-os-lqcw), the rule the
  // compose and stack env editors follow (agent-os-umig). A lock change keeps
  // the edits too (agent-os-vqf5): a lock leaves them on screen, Save stays
  // disabled while locked, and an unlock puts the real key and value back into
  // every blanked row the user left as it was, so a save never writes a blank.
  // When the two payloads do not line up, nothing is filled and Save is refused
  // until Reload (agent-os-yt2f).
  const hydrate = (next: NonNullable<typeof data>) => {
    const rows = (next.vars ?? []).map((v) => ({ ...v, _rowId: nextRowId() }))
    setVars(rows)
    setLoaded(rows)
    setRedacted(next.locked ? new Set(rows.filter((r) => r.value === '').map((r) => r._rowId)) : NO_REDACTED_ROWS)
    setSaveRefused(false)
    setBlindGuard(null)
    setHiddenKeyIds(NO_REDACTED_ROWS)
    setSent(null)
    setVisible({})
    setDirty(false)
  }
  /**
   * A dirty lock keeps the unlocked rows, which carry a multi-line secret's
   * body lines under keys that are their own text; the locked payload blanks
   * those keys (agent-os-f3am, the y6e8 shape). Line the two up by position,
   * read-only flag and every key the lock kept, mask what the lock flags and
   * hide the keys it blanked. When they do not line up, the file changed: mask
   * every row and hide every key.
   */
  const maskRowsTheLockFlags = (locked: EnvVar[]) => {
    const lined =
      loaded.length === locked.length &&
      loaded.every(
        (row, i) => !!row.readOnly === !!locked[i].readOnly && (locked[i].key === '' || locked[i].key === row.key),
      )
    const flagged = new Set<number>()
    const keys = new Set<number>()
    if (lined) {
      loaded.forEach((row, i) => {
        if (!locked[i].sensitive) return
        flagged.add(row._rowId)
        if (locked[i].key === '' && row.key !== '') keys.add(row._rowId)
      })
    } else {
      for (const row of vars) {
        flagged.add(row._rowId)
        keys.add(row._rowId)
      }
    }
    if (flagged.size > 0) {
      setVars((prev) =>
        prev.some((r) => flagged.has(r._rowId) && !r.sensitive)
          ? prev.map((r) => (flagged.has(r._rowId) && !r.sensitive ? { ...r, sensitive: true } : r))
          : prev,
      )
    }
    if (keys.size > 0) setHiddenKeyIds((prev) => new Set([...prev, ...keys]))
  }

  const { changedOnDisk, reload } = useExternalFileChange(data, {
    isDirty: dirty,
    lockedNow,
    hydrate,
    rebaseWhileDirty: (next, prev) => {
      if (!prev || Boolean(next.locked) === Boolean(prev.locked)) return false
      // Locked: the payload is redacted and carries nothing to rebase onto, but
      // it says which kept rows are secret.
      if (next.locked) {
        maskRowsTheLockFlags(next.vars ?? [])
        return true
      }
      const nextVars = next.vars ?? []
      const aligned = alignRedactedRows(loaded, redacted, nextVars)
      if (!aligned) {
        setSaveRefused(true)
        return false
      }
      setSaveRefused(false)
      // Only an unlock against a locked baseline lines anything up. A later one
      // (locked again, unlocked again) keeps the guard it set.
      if (aligned.size > 0) {
        const ids = blindEditedRowIds([vars], loaded, aligned)
        setBlindGuard(ids.size > 0 ? { ids, aligned, informed: new Map() } : null)
      }
      const baseline = refillRedactedRows(loaded, loaded, aligned, REDACTABLE_FIELDS)
      setVars(refillRedactedRows(vars, loaded, aligned, REDACTABLE_FIELDS))
      setLoaded(baseline)
      setRedacted(NO_REDACTED_ROWS)
      // Nothing else changed on the server since the baseline: the edits stay,
      // no notice. Otherwise the standard rule below decides.
      return sameVars(nextVars, baseline)
    },
    canHydrateWhileDirty: (next) => sameVars(next.vars ?? [], vars),
    isOwnSave: (next) => sent !== null && sameVars(next.vars ?? [], sent),
    onOwnSave: () => setSent(null),
  })

  const refusal = saveRefused
    ? ('unaligned' as const)
    : blindGuard && holdsBlindEdit(vars, blindGuard.ids, blindGuard.aligned, blindGuard.informed)
      ? ('blind' as const)
      : null

  // The payload on screen was served unlocked but the session is locked now:
  // the lock's refetch has not landed, or failed. Its keys were never blanked,
  // so every masked row hides its key until the locked payload arrives
  // (agent-os-f3am).
  const staleUnlocked = lockedNow && data !== undefined && !data.locked
  // Wrap vars with their original index so filtered results can route edits
  // correctly, and with whether the row's key is hidden: a masked row whose key
  // the lock blanked, or any masked row while the payload is stale.
  const indexedVars = useMemo<IndexedEnvVar[]>(
    () =>
      vars.map((v, i) => {
        const masked = (v.sensitive === true || isSensitiveKey(v.key)) && !visible[v._rowId]
        return { ...v, _originalIndex: i, keyHidden: masked && (staleUnlocked || hiddenKeyIds.has(v._rowId)) }
      }),
    [vars, visible, staleUnlocked, hiddenKeyIds],
  )
  // A hidden value or key is not searched, the same rule the table masks it
  // by: a match would tell a locked user what it contains (agent-os-sopw, f3am).
  const searchFields = useMemo(
    () => [
      (e: IndexedEnvVar) => searchable(e.key, e.keyHidden),
      (e: IndexedEnvVar) => searchable(e.value, (e.sensitive === true || isSensitiveKey(e.key)) && !visible[e._rowId]),
    ],
    [visible],
  )
  const { query, setQuery, filtered } = useTextFilter(indexedVars, searchFields)

  const handleChange = (index: number, field: EditableField, value: string) => {
    // A key or value typed into a blind-edited row while its value is in view is
    // the user's choice, not a blind edit (agent-os-tthh, as agent-os-ocnh in
    // the stack editor). "In view" is the row as it was before this edit, by the
    // same rule the table masks it by, and never while locked.
    const row = vars[index]
    const inView =
      row !== undefined &&
      !lockedNow &&
      data?.locked !== true &&
      !((row.sensitive === true || isSensitiveKey(row.key)) && !visible[row._rowId])
    if (blindGuard?.ids.has(row?._rowId) && inView) {
      const typed = { key: row.key, value: row.value, [field]: value }
      setBlindGuard({ ...blindGuard, informed: new Map(blindGuard.informed).set(row._rowId, typed) })
    }
    setVars((prev) => {
      const next = [...prev]
      next[index] = { ...next[index], [field]: value }
      return next
    })
    setDirty(true)
  }

  const handleAdd = () => {
    const row = { key: '', value: '', _rowId: nextRowId() }
    setVars((prev) => [...prev, row])
    setDirty(true)
  }

  const handleDelete = (index: number) => {
    const rowId = vars[index]?._rowId
    setVars((prev) => prev.filter((_, i) => i !== index))
    setVisible((prev) => {
      const next = { ...prev }
      if (rowId !== undefined) delete next[rowId]
      return next
    })
    setDirty(true)
  }

  const handleSave = () => {
    // Refused here too, not only by the disabled button.
    if (refusal) return
    const rows = cleanRows(vars)
    setSent(rows)
    sentRows.remember(rows)
    // The wire format has no row id.
    updateGlobalEnv.mutate(rows.map(({ key, value }) => ({ key, value })), {
      onSuccess: () => {
        toast.success('Global environment variables saved')
        // Typing that landed while the save was in flight is not on the server:
        // stay dirty, and the refetch is then recognised as this save's own
        // (agent-os-lqcw, the same rule as agent-os-3761).
        setLoaded(rows)
        setBlindGuard(null)
        if (sentRows.stillHolds()) {
          setDirty(false)
          setSent(null)
        }
      },
      onError: (err) => {
        setSent(null)
        presentError(err, { fallback: 'Failed to save global environment variables' })
      },
    })
  }

  return {
    isPending,
    // agent-os-wczm: the two halves of `isError`, not `isError` itself. The
    // call site has no `data` to pair a bare isError with — `data` is not on
    // this hook's return surface and putting it there would widen it for one
    // boolean — so the hook exports the distinction TanStack already draws:
    // isLoadingError is `isError && data === undefined` (nothing to show, so
    // refuse), isRefetchError is `isError && data !== undefined` (a refresh
    // failed over variables and edits that must not be discarded).
    isLoadingError,
    isRefetchError,
    // A save from this panel is a FULL REPLACE of the variable list, so an
    // operator told the list may be stale needs a way to make it fresh --
    // otherwise the notice states a problem it offers no way out of.
    refetch,
    // The server blanked the secret-looking values because no unlock token was in
    // play. Saving from that state would persist the blanks, and the backend 403s
    // the write, so the panel disables Save until unlocked (agent-os-7o5s).
    locked: data?.locked === true,
    // Why Save is refused (agent-os-yt2f), or null. Never while locked: the lock
    // notice and the disabled Save already cover that.
    saveRefused: data?.locked === true ? null : refusal,
    vars,
    dirty,
    query,
    setQuery,
    filtered,
    handleChange,
    handleAdd,
    handleDelete,
    handleSave,
    changedElsewhere: changedOnDisk,
    // A blind edit can be refused with nothing held back (the unlock lined up),
    // so Reload loads the payload on screen when no newer one is pending.
    reloadChanged: () => {
      if (changedOnDisk) reload()
      else if (data) hydrate(data)
    },
    isSaving: updateGlobalEnv.isPending,
  }
}

/** What a save sends: keys trimmed, rows without a key dropped. */
function cleanRows(vars: EnvRow[]): EnvRow[] {
  return vars.map((v) => ({ ...v, key: v.key.trim() })).filter((v) => v.key !== '')
}

/** Two lists say the same variables, in the same order. */
function sameVars(a: EnvVar[], b: EnvVar[]) {
  return a.length === b.length && a.every((v, i) => v.key === b[i].key && v.value === b[i].value)
}
