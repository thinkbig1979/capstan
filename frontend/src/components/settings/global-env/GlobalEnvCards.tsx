import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Eye, EyeOff, Trash2 } from 'lucide-react'
import { isSensitiveKey } from './sensitiveKey'
import type { EnvVar, IndexedEnvVar } from './types'

interface GlobalEnvCardsProps {
  hasVars: boolean
  filtered: IndexedEnvVar[]
  query: string
  visible: Record<number, boolean>
  onChange: (index: number, field: keyof EnvVar, value: string) => void
  onToggleVisible: (index: number) => void
  onDelete: (index: number) => void
}

export function GlobalEnvCards({
  hasVars,
  filtered,
  query,
  visible,
  onChange,
  onToggleVisible,
  onDelete,
}: GlobalEnvCardsProps) {
  return (
    <div className="md:hidden space-y-3">
      {!hasVars ? (
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
                onChange={(e) => onChange(index, 'key', e.target.value)}
                placeholder="KEY"
                aria-label={`Global env key ${index + 1}`}
              />
              <div className="flex items-center gap-2">
                <Input
                  type={sensitive && !reveal ? 'password' : 'text'}
                  value={entry.value}
                  onChange={(e) => onChange(index, 'value', e.target.value)}
                  placeholder="value"
                  className="flex-1"
                  aria-label={`Global env value ${index + 1}`}
                />
                {sensitive && (
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon"
                    onClick={() => onToggleVisible(index)}
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
                  onClick={() => onDelete(index)}
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
  )
}
