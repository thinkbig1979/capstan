import { useState } from 'react'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { Badge } from '@/components/ui/badge'
import { Eye, EyeOff, Plus, Trash2, Save } from 'lucide-react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { apiClient } from '@/lib/api'
import { toast } from 'sonner'
import type { EnvEntry } from '@/types'
import { Switch } from '@/components/ui/switch'

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

  const { data: envData, isLoading, isError } = useQuery({
    queryKey: ['stack', stackId, 'env'],
    queryFn: async () => {
      try {
        const response = await apiClient.get(`/stacks/${stackId}/env`)
        return response.data as { entries: EnvEntry[]; raw: string }
      } catch (error: { response?: { status?: number } }) {
        if (error.response?.status === 404) {
          return null
        }
        throw error
      }
    },
    onSuccess: (data) => {
      if (data) {
        setEntries(data.entries)
        setRawContent(data.raw)
        setHasUnsavedChanges(false)
        setShowEnvSection(true)
      } else {
        setShowEnvSection(false)
      }
    },
  })

  const saveMutation = useMutation({
    mutationFn: async ({ entries, raw }: { entries?: EnvEntry[]; raw?: string }) => {
      const response = await apiClient.put(`/stacks/${stackId}/env`, {
        ...(entries !== undefined && { entries }),
        ...(raw !== undefined && { raw }),
      })
      return response.data
    },
    onSuccess: () => {
      setHasUnsavedChanges(false)
      toast.success('Environment variables saved successfully')
      queryClient.invalidateQueries({ queryKey: ['stack', stackId] })
    },
    onError: () => {
      toast.error('Failed to save environment variables')
    },
  })

  const handleAddEntry = () => {
    setEntries([...entries, { key: '', value: '', sensitive: false }])
    setHasUnsavedChanges(true)
  }

  const handleDeleteEntry = (index: number) => {
    const newEntries = entries.filter((_, i) => i !== index)
    setEntries(newEntries)
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
        <Tabs value={view} onValueChange={(v: 'table' | 'raw') => setView(v)}>
          <TabsList>
            <TabsTrigger value="table">Table View</TabsTrigger>
            <TabsTrigger value="raw">Raw Editor</TabsTrigger>
          </TabsList>
        </Tabs>

        {hasUnsavedChanges && (
          <Badge variant="secondary" className="text-xs">
            Unsaved changes
          </Badge>
        )}
      </div>

      {view === 'table' && (
        <div className="space-y-4">
          <div className="rounded-md border">
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
                          />
                          <Button
                            variant="ghost"
                            size="icon"
                            onClick={() => toggleVisibility(index)}
                            title="Toggle visibility"
                          >
                            {entry.sensitive ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
                          </Button>
                        </div>
                      ) : (
                        <Input
                          value={entry.value}
                          onChange={(e) => handleEntryChange(index, 'value', e.target.value)}
                          placeholder="value"
                        />
                      )}
                    </TableCell>
                    <TableCell>
                      {entry.comment ? (
                        <span className="text-muted-foreground text-sm">comment</span>
                      ) : (
                        <Switch
                          checked={!entry.sensitive}
                          onCheckedChange={(checked) => handleEntryChange(index, 'sensitive', !checked)}
                          disabled={isSensitiveKey(entry.key)}
                        />
                      )}
                    </TableCell>
                    <TableCell className="text-right">
                      <Button
                        variant="ghost"
                        size="icon"
                        onClick={() => handleDeleteEntry(index)}
                        title="Delete"
                      >
                        <Trash2 className="h-4 w-4" />
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
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
              setRawContent(e.target.value)
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
