import { useEffect, useMemo, useState, type Dispatch, type SetStateAction } from 'react'
import { toast } from 'sonner'
import { useTextFilter } from '@/hooks/useTextFilter'
import { useGlobalEnv, useUpdateGlobalEnv } from '@/hooks/useResources'
import { classifyError } from '@/lib/error-handler'
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
  const { data, isLoading, isError } = useGlobalEnv()
  const updateGlobalEnv = useUpdateGlobalEnv()

  const [vars, setVars] = useState<EnvVar[]>([])
  const [dirty, setDirty] = useState(false)

  useEffect(() => {
    if (!data) return
    // Sync server state into local edit buffer when query resolves or refetches.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setVars(data.vars ?? [])

    setVisible({})

    setDirty(false)
  }, [data, setVisible])

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
    const cleaned = vars
      .map((v) => ({ key: v.key.trim(), value: v.value }))
      .filter((v) => v.key !== '')
    updateGlobalEnv.mutate(cleaned, {
      onSuccess: () => {
        toast.success('Global environment variables saved')
        setDirty(false)
      },
      onError: (err) => {
        toast.error(classifyError(err).message || 'Failed to save global environment variables')
      },
    })
  }

  return {
    isLoading,
    isError,
    vars,
    dirty,
    query,
    setQuery,
    filtered,
    handleChange,
    handleAdd,
    handleDelete,
    handleSave,
    isSaving: updateGlobalEnv.isPending,
  }
}
