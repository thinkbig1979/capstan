import { Search, X } from 'lucide-react'
import { cn } from '@/lib/utils'

interface TableSearchProps {
  value: string
  onChange: (value: string) => void
  placeholder?: string
  className?: string
  /** Accessible label for the input (defaults to the placeholder). */
  ariaLabel?: string
  /** Id for the underlying input, so a page-level <label htmlFor> can target it. */
  id?: string
}

/**
 * Reusable text-filter input matching the sidebar's stack search: a leading
 * search icon and a clear (✕) button that appears once there's a query.
 */
export function TableSearch({
  value,
  onChange,
  placeholder = 'Filter…',
  className,
  ariaLabel,
  id,
}: TableSearchProps) {
  return (
    <div className={cn('relative', className)}>
      <Search className="pointer-events-none absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
      <input
        id={id}
        type="text"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        aria-label={ariaLabel ?? placeholder}
        className="h-8 w-full rounded-md border bg-background pl-7 pr-7 text-sm focus:outline-hidden focus:ring-1 focus:ring-ring"
      />
      {value && (
        <button
          type="button"
          aria-label="Clear filter"
          onClick={() => onChange('')}
          className="absolute right-1.5 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground"
        >
          <X className="h-3 w-3" />
        </button>
      )}
    </div>
  )
}
