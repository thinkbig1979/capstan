import { useEffect, useMemo, useState } from 'react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { TableSearch } from '@/components/ui/table-search'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { useTextFilter } from '@/hooks/useTextFilter'
import { LoadingSpinner } from '@/components/LoadingSkeleton'
import { Badge } from '@/components/ui/badge'
import { Eye, EyeOff, Plus, Save, Trash2 } from 'lucide-react'
import { useGlobalEnv, useUpdateGlobalEnv } from '@/hooks/useResources'
import { useEnvUnlockStore } from '@/stores/envUnlockStore'
import { EnvUnlockDialog } from '@/components/EnvUnlockDialog'
import { EnvUnlockStatus } from '@/components/EnvUnlockStatus'
import { HelpHint } from '@/components/ui/help-hint'
import { classifyError } from '@/lib/error-handler'
import { useAuth } from '@/hooks/useAuth'
import { toast } from 'sonner'

type EnvVar = { key: string; value: string }

// Indexed wrapper so the text-filter result can carry the original vars[] index,
// ensuring handleChange / handleDelete / toggleVisible always target the right entry.
type IndexedEnvVar = EnvVar & { _originalIndex: number }

const ENV_SEARCH_FIELDS = [
  (e: IndexedEnvVar) => e.key,
  (e: IndexedEnvVar) => e.value,
]

const SENSITIVE_PATTERNS = ['_KEY', '_SECRET', '_PASSWORD', '_TOKEN', '_API_']

function isSensitiveKey(key: string) {
  const upper = key.toUpperCase()
  return SENSITIVE_PATTERNS.some((p) => upper.includes(p))
}

export function GlobalEnvSettingsContent() {
  const { data, isLoading, isError } = useGlobalEnv()
  const updateGlobalEnv = useUpdateGlobalEnv()
  const { authDisabled } = useAuth()
  const isUnlocked = useEnvUnlockStore((s) => s.isUnlocked)
  const unlockedUntil = useEnvUnlockStore((s) => s.unlockedUntil)

  const [vars, setVars] = useState<EnvVar[]>([])
  const [visible, setVisible] = useState<Record<number, boolean>>({})
  const [dirty, setDirty] = useState(false)
  const [unlockDialogOpen, setUnlockDialogOpen] = useState(false)
  const [pendingRevealIndex, setPendingRevealIndex] = useState<number | null>(null)

  useEffect(() => {
    if (!data) return
    // Sync server state into local edit buffer when query resolves or refetches.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setVars(data.vars ?? [])
     
    setVisible({})
     
    setDirty(false)
  }, [data])

  // Re-mask all values when the unlock session ends.
  useEffect(() => {
    if (unlockedUntil !== null) return
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setVisible({})
  }, [unlockedUntil])

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

  const toggleVisible = (index: number) => {
    if (visible[index]) {
      // Hiding never requires unlock.
      setVisible((prev) => ({ ...prev, [index]: false }))
      return
    }
    if (authDisabled || isUnlocked()) {
      setVisible((prev) => ({ ...prev, [index]: true }))
      return
    }
    setPendingRevealIndex(index)
    setUnlockDialogOpen(true)
  }

  const handleUnlocked = () => {
    if (pendingRevealIndex !== null) {
      setVisible((prev) => ({ ...prev, [pendingRevealIndex]: true }))
    }
    setPendingRevealIndex(null)
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
        <div className="flex items-center gap-2 shrink-0">
          <HelpHint label="Hidden values" title="Hidden values" align="end">
            <p>
              Keys that look like secrets (passwords, tokens, API keys) stay masked in the table.
            </p>
            <p>
              Revealing one starts a short unlock session protected by your password, and values
              re-hide when it ends.
            </p>
          </HelpHint>
          <EnvUnlockStatus />
          {dirty && (
            <Badge variant="secondary" className="text-xs whitespace-nowrap">
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

      {vars.length > 0 && (
        <TableSearch
          value={query}
          onChange={setQuery}
          placeholder="Filter by key or value…"
          className="w-full sm:w-64"
        />
      )}

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
            ) : filtered.length === 0 ? (
              <TableRow>
                <TableCell colSpan={3} className="text-center text-sm text-muted-foreground py-6">
                  No variables match &quot;{query}&quot;.
                </TableCell>
              </TableRow>
            ) : (
              filtered.map((entry) => {
                const index = entry._originalIndex
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
        ) : filtered.length === 0 ? (
          <div className="rounded-md border p-4 text-center text-sm text-muted-foreground">
            No variables match &quot;{query}&quot;.
          </div>
        ) : (
          filtered.map((entry) => {
            const index = entry._originalIndex
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
