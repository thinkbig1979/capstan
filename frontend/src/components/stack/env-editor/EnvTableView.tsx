import { useMemo } from 'react'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Eye, EyeOff, Plus, Trash2, Save } from 'lucide-react'
import type { EnvEntryDraft } from '@/types'
import { useTextFilter } from '@/hooks/useTextFilter'
import { TableSearch } from '@/components/ui/table-search'
import type { EnvEntryRow, IndexedEnvEntry } from './types'

const NO_HIDDEN_KEYS: ReadonlySet<number> = new Set()

/**
 * A bare line read without "=" (a multi-line secret's body line) is a comment
 * row whose key is its own text, so a masked one hides its key as well as its
 * value; so does a row a dirty lock found to be such a line parsed as KEY=
 * (agent-os-y6e8). A multi-line row keeps its key: that is the name.
 */
const keyHidden = (e: EnvEntryRow, hiddenKeyIds: ReadonlySet<number>) =>
  e.sensitive && ((e.comment === true && e.key === e.value) || hiddenKeyIds.has(e._rowId))

/**
 * A row the table shows but does not edit: a comment, a line compose reads
 * without "=" (agent-os-n43b), or a quoted value spanning several lines
 * (agent-os-fw2q). The backend sends the multi-line value as one row holding
 * every line; it is edited in the Raw view, and an unedited table save writes
 * it back unchanged. An unlocked session receives a secret in full, so a
 * sensitive read-only row stays masked until the eye toggle reveals it, the
 * same way a sensitive editable value does. Every keyed row (a variable) has
 * the toggle, one-line ones too (agent-os-hag4). A free-text comment has no
 * key and is shown as is, unless it is masked: the server flags a commented-out
 * secret, and a lock with unsaved edits masks every comment (agent-os-y6e8).
 */
function ReadOnlyValue({
  value,
  keyed,
  masked,
  onToggle,
  toggleLabel,
  toggleClassName,
}: {
  value: string
  keyed: boolean
  masked: boolean
  onToggle: () => void
  toggleLabel: string
  toggleClassName?: string
}) {
  const multiline = value.includes('\n')
  if (!multiline && !keyed && !masked) {
    return <span className="italic text-muted-foreground">{value}</span>
  }
  return (
    <div className="flex items-start gap-2">
      <div className="min-w-0 flex-1 space-y-1">
        <span className="block whitespace-pre-wrap break-all italic text-muted-foreground">
          {masked ? '••••••' : value}
        </span>
        {multiline && <span className="text-xs text-muted-foreground">Multi-line value, edit in Raw</span>}
      </div>
      <Button variant="ghost" size="icon" onClick={onToggle} className={toggleClassName} aria-label={toggleLabel}>
        {masked ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
      </Button>
    </div>
  )
}

interface EnvTableViewProps {
  /**
   * Kept mounted regardless of the active tab — this component owns its own
   * search-query state, and unmounting/remounting it on every tab switch
   * would reset that query. `visible` just gates the rendered output.
   */
  visible: boolean
  entries: EnvEntryRow[]
  onEntryChange: (index: number, field: keyof EnvEntryDraft, value: string | boolean) => void
  onDeleteEntry: (index: number) => void
  onAddEntry: () => void
  onToggleVisibility: (index: number) => void
  onSaveTable: () => void
  saving: boolean
  hasUnsavedChanges: boolean
  /**
   * True when the backend withheld the sensitive values because no unlock token
   * was in play. Saving from that state would persist the blanks it sent us, and
   * the backend refuses the write anyway — so the button is disabled rather than
   * left to fail (agent-os-7o5s).
   */
  locked: boolean
  /** The rows kept from the locked view could not be filled (agent-os-yt2f). */
  saveRefused: boolean
  /** Rows whose key a dirty lock found to be secret content (agent-os-y6e8). */
  hiddenKeyIds?: ReadonlySet<number>
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
  locked,
  saveRefused,
  hiddenKeyIds = NO_HIDDEN_KEYS,
}: EnvTableViewProps) {
  // A masked value is not searched, nor a hidden key: a match would tell a
  // locked user what it contains, one substring at a time (agent-os-kxag, y6e8).
  const searchFields = useMemo(
    () => [
      (e: IndexedEnvEntry) => (keyHidden(e, hiddenKeyIds) ? '' : e.key),
      (e: IndexedEnvEntry) => (e.sensitive ? '' : e.value),
    ],
    [hiddenKeyIds],
  )
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
  } = useTextFilter(indexedEntries, searchFields)

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
              <TableRow key={entry._rowId}>
                <TableCell>
                  <Input
                    value={keyHidden(entry, hiddenKeyIds) ? '' : entry.key}
                    onChange={(e) => onEntryChange(i, 'key', e.target.value)}
                    placeholder="KEY"
                    disabled={entry.comment || keyHidden(entry, hiddenKeyIds)}
                    aria-label={`Environment variable key ${i + 1}`}
                  />
                </TableCell>
                <TableCell>
                  {entry.comment ? (
                    <ReadOnlyValue
                      value={entry.value}
                      keyed={entry.key !== ''}
                      masked={entry.sensitive}
                      onToggle={() => onToggleVisibility(i)}
                      toggleLabel={`Toggle visibility for entry ${i + 1}`}
                    />
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
                      onChange={() => onToggleVisibility(i)}
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
          <div key={entry._rowId} className="rounded-lg border p-4 space-y-3">
            <div>
              <Input
                value={keyHidden(entry, hiddenKeyIds) ? '' : entry.key}
                onChange={(e) => onEntryChange(i, 'key', e.target.value)}
                placeholder="KEY"
                disabled={entry.comment || keyHidden(entry, hiddenKeyIds)}
                aria-label={`Environment variable key ${i + 1}`}
              />
            </div>
            <div>
              {entry.comment ? (
                <ReadOnlyValue
                  value={entry.value}
                  keyed={entry.key !== ''}
                  masked={entry.sensitive}
                  onToggle={() => onToggleVisibility(i)}
                  toggleLabel={`Toggle visibility for entry ${i + 1}`}
                  toggleClassName="min-h-[44px] min-w-[44px]"
                />
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
                    onChange={() => onToggleVisibility(i)}
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
        <Button
          onClick={onSaveTable}
          disabled={saving || !hasUnsavedChanges || locked || saveRefused}
          title={locked ? 'Unlock with your password to edit environment variables' : undefined}
        >
          <Save className="mr-2 h-4 w-4" />
          {saving ? 'Saving...' : 'Save'}
        </Button>
      </div>
    </div>
  )
}
