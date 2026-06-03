import { useState, useEffect, useCallback, useMemo } from 'react'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { Badge } from '@/components/ui/badge'
import { Eye, EyeOff, Plus, Trash2, Save, Undo, Redo } from 'lucide-react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { stacksApi } from '@/lib/api'
import { isActionResult } from '@/lib/action-result'
import type { EnvEntry } from '@/types'
import { useEnvUnlockStore } from '@/stores/envUnlockStore'
import { EnvUnlockDialog } from '@/components/EnvUnlockDialog'
import { EnvUnlockStatus } from '@/components/EnvUnlockStatus'
import { useAuth } from '@/hooks/useAuth'
import { useActionMutation } from '@/hooks/useActionMutation'
import { useTextFilter } from '@/hooks/useTextFilter'
import { TableSearch } from '@/components/ui/table-search'

/** Entries tagged with their original index so filter never shifts edit targets. */
interface IndexedEnvEntry extends EnvEntry {
  _originalIndex: number
}

const ENV_SEARCH_FIELDS = [
  (e: IndexedEnvEntry) => e.key,
  (e: IndexedEnvEntry) => e.value,
]

interface EnvEditorProps {
  stackId: string
}

export function EnvEditor({ stackId }: EnvEditorProps) {
  const queryClient = useQueryClient()
  const { authDisabled } = useAuth()
  const isUnlocked = useEnvUnlockStore((s) => s.isUnlocked)
  const unlockedUntil = useEnvUnlockStore((s) => s.unlockedUntil)
  const [view, setView] = useState<'table' | 'raw'>('table')
  const [entries, setEntries] = useState<EnvEntry[]>([])
  const [rawContent, setRawContent] = useState('')
  const [hasUnsavedChanges, setHasUnsavedChanges] = useState(false)
  const [showEnvSection, setShowEnvSection] = useState(false)
  const [history, setHistory] = useState<{ entries: EnvEntry[]; raw: string }[]>([])
  const [historyIndex, setHistoryIndex] = useState(-1)
  const [unlockDialogOpen, setUnlockDialogOpen] = useState(false)
  const [pendingRevealIndex, setPendingRevealIndex] = useState<number | null>(null)
  const MAX_HISTORY = 50

  // ── Search filter (table view only) ────────────────────────────────────────
  // Entries are tagged with their original index so that all edit/delete/toggle
  // handlers still target `entries[originalIndex]` regardless of filter order.
  const indexedEntries = useMemo<IndexedEnvEntry[]>(
    () => entries.map((e, i) => ({ ...e, _originalIndex: i })),
    [entries],
  )
  const {
    query: envQuery,
    setQuery: setEnvQuery,
    filtered: filteredIndexed,
  } = useTextFilter(indexedEntries, ENV_SEARCH_FIELDS)

  const pushToHistory = useCallback(
    (newEntries: EnvEntry[], newRaw: string) => {
      if (historyIndex >= 0) {
        const currentState = history[historyIndex]
        if (
          JSON.stringify(currentState.entries) === JSON.stringify(newEntries) &&
          currentState.raw === newRaw
        ) {
          return
        }
      }

      const newHistory = history.slice(0, historyIndex + 1)
      newHistory.push({ entries: newEntries, raw: newRaw })
      if (newHistory.length > MAX_HISTORY) {
        newHistory.shift()
      }
      setHistory(newHistory)
      setHistoryIndex(newHistory.length - 1)
    },
    [history, historyIndex],
  )

  const handleUndo = useCallback(() => {
    if (historyIndex > 0) {
      const prev = history[historyIndex - 1]
      setEntries(prev.entries)
      setRawContent(prev.raw)
      setHistoryIndex((prev) => prev - 1)
      setHasUnsavedChanges(true)
    }
  }, [history, historyIndex])

  const handleRedo = useCallback(() => {
    if (historyIndex < history.length - 1) {
      const next = history[historyIndex + 1]
      setEntries(next.entries)
      setRawContent(next.raw)
      setHistoryIndex((prev) => prev + 1)
      setHasUnsavedChanges(true)
    }
  }, [history, historyIndex])

  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key === 'z' && !e.shiftKey) {
        e.preventDefault()
        handleUndo()
      } else if ((e.ctrlKey || e.metaKey) && (e.key === 'y' || (e.key === 'z' && e.shiftKey))) {
        e.preventDefault()
        handleRedo()
      }
    }
    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [handleUndo, handleRedo])

  const { data: envData, isLoading, isError } = useQuery({
    queryKey: ['stack', stackId, 'env'],
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

  useEffect(() => {
    if (envData) {
       
      setEntries(envData.entries)

      setRawContent(envData.raw)

      setHasUnsavedChanges(false)

      setShowEnvSection(true)

      setHistory([{ entries: envData.entries, raw: envData.raw }])

      setHistoryIndex(0)
    } else {

      setShowEnvSection(false)
    }
  }, [envData])

  /**
   * Save mutation — consumes ActionResult to surface real outcomes.
   * Audit finding #15: backend may return failed/partial for invalid entries;
   * we must not show a false "Saved" toast in those cases.
   */
  const saveMutation = useActionMutation({
    mutationFn: async (body: { entries?: EnvEntry[]; raw?: string }) => {
      const raw = await stacksApi.updateEnv(stackId, body)
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
    invalidate: [['stack', stackId]],
    successTitle: 'Environment variables saved',
    onResult: (result) => {
      if (result.outcome === 'success' || result.outcome === 'no_change') {
        setHasUnsavedChanges(false)
        const body = saveMutation.variables
        if (body) {
          setHistory([{ entries: body.entries || entries, raw: body.raw || rawContent }])
          setHistoryIndex(0)
        }
      }
    },
  })

  /**
   * Create env file mutation — wires the "Create Environment File" button.
   * Audit finding #16: the button was dead (flipped a flag that a higher render
   * guard ignored). The backend now exposes POST /stacks/:id/env.
   */
  const createEnvMutation = useActionMutation<void>({
    mutationFn: async (_vars: void) => {
      const raw = await stacksApi.createEnv(stackId)
      if (isActionResult(raw)) return raw
      return { outcome: 'success' as const, reason: 'Environment file created' }
    },
    invalidate: [['stack', stackId, 'env'], ['stack', stackId]],
    successTitle: 'Environment file created',
    onResult: (result) => {
      if (result.outcome === 'success' || result.outcome === 'no_change') {
        // Reveal the editor immediately — the query invalidation above will
        // re-fetch and populate entries/raw.
        setShowEnvSection(true)
        setEntries([])
        setRawContent('')
        setHistory([{ entries: [], raw: '' }])
        setHistoryIndex(0)
        setHasUnsavedChanges(false)
      }
    },
  })

  const handleAddEntry = () => {
    const newEntries = [...entries, { key: '', value: '', sensitive: false }]
    setEntries(newEntries)
    pushToHistory(newEntries, rawContent)
    setHasUnsavedChanges(true)
  }

  const handleDeleteEntry = (index: number) => {
    const newEntries = entries.filter((_, i) => i !== index)
    setEntries(newEntries)
    pushToHistory(newEntries, rawContent)
    setHasUnsavedChanges(true)
  }

  const handleEntryChange = (index: number, field: keyof EnvEntry, value: string | boolean) => {
    const newEntries = [...entries]
    newEntries[index] = { ...newEntries[index], [field]: value }

    if (field === 'key' && typeof value === 'string') {
      const sensitivePatterns = ['_KEY', '_SECRET', '_PASSWORD', '_TOKEN', '_API_']
      newEntries[index].sensitive = sensitivePatterns.some((pattern) =>
        value.toUpperCase().includes(pattern),
      )
    }

    setEntries(newEntries)
    pushToHistory(newEntries, rawContent)
    setHasUnsavedChanges(true)
  }

  const handleSaveTable = () => {
    saveMutation.mutate({ entries })
  }

  const handleSaveRaw = () => {
    saveMutation.mutate({ raw: rawContent })
  }

  const applyVisibilityToggle = (index: number) => {
    const newEntries = [...entries]
    newEntries[index] = { ...newEntries[index], sensitive: !newEntries[index].sensitive }
    setEntries(newEntries)
    pushToHistory(newEntries, rawContent)
  }

  const toggleVisibility = (index: number) => {
    const current = entries[index]
    const willReveal = current?.sensitive === true
    if (!willReveal || authDisabled || isUnlocked()) {
      applyVisibilityToggle(index)
      return
    }
    setPendingRevealIndex(index)
    setUnlockDialogOpen(true)
  }

  const handleUnlocked = () => {
    if (pendingRevealIndex !== null) {
      applyVisibilityToggle(pendingRevealIndex)
    }
    setPendingRevealIndex(null)
  }

  // When the unlock session ends (manual lock or auto-expiry), re-mask any
  // sensitive-by-name entries the user had revealed during the session.
  useEffect(() => {
    if (unlockedUntil !== null) return
     
    setEntries((prev) => {
      let changed = false
      const next = prev.map((e) => {
        if (!e.sensitive && isSensitiveKey(e.key)) {
          changed = true
          return { ...e, sensitive: true }
        }
        return e
      })
      return changed ? next : prev
    })
  }, [unlockedUntil])

  const isSensitiveKey = (key: string) => {
    const sensitivePatterns = ['_KEY', '_SECRET', '_PASSWORD', '_TOKEN', '_API_']
    return sensitivePatterns.some((pattern) => key.toUpperCase().includes(pattern))
  }

  if (isLoading) {
    return <div className="flex items-center justify-center py-8">Loading...</div>
  }

  if (isError) {
    return (
      <div className="flex flex-col items-center justify-center py-8 text-muted-foreground">
        <p>Failed to load environment file</p>
        <Button
          variant="outline"
          onClick={() => {
            queryClient.invalidateQueries({ queryKey: ['stack', stackId, 'env'] })
          }}
          className="mt-4"
        >
          Retry
        </Button>
      </div>
    )
  }

  // envData === null means the backend returned 404 (no env file).
  // showEnvSection is set to true either after a successful create or when data loads.
  if (!showEnvSection) {
    return (
      <div className="flex flex-col items-center justify-center py-8 text-muted-foreground">
        <p>No environment file found for this stack</p>
        <Button
          variant="outline"
          onClick={() => createEnvMutation.mutate()}
          disabled={createEnvMutation.isPending}
          className="mt-4"
        >
          {createEnvMutation.isPending ? 'Creating...' : 'Create Environment File'}
        </Button>
      </div>
    )
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          <Tabs value={view} onValueChange={(v: string) => setView(v as 'table' | 'raw')}>
            <TabsList>
              <TabsTrigger value="table">Table View</TabsTrigger>
              <TabsTrigger value="raw">Raw Editor</TabsTrigger>
            </TabsList>
          </Tabs>
          <div className="flex items-center gap-1 ml-2">
            <Button
              variant="ghost"
              size="icon"
              onClick={handleUndo}
              disabled={historyIndex <= 0}
              title="Undo (Ctrl+Z)"
            >
              <Undo className="h-4 w-4" />
            </Button>
            <Button
              variant="ghost"
              size="icon"
              onClick={handleRedo}
              disabled={historyIndex >= history.length - 1}
              title="Redo (Ctrl+Y)"
            >
              <Redo className="h-4 w-4" />
            </Button>
          </div>
        </div>

        <div className="flex items-center gap-2">
          <EnvUnlockStatus />
          {hasUnsavedChanges && (
            <Badge variant="secondary" className="text-xs">
              Unsaved changes
            </Badge>
          )}
        </div>
      </div>
      <EnvUnlockDialog
        open={unlockDialogOpen}
        onOpenChange={(open) => {
          setUnlockDialogOpen(open)
          if (!open) setPendingRevealIndex(null)
        }}
        onUnlocked={handleUnlocked}
      />

      {view === 'table' && (
        <div className="space-y-4">
          <div className="flex items-center gap-3">
            <TableSearch
              value={envQuery}
              onChange={setEnvQuery}
              placeholder="Filter env vars…"
              className="w-full sm:w-56"
            />
            {envQuery && filteredIndexed.length === 0 && (
              <span className="text-sm text-muted-foreground">No matches.</span>
            )}
          </div>
          <div className="hidden md:block rounded-md border">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="w-[250px]">Key</TableHead>
                  <TableHead>Value</TableHead>
                  <TableHead className="w-[80px]">Visible</TableHead>
                  <TableHead className="w-[80px] text-right">Actions</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {filteredIndexed.map((entry) => {
                  const i = entry._originalIndex
                  return (
                  <TableRow key={entry.key || `entry-${i}`}>
                    <TableCell>
                      <Input
                        value={entry.key}
                        onChange={(e) => handleEntryChange(i, 'key', e.target.value)}
                        placeholder="KEY"
                        disabled={entry.comment}
                        aria-label={`Environment variable key ${i + 1}`}
                      />
                    </TableCell>
                    <TableCell>
                      {entry.comment ? (
                        <span className="italic text-muted-foreground">{entry.value}</span>
                      ) : entry.sensitive ? (
                        <div className="flex items-center gap-2">
                          <Input
                            type={entry.sensitive ? 'password' : 'text'}
                            value={entry.value}
                            onChange={(e) => handleEntryChange(i, 'value', e.target.value)}
                            placeholder="value"
                            aria-label={`Environment variable value ${i + 1}`}
                          />
                          <Button
                            variant="ghost"
                            size="icon"
                            onClick={() => toggleVisibility(i)}
                            aria-label={`Toggle visibility for entry ${i + 1}`}
                          >
                            {entry.sensitive ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
                          </Button>
                        </div>
                      ) : (
                        <Input
                          value={entry.value}
                          onChange={(e) => handleEntryChange(i, 'value', e.target.value)}
                          placeholder="value"
                          aria-label={`Environment variable value ${i + 1}`}
                        />
                      )}
                    </TableCell>
                    <TableCell>
                      {entry.comment ? (
                        <span className="text-muted-foreground text-sm">comment</span>
                      ) : (
                        <input
                          type="checkbox"
                          checked={!entry.sensitive}
                          onChange={(e) => handleEntryChange(i, 'sensitive', !e.target.checked)}
                          disabled={isSensitiveKey(entry.key)}
                          className="h-4 w-4"
                          aria-label={`Toggle visibility for entry ${i + 1}`}
                        />
                      )}
                    </TableCell>
                    <TableCell className="text-right">
                      <Button
                        variant="ghost"
                        size="icon"
                        onClick={() => handleDeleteEntry(i)}
                        aria-label={`Delete entry ${i + 1}`}
                      >
                        <Trash2 className="h-4 w-4" />
                      </Button>
                    </TableCell>
                  </TableRow>
                  )
                })}
              </TableBody>
            </Table>
          </div>

          <div className="md:hidden space-y-3">
            {filteredIndexed.map((entry) => {
              const i = entry._originalIndex
              return (
              <div key={entry.key || `entry-${i}`} className="rounded-lg border p-4 space-y-3">
                <div>
                  <Input
                    value={entry.key}
                    onChange={(e) => handleEntryChange(i, 'key', e.target.value)}
                    placeholder="KEY"
                    disabled={entry.comment}
                    aria-label={`Environment variable key ${i + 1}`}
                  />
                </div>
                <div>
                  {entry.comment ? (
                    <span className="italic text-muted-foreground">{entry.value}</span>
                  ) : entry.sensitive ? (
                    <div className="flex items-center gap-2">
                      <Input
                        type={entry.sensitive ? 'password' : 'text'}
                        value={entry.value}
                        onChange={(e) => handleEntryChange(i, 'value', e.target.value)}
                        placeholder="value"
                        className="flex-1"
                        aria-label={`Environment variable value ${i + 1}`}
                      />
                      <Button
                        variant="ghost"
                        size="icon"
                        onClick={() => toggleVisibility(i)}
                        className="min-h-[44px] min-w-[44px]"
                        aria-label={`Toggle visibility for entry ${i + 1}`}
                      >
                        {entry.sensitive ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
                      </Button>
                    </div>
                  ) : (
                    <Input
                      value={entry.value}
                      onChange={(e) => handleEntryChange(i, 'value', e.target.value)}
                      placeholder="value"
                      aria-label={`Environment variable value ${i + 1}`}
                    />
                  )}
                </div>
                {!entry.comment && (
                  <div className="flex items-center justify-between">
                    <div className="flex items-center gap-2">
                      <input
                        type="checkbox"
                        checked={!entry.sensitive}
                        onChange={(e) => handleEntryChange(i, 'sensitive', !e.target.checked)}
                        disabled={isSensitiveKey(entry.key)}
                        className="h-4 w-4"
                        aria-label={`Toggle visibility for entry ${i + 1}`}
                      />
                      <span className="text-sm">Visible</span>
                    </div>
                    <Button
                      variant="ghost"
                      size="icon"
                      onClick={() => handleDeleteEntry(i)}
                      className="min-h-[44px] min-w-[44px]"
                      aria-label={`Delete entry ${i + 1}`}
                    >
                      <Trash2 className="h-4 w-4" />
                    </Button>
                  </div>
                )}
              </div>
              )
            })}
          </div>

          <div className="flex justify-between">
            <Button variant="outline" onClick={handleAddEntry}>
              <Plus className="mr-2 h-4 w-4" />
              Add Entry
            </Button>
            <Button onClick={handleSaveTable} disabled={saveMutation.isPending || !hasUnsavedChanges}>
              <Save className="mr-2 h-4 w-4" />
              {saveMutation.isPending ? 'Saving...' : 'Save'}
            </Button>
          </div>
        </div>
      )}

      {view === 'raw' && (
        <div className="space-y-4">
          <Textarea
            value={rawContent}
            onChange={(e) => {
              const newRaw = e.target.value
              setRawContent(newRaw)
              pushToHistory(entries, newRaw)
              setHasUnsavedChanges(true)
            }}
            placeholder="KEY=value"
            className="font-mono min-h-[400px]"
          />
          <div className="flex justify-end">
            <Button onClick={handleSaveRaw} disabled={saveMutation.isPending || !hasUnsavedChanges}>
              <Save className="mr-2 h-4 w-4" />
              {saveMutation.isPending ? 'Saving...' : 'Save'}
            </Button>
          </div>
        </div>
      )}
    </div>
  )
}
