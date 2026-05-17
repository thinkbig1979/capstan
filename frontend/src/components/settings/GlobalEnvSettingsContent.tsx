import { useEffect, useState } from 'react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { LoadingSpinner } from '@/components/LoadingSkeleton'
import { Badge } from '@/components/ui/badge'
import { Eye, EyeOff, Plus, Save, Trash2 } from 'lucide-react'
import { useGlobalEnv, useUpdateGlobalEnv } from '@/hooks/useResources'
import { classifyError } from '@/lib/error-handler'
import { toast } from 'sonner'

type EnvVar = { key: string; value: string }

const SENSITIVE_PATTERNS = ['_KEY', '_SECRET', '_PASSWORD', '_TOKEN', '_API_']

function isSensitiveKey(key: string) {
  const upper = key.toUpperCase()
  return SENSITIVE_PATTERNS.some((p) => upper.includes(p))
}

export function GlobalEnvSettingsContent() {
  const { data, isLoading, isError } = useGlobalEnv()
  const updateGlobalEnv = useUpdateGlobalEnv()

  const [vars, setVars] = useState<EnvVar[]>([])
  const [visible, setVisible] = useState<Record<number, boolean>>({})
  const [dirty, setDirty] = useState(false)

  useEffect(() => {
    if (!data) return
    // Sync server state into local edit buffer when query resolves or refetches.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setVars(data.vars ?? [])
     
    setVisible({})
     
    setDirty(false)
  }, [data])

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

  const toggleVisible = (index: number) => {
    setVisible((prev) => ({ ...prev, [index]: !prev[index] }))
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

  if (isLoading) {
    return <div className="py-4"><LoadingSpinner /></div>
  }

  if (isError) {
    return (
      <div className="py-4 text-sm text-destructive">
        Failed to load global environment variables.
      </div>
    )
  }

  return (
    <div className="space-y-4">
      <div className="flex items-start justify-between gap-4">
        <p className="text-sm text-muted-foreground">
          Variables here apply to every stack. Docker Compose loads them before each
          stack&apos;s own <code className="text-xs">.env</code>, which can still override them.
          Changes take effect on the next stack restart.
        </p>
        {dirty && (
          <Badge variant="secondary" className="text-xs whitespace-nowrap">
            Unsaved changes
          </Badge>
        )}
      </div>

      <div className="hidden md:block rounded-md border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-[260px]">Key</TableHead>
              <TableHead>Value</TableHead>
              <TableHead className="w-[80px] text-right">Actions</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {vars.length === 0 ? (
              <TableRow>
                <TableCell colSpan={3} className="text-center text-sm text-muted-foreground py-6">
                  No global variables yet. Add one to get started.
                </TableCell>
              </TableRow>
            ) : (
              vars.map((entry, index) => {
                const sensitive = isSensitiveKey(entry.key)
                const reveal = !!visible[index]
                return (
                  <TableRow key={`row-${index}`}>
                    <TableCell>
                      <Input
                        value={entry.key}
                        onChange={(e) => handleChange(index, 'key', e.target.value)}
                        placeholder="KEY"
                        aria-label={`Global env key ${index + 1}`}
                      />
                    </TableCell>
                    <TableCell>
                      <div className="flex items-center gap-2">
                        <Input
                          type={sensitive && !reveal ? 'password' : 'text'}
                          value={entry.value}
                          onChange={(e) => handleChange(index, 'value', e.target.value)}
                          placeholder="value"
                          aria-label={`Global env value ${index + 1}`}
                        />
                        {sensitive && (
                          <Button
                            type="button"
                            variant="ghost"
                            size="icon"
                            onClick={() => toggleVisible(index)}
                            title={reveal ? 'Hide value' : 'Show value'}
                            aria-label={reveal ? `Hide value ${index + 1}` : `Show value ${index + 1}`}
                          >
                            {reveal ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
                          </Button>
                        )}
                      </div>
                    </TableCell>
                    <TableCell className="text-right">
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon"
                        onClick={() => handleDelete(index)}
                        title="Remove variable"
                        aria-label={`Remove global env ${index + 1}`}
                      >
                        <Trash2 className="h-4 w-4" />
                      </Button>
                    </TableCell>
                  </TableRow>
                )
              })
            )}
          </TableBody>
        </Table>
      </div>

      <div className="md:hidden space-y-3">
        {vars.length === 0 ? (
          <div className="rounded-md border p-4 text-center text-sm text-muted-foreground">
            No global variables yet. Add one to get started.
          </div>
        ) : (
          vars.map((entry, index) => {
            const sensitive = isSensitiveKey(entry.key)
            const reveal = !!visible[index]
            return (
              <div key={`mrow-${index}`} className="rounded-lg border p-4 space-y-3">
                <Input
                  value={entry.key}
                  onChange={(e) => handleChange(index, 'key', e.target.value)}
                  placeholder="KEY"
                  aria-label={`Global env key ${index + 1}`}
                />
                <div className="flex items-center gap-2">
                  <Input
                    type={sensitive && !reveal ? 'password' : 'text'}
                    value={entry.value}
                    onChange={(e) => handleChange(index, 'value', e.target.value)}
                    placeholder="value"
                    className="flex-1"
                    aria-label={`Global env value ${index + 1}`}
                  />
                  {sensitive && (
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon"
                      onClick={() => toggleVisible(index)}
                      className="min-h-[44px] min-w-[44px]"
                      title={reveal ? 'Hide value' : 'Show value'}
                      aria-label={reveal ? `Hide value ${index + 1}` : `Show value ${index + 1}`}
                    >
                      {reveal ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
                    </Button>
                  )}
                </div>
                <div className="flex justify-end">
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon"
                    onClick={() => handleDelete(index)}
                    className="min-h-[44px] min-w-[44px]"
                    title="Remove variable"
                    aria-label={`Remove global env ${index + 1}`}
                  >
                    <Trash2 className="h-4 w-4" />
                  </Button>
                </div>
              </div>
            )
          })
        )}
      </div>

      <div className="flex justify-between">
        <Button type="button" variant="outline" onClick={handleAdd}>
          <Plus className="mr-2 h-4 w-4" />
          Add Variable
        </Button>
        <Button
          type="button"
          onClick={handleSave}
          disabled={updateGlobalEnv.isPending || !dirty}
        >
          {updateGlobalEnv.isPending ? (
            <>
              <span className="mr-2"><LoadingSpinner size="small" /></span>
              Saving...
            </>
          ) : (
            <>
              <Save className="mr-2 h-4 w-4" />
              Save
            </>
          )}
        </Button>
      </div>
    </div>
  )
}
