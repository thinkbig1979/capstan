import { useEffect, useMemo, useRef, useState, type Dispatch, type SetStateAction } from 'react'
import { toast } from 'sonner'
import { useTextFilter } from '@/hooks/useTextFilter'
import { useGlobalEnv, useUpdateGlobalEnv } from '@/hooks/useResources'
import { useExternalFileChange } from '@/hooks/useExternalFileChange'
import { presentError } from '@/lib/error-handler'
import type { EnvVar, IndexedEnvVar } from './types'

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

  const [vars, setVars] = useState<EnvVar[]>([])
  const [dirty, setDirty] = useState(false)

  // The list a save last sent. Its own refetch comes back carrying exactly this,
  // which must not read as a change made elsewhere (agent-os-lqcw).
  const savedRef = useRef<EnvVar[] | null>(null)
  const varsRef = useRef(vars)
  useEffect(() => {
    varsRef.current = vars
  }, [vars])

  // A new payload replaces the form only when the form is clean. A dirty form
  // keeps what the user typed and offers Reload (agent-os-lqcw), the rule the
  // compose and stack env editors follow (agent-os-umig). A lock change keeps
  // the plain reload for now: agent-os-vqf5 owns the edits that still discards.
  const { changedOnDisk, reload } = useExternalFileChange(data, {
    isDirty: dirty,
    hydrate: (next) => {
      setVars(next.vars ?? [])
      setVisible({})
      setDirty(false)
    },
    canHydrateWhileDirty: (next, prev) =>
      Boolean(next.locked) !== Boolean(prev?.locked) || sameVars(next.vars ?? [], vars),
    isOwnSave: (next) => savedRef.current !== null && sameVars(next.vars ?? [], savedRef.current),
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
    setVars((prev) => [...prev, { key: '', value: '' }])
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
    const cleaned = cleanVars(vars)
    savedRef.current = cleaned
    updateGlobalEnv.mutate(cleaned, {
      onSuccess: () => {
        toast.success('Global environment variables saved')
        // Typing that landed while the save was in flight is not on the server:
        // stay dirty, and the refetch is then recognised as this save's own
        // (agent-os-lqcw, the same rule as agent-os-3761).
        if (sameVars(cleanVars(varsRef.current), cleaned)) setDirty(false)
      },
      onError: (err) => {
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
function cleanVars(vars: EnvVar[]): EnvVar[] {
  return vars.map((v) => ({ key: v.key.trim(), value: v.value })).filter((v) => v.key !== '')
}

/** Two lists say the same variables, in the same order. */
function sameVars(a: EnvVar[], b: EnvVar[]) {
  return a.length === b.length && a.every((v, i) => v.key === b[i].key && v.value === b[i].value)
}
