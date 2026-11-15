import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Eye, EyeOff, Trash2 } from 'lucide-react'
import { isSensitiveKey } from '@/components/stack/env-editor/sensitiveKey'
import type { EditableField, IndexedEnvVar } from './types'
import { ReadOnlyValue } from './ReadOnlyValue'
import { valuePlaceholder } from '@/components/stack/env-editor/hiddenValue'

/** The single source of truth for this table's width: the header cells and the
 * spanning row's colSpan both derive from it, so adding a column cannot leave
 * the spanning row under-spanning the body (agent-os-o9fx). */
const GLOBAL_ENV_COLUMNS: readonly { label: string; className?: string }[] = [
  { label: 'Key', className: 'w-[260px]' },
  { label: 'Value' },
  { label: 'Actions', className: 'w-[80px] text-right' },
]

interface GlobalEnvTableProps {
  hasVars: boolean
  filtered: IndexedEnvVar[]
  query: string
  /** Revealed rows, by `_rowId`. */
  visible: Record<number, boolean>
  onChange: (index: number, field: EditableField, value: string) => void
  onToggleVisible: (rowId: number) => void
  onDelete: (index: number) => void
  /** The payload on screen was served locked: a blank secret is a hidden one. */
  locked?: boolean
}

export function GlobalEnvTable({
  hasVars,
  filtered,
  query,
  visible,
  onChange,
  onToggleVisible,
  onDelete,
  locked = false,
}: GlobalEnvTableProps) {
  return (
    <div className="hidden md:block rounded-md border">
      <Table>
        <TableHeader>
          <TableRow>
            {GLOBAL_ENV_COLUMNS.map((column) => (
              <TableHead key={column.label} className={column.className}>{column.label}</TableHead>
            ))}
          </TableRow>
        </TableHeader>
        <TableBody>
          {!hasVars ? (
            <TableRow>
              <TableCell colSpan={GLOBAL_ENV_COLUMNS.length} className="text-center text-sm text-muted-foreground py-6">
                No global variables yet. Add one to get started.
              </TableCell>
            </TableRow>
          ) : filtered.length === 0 ? (
            <TableRow>
              <TableCell colSpan={GLOBAL_ENV_COLUMNS.length} className="text-center text-sm text-muted-foreground py-6">
                No variables match &quot;{query}&quot;.
              </TableCell>
            </TableRow>
          ) : (
            filtered.map((entry) => {
              const index = entry._originalIndex
              // The server's flag covers a secret value under a harmless key
              // (agent-os-ruuw); the name covers a key typed since the last load.
              const sensitive = entry.sensitive === true || isSensitiveKey(entry.key)
              const reveal = !!visible[entry._rowId]
              // A line the server keeps verbatim (agent-os-jb5g), or a multi-line value.
              const readOnly = entry.readOnly === true || entry.value.includes('\n')
              return (
                <TableRow key={`row-${index}`}>
                  <TableCell>
                    <Input
                      value={entry.keyHidden ? '' : entry.key}
                      onChange={(e) => onChange(index, 'key', e.target.value)}
                      placeholder="KEY"
                      disabled={readOnly || entry.keyHidden}
                      aria-label={`Global env key ${index + 1}`}
                    />
                  </TableCell>
                  <TableCell>
                    <div className="flex items-center gap-2">
                      {readOnly ? (
                        <ReadOnlyValue value={entry.value} masked={sensitive && !reveal} />
                      ) : (
                        <Input
                          type={sensitive && !reveal ? 'password' : 'text'}
                          value={entry.value}
                          onChange={(e) => onChange(index, 'value', e.target.value)}
                          placeholder={valuePlaceholder(locked, sensitive && !reveal, entry.value)}
                          disabled={entry.valueLocked}
                          aria-label={`Global env value ${index + 1}`}
                        />
                      )}
                      {sensitive && (
                        <Button
                          type="button"
                          variant="ghost"
                          size="icon"
                          onClick={() => onToggleVisible(entry._rowId)}
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
                      onClick={() => onDelete(index)}
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
  )
}
