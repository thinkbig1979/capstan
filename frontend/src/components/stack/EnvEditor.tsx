import { useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { stacksApi } from '@/lib/api'
import type { EnvEntry } from '@/types'
import { useEnvUnlockStore } from '@/stores/envUnlockStore'
import { EnvUnlockDialog } from '@/components/EnvUnlockDialog'
import { useAuth } from '@/hooks/useAuth'
import { useEnvHistory } from './env-editor/useEnvHistory'
import { useEnvUnlockRemask } from './env-editor/useEnvUnlockRemask'
import { useEnvMutations } from './env-editor/useEnvMutations'
import { useEnvEntryActions } from './env-editor/useEnvEntryActions'
import { useNextRowId } from './env-editor/useRowId'
import { EnvEditorToolbar } from './env-editor/EnvEditorToolbar'
import { EnvLoadingState, EnvErrorState, EnvNoFileState } from './env-editor/EnvEditorEmptyStates'
import { EnvTableView } from './env-editor/EnvTableView'
import { EnvRawView } from './env-editor/EnvRawView'
import type { EnvEntryRow } from './env-editor/types'
import { queryKeys } from '@/lib/query-keys'

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

  const { data: envData, isLoading, isError } = useQuery({
    queryKey: queryKeys.stack.env(stackId),
    queryFn: async () => {
      try {
        const data = await stacksApi.getEnv(stackId)
        return data as { filename: string; entries: EnvEntry[]; raw: string } | undefined
      } catch (error: unknown) {
        const err = error as { response?: { status?: number }; status?: number }
        if (err.response?.status === 404 || err.status === 404) {
          return null
        }
        throw error
      }
    },
  })

  // Hydrate local editable state whenever a new envData query result arrives.
  // Adjusted during render (rather than in an effect) by comparing against the
  // envData reference from the previous render — see
  // https://react.dev/learn/you-might-not-need-an-effect.
  const [prevEnvData, setPrevEnvData] = useState(envData)
  if (envData !== prevEnvData) {
    setPrevEnvData(envData)
    if (envData) {
      const rows = envData.entries.map((e) => ({ ...e, _rowId: nextRowId() }))
      setEntries(rows)
      setRawContent(envData.raw)
      setHasUnsavedChanges(false)
      setShowEnvSection(true)
      resetHistory(rows, envData.raw)
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

  if (isLoading) {
    return <EnvLoadingState />
  }

  if (isError) {
    return (
      <EnvErrorState
        onRetry={() => queryClient.invalidateQueries({ queryKey: queryKeys.stack.env(stackId) })}
      />
    )
  }

  // envData === null means the backend returned 404 (no env file).
  // showEnvSection is set to true either after a successful create or when data loads.
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
      />
      <EnvUnlockDialog
        open={unlockDialogOpen}
        onOpenChange={handleUnlockDialogOpenChange}
        onUnlocked={handleUnlocked}
      />

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
      />

      {view === 'raw' && (
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
