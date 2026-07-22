import { useMemo } from 'react'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Eye, EyeOff, Plus, Trash2, Save } from 'lucide-react'
import type { EnvEntry } from '@/types'
import { useTextFilter } from '@/hooks/useTextFilter'
import { TableSearch } from '@/components/ui/table-search'
import { isSensitiveKey } from './sensitiveKey'
import type { IndexedEnvEntry } from './types'

const ENV_SEARCH_FIELDS = [
  (e: IndexedEnvEntry) => e.key,
  (e: IndexedEnvEntry) => e.value,
]

interface EnvTableViewProps {
  /**
   * Kept mounted regardless of the active tab — this component owns its own
   * search-query state, and unmounting/remounting it on every tab switch
   * would reset that query. `visible` just gates the rendered output.
   */
  visible: boolean
  entries: EnvEntry[]
  onEntryChange: (index: number, field: keyof EnvEntry, value: string | boolean) => void
  onDeleteEntry: (index: number) => void
  onAddEntry: () => void
  onToggleVisibility: (index: number) => void
  onSaveTable: () => void
  saving: boolean
  hasUnsavedChanges: boolean
}

export function EnvTableView({
  visible,
  entries,
  onEntryChange,
  onDeleteEntry,
  onAddEntry,
  onToggleVisibility,
  onSaveTable,
  saving,
  hasUnsavedChanges,
}: EnvTableViewProps) {
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

  if (!visible) return null

  return (
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
                    onChange={(e) => onEntryChange(i, 'key', e.target.value)}
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
                        onChange={(e) => onEntryChange(i, 'value', e.target.value)}
                        placeholder="value"
                        aria-label={`Environment variable value ${i + 1}`}
                      />
                      <Button
                        variant="ghost"
                        size="icon"
                        onClick={() => onToggleVisibility(i)}
                        aria-label={`Toggle visibility for entry ${i + 1}`}
                      >
                        {entry.sensitive ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
                      </Button>
                    </div>
                  ) : (
                    <Input
                      value={entry.value}
                      onChange={(e) => onEntryChange(i, 'value', e.target.value)}
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
                      onChange={(e) => onEntryChange(i, 'sensitive', !e.target.checked)}
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
                    onClick={() => onDeleteEntry(i)}
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
                onChange={(e) => onEntryChange(i, 'key', e.target.value)}
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
                    onChange={(e) => onEntryChange(i, 'value', e.target.value)}
                    placeholder="value"
                    className="flex-1"
                    aria-label={`Environment variable value ${i + 1}`}
                  />
                  <Button
                    variant="ghost"
                    size="icon"
                    onClick={() => onToggleVisibility(i)}
                    className="min-h-[44px] min-w-[44px]"
                    aria-label={`Toggle visibility for entry ${i + 1}`}
                  >
                    {entry.sensitive ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
                  </Button>
                </div>
              ) : (
                <Input
                  value={entry.value}
                  onChange={(e) => onEntryChange(i, 'value', e.target.value)}
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
                    onChange={(e) => onEntryChange(i, 'sensitive', !e.target.checked)}
                    disabled={isSensitiveKey(entry.key)}
                    className="h-4 w-4"
                    aria-label={`Toggle visibility for entry ${i + 1}`}
                  />
                  <span className="text-sm">Visible</span>
                </div>
                <Button
                  variant="ghost"
                  size="icon"
                  onClick={() => onDeleteEntry(i)}
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
        <Button variant="outline" onClick={onAddEntry}>
          <Plus className="mr-2 h-4 w-4" />
          Add Entry
        </Button>
        <Button onClick={onSaveTable} disabled={saving || !hasUnsavedChanges}>
          <Save className="mr-2 h-4 w-4" />
          {saving ? 'Saving...' : 'Save'}
        </Button>
      </div>
    </div>
  )
}
