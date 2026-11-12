import { useEffect, useMemo, useRef, useState, type Dispatch, type SetStateAction } from 'react'
import { toast } from 'sonner'
import { useTextFilter } from '@/hooks/useTextFilter'
import { useGlobalEnv, useUpdateGlobalEnv } from '@/hooks/useResources'
import { useExternalFileChange } from '@/hooks/useExternalFileChange'
import { presentError } from '@/lib/error-handler'
import { fillRedactedBlanks } from '@/lib/env-unlock-rebase'
import { isSensitiveKey } from './sensitiveKey'
import { useNextRowId } from '@/components/stack/env-editor/useRowId'
import type { EnvRow, EnvVar, IndexedEnvVar } from './types'

const ENV_SEARCH_FIELDS = [
  (e: IndexedEnvVar) => e.key,
  (e: IndexedEnvVar) => e.value,
]

/**
 * Local edit buffer for the global env vars table: syncs from the server
 * query, tracks unsaved-changes (dirty), and wraps the CRUD handlers plus the
 * save mutation. `setVisible` is threaded in from useGlobalEnvReveal so the
 * data-sync effect and handleDelete can clear reveal state for the right
 * indices without owning that state themselves.
 */
export function useGlobalEnvVars(setVisible: Dispatch<SetStateAction<Record<number, boolean>>>) {
  const { data, isPending, isLoadingError, isRefetchError, refetch } = useGlobalEnv()
  const updateGlobalEnv = useUpdateGlobalEnv()

  const nextRowId = useNextRowId()
  const [vars, setVars] = useState<EnvRow[]>([])
  const [dirty, setDirty] = useState(false)
  // The list as last loaded or saved: what a kept edit is compared against.
  const [loaded, setLoaded] = useState<EnvRow[]>([])

  // The list a save last sent. Its own refetch comes back carrying exactly this,
  // which must not read as a change made elsewhere (agent-os-lqcw). State, not a
  // ref, because isOwnSave reads it while rendering. It is dropped once that
  // refetch is consumed, the form is clean again, or the save failed.
  const [sent, setSent] = useState<EnvRow[] | null>(null)
  const varsRef = useRef(vars)
  useEffect(() => {
    varsRef.current = vars
  }, [vars])

  // A new payload replaces the form only when the form is clean. A dirty form
  // keeps what the user typed and offers Reload (agent-os-lqcw), the rule the
  // compose and stack env editors follow (agent-os-umig). A lock change keeps
  // the edits too (agent-os-vqf5): a lock leaves them on screen, Save stays
  // disabled while locked, and an unlock puts the real value back into any row
  // that only held the redacted blank, so a save never writes a blank.
  const { changedOnDisk, reload } = useExternalFileChange(data, {
    isDirty: dirty,
    hydrate: (next) => {
      const rows = (next.vars ?? []).map((v) => ({ ...v, _rowId: nextRowId() }))
      setVars(rows)
      setLoaded(rows)
      setSent(null)
      setVisible({})
      setDirty(false)
    },
    rebaseWhileDirty: (next, prev) => {
      if (!prev || Boolean(next.locked) === Boolean(prev.locked)) return false
      // Locked: the payload is redacted and carries nothing to rebase onto.
      if (next.locked) return true
      const nextVars = next.vars ?? []
      const redacted = new Set(
        (prev.vars ?? [])
          .filter((v) => prev.locked && v.value === '' && isSensitiveKey(v.key))
          .map((v) => v.key),
      )
      const baseline = fillRedactedBlanks(loaded, loaded, redacted, nextVars)
      setVars(fillRedactedBlanks(vars, loaded, redacted, nextVars))
      setLoaded(baseline)
      // Nothing else changed on the server since the baseline: the edits stay,
      // no notice. Otherwise the standard rule below decides.
      return sameVars(nextVars, baseline)
    },
    canHydrateWhileDirty: (next) => sameVars(next.vars ?? [], vars),
    isOwnSave: (next) => sent !== null && sameVars(next.vars ?? [], sent),
    onOwnSave: () => setSent(null),
  })

  // Wrap vars with their original index so filtered results can route edits correctly.
  const indexedVars = useMemo<IndexedEnvVar[]>(
    () => vars.map((v, i) => ({ ...v, _originalIndex: i })),
    [vars],
  )
  const { query, setQuery, filtered } = useTextFilter(indexedVars, ENV_SEARCH_FIELDS)

  const handleChange = (index: number, field: keyof EnvVar, value: string) => {
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
    setVars((prev) => prev.filter((_, i) => i !== index))
    setVisible((prev) => {
      const next = { ...prev }
      delete next[index]
      return next
    })
    setDirty(true)
  }

  const handleSave = () => {
    const rows = cleanRows(vars)
    setSent(rows)
    // The wire format has no row id.
    updateGlobalEnv.mutate(rows.map(({ key, value }) => ({ key, value })), {
      onSuccess: () => {
        toast.success('Global environment variables saved')
        // Typing that landed while the save was in flight is not on the server:
        // stay dirty, and the refetch is then recognised as this save's own
        // (agent-os-lqcw, the same rule as agent-os-3761).
        setLoaded(rows)
        if (sameVars(cleanRows(varsRef.current), rows)) {
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
    reloadChanged: reload,
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
