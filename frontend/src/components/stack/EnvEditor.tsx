import { useState, useEffect, useCallback } from 'react'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { Badge } from '@/components/ui/badge'
import { Eye, EyeOff, Plus, Trash2, Save, Undo, Redo } from 'lucide-react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { apiClient } from '@/lib/api'
import { toast } from 'sonner'
import type { EnvEntry } from '@/types'

interface EnvEditorProps {
  stackId: string
}

export function EnvEditor({ stackId }: EnvEditorProps) {
  const queryClient = useQueryClient()
  const [view, setView] = useState<'table' | 'raw'>('table')
  const [entries, setEntries] = useState<EnvEntry[]>([])
  const [rawContent, setRawContent] = useState('')
  const [hasUnsavedChanges, setHasUnsavedChanges] = useState(false)
  const [showEnvSection, setShowEnvSection] = useState(false)
  const [history, setHistory] = useState<{ entries: EnvEntry[]; raw: string }[]>([])
  const [historyIndex, setHistoryIndex] = useState(-1)
  const MAX_HISTORY = 50

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
      setHistoryIndex(historyIndex - 1)
      setHasUnsavedChanges(true)
    }
  }, [history, historyIndex])

  const handleRedo = useCallback(() => {
    if (historyIndex < history.length - 1) {
      const next = history[historyIndex + 1]
      setEntries(next.entries)
      setRawContent(next.raw)
      setHistoryIndex(historyIndex + 1)
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
        const response = await apiClient.get(`/stacks/${stackId}/env`)
        return response.data as { entries: EnvEntry[]; raw: string } | undefined
      } catch (error: unknown) {
        const err = error as { response?: { status?: number } }
        if (err.response?.status === 404) {
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

  const saveMutation = useMutation({
    mutationFn: async ({ entries, raw }: { entries?: EnvEntry[]; raw?: string }) => {
      const response = await apiClient.put(`/stacks/${stackId}/env`, {
        ...(entries !== undefined && { entries }),
        ...(raw !== undefined && { raw }),
      })
      return response.data
    },
    onSuccess: (_, variables) => {
      setHasUnsavedChanges(false)
      toast.success('Environment variables saved successfully')
      queryClient.invalidateQueries({ queryKey: ['stack', stackId] })
      setHistory([{ entries: variables.entries || entries, raw: variables.raw || rawContent }])
      setHistoryIndex(0)
    },
    onError: () => {
      toast.error('Failed to save environment variables')
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

  const toggleVisibility = (index: number) => {
    const newEntries = [...entries]
    newEntries[index].sensitive = !newEntries[index].sensitive
    setEntries(newEntries)
  }

  const isSensitiveKey = (key: string) => {
    const sensitivePatterns = ['_KEY', '_SECRET', '_PASSWORD', '_TOKEN', '_API_']
    return sensitivePatterns.some((pattern) => key.toUpperCase().includes(pattern))
  }

  if (isLoading) {
    return <div className="flex items-center justify-center py-8">Loading...</div>
  }

  if (isError || !envData) {
    return (
      <div className="flex flex-col items-center justify-center py-8 text-muted-foreground">
        <p>No environment file found for this stack</p>
        <Button variant="outline" onClick={() => setShowEnvSection(true)} className="mt-4">
          Create Environment File
        </Button>
      </div>
    )
  }

  if (!showEnvSection) {
    return (
      <div className="flex flex-col items-center justify-center py-8 text-muted-foreground">
        <p>No environment file found for this stack</p>
        <Button variant="outline" onClick={() => setShowEnvSection(true)} className="mt-4">
          Create Environment File
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

        {hasUnsavedChanges && (
          <Badge variant="secondary" className="text-xs">
            Unsaved changes
          </Badge>
        )}
      </div>

      {view === 'table' && (
        <div className="space-y-4">
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
                {entries.map((entry, index) => (
                  <TableRow key={index}>
                    <TableCell>
                      <Input
                        value={entry.key}
                        onChange={(e) => handleEntryChange(index, 'key', e.target.value)}
                        placeholder="KEY"
                        disabled={entry.comment}
                        aria-label={`Environment variable key ${index + 1}`}
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
                            onChange={(e) => handleEntryChange(index, 'value', e.target.value)}
                            placeholder="value"
                            aria-label={`Environment variable value ${index + 1}`}
                          />
                          <Button
                            variant="ghost"
                            size="icon"
                            onClick={() => toggleVisibility(index)}
                            aria-label={`Toggle visibility for entry ${index + 1}`}
                          >
                            {entry.sensitive ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
                          </Button>
                        </div>
                      ) : (
                        <Input
                          value={entry.value}
                          onChange={(e) => handleEntryChange(index, 'value', e.target.value)}
                          placeholder="value"
                          aria-label={`Environment variable value ${index + 1}`}
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
                          onChange={(e) => handleEntryChange(index, 'sensitive', !e.target.checked)}
                          disabled={isSensitiveKey(entry.key)}
                          className="h-4 w-4"
                          aria-label={`Toggle visibility for entry ${index + 1}`}
                        />
                      )}
                    </TableCell>
                    <TableCell className="text-right">
                      <Button
                        variant="ghost"
                        size="icon"
                        onClick={() => handleDeleteEntry(index)}
                        aria-label={`Delete entry ${index + 1}`}
                      >
                        <Trash2 className="h-4 w-4" />
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>

          <div className="md:hidden space-y-3">
            {entries.map((entry, index) => (
              <div key={index} className="rounded-lg border p-4 space-y-3">
                <div>
                  <Input
                    value={entry.key}
                    onChange={(e) => handleEntryChange(index, 'key', e.target.value)}
                    placeholder="KEY"
                    disabled={entry.comment}
                    aria-label={`Environment variable key ${index + 1}`}
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
                        onChange={(e) => handleEntryChange(index, 'value', e.target.value)}
                        placeholder="value"
                        className="flex-1"
                        aria-label={`Environment variable value ${index + 1}`}
                      />
                      <Button
                        variant="ghost"
                        size="icon"
                        onClick={() => toggleVisibility(index)}
                        className="min-h-[44px] min-w-[44px]"
                        aria-label={`Toggle visibility for entry ${index + 1}`}
                      >
                        {entry.sensitive ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
                      </Button>
                    </div>
                  ) : (
                    <Input
                      value={entry.value}
                      onChange={(e) => handleEntryChange(index, 'value', e.target.value)}
                      placeholder="value"
                      aria-label={`Environment variable value ${index + 1}`}
                    />
                  )}
                </div>
                {!entry.comment && (
                  <div className="flex items-center justify-between">
                    <div className="flex items-center gap-2">
                      <input
                        type="checkbox"
                        checked={!entry.sensitive}
                        onChange={(e) => handleEntryChange(index, 'sensitive', !e.target.checked)}
                        disabled={isSensitiveKey(entry.key)}
                        className="h-4 w-4"
                        aria-label={`Toggle visibility for entry ${index + 1}`}
                      />
                      <span className="text-sm">Visible</span>
                    </div>
                    <Button
                      variant="ghost"
                      size="icon"
                      onClick={() => handleDeleteEntry(index)}
                      className="min-h-[44px] min-w-[44px]"
                      aria-label={`Delete entry ${index + 1}`}
                    >
                      <Trash2 className="h-4 w-4" />
                    </Button>
                  </div>
                )}
              </div>
            ))}
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
