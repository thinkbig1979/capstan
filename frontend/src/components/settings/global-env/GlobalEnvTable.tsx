import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Eye, EyeOff, Trash2 } from 'lucide-react'
import { isSensitiveKey } from './sensitiveKey'
import type { EnvVar, IndexedEnvVar } from './types'

interface GlobalEnvTableProps {
  hasVars: boolean
  filtered: IndexedEnvVar[]
  query: string
  visible: Record<number, boolean>
  onChange: (index: number, field: keyof EnvVar, value: string) => void
  onToggleVisible: (index: number) => void
  onDelete: (index: number) => void
}

export function GlobalEnvTable({
  hasVars,
  filtered,
  query,
  visible,
  onChange,
  onToggleVisible,
  onDelete,
}: GlobalEnvTableProps) {
  return (
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
          {!hasVars ? (
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
                      onChange={(e) => onChange(index, 'key', e.target.value)}
                      placeholder="KEY"
                      aria-label={`Global env key ${index + 1}`}
                    />
                  </TableCell>
                  <TableCell>
                    <div className="flex items-center gap-2">
                      <Input
                        type={sensitive && !reveal ? 'password' : 'text'}
                        value={entry.value}
                        onChange={(e) => onChange(index, 'value', e.target.value)}
                        placeholder="value"
                        aria-label={`Global env value ${index + 1}`}
                      />
                      {sensitive && (
                        <Button
                          type="button"
                          variant="ghost"
                          size="icon"
                          onClick={() => onToggleVisible(index)}
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
