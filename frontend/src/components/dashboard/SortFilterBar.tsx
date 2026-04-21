import { ArrowUpDown, ChevronsUpDown } from 'lucide-react'
import { Button } from '@/components/ui/button'

export interface SortOption {
  key: string
  label: string
}

export interface FilterOption {
  key: string
  label: string
}

interface SortFilterBarProps {
  sortOptions: SortOption[]
  sortValue: string
  onSortChange: (key: string) => void
  filterOptions?: FilterOption[]
  filterValue?: string
  onFilterChange?: (key: string) => void
  actions?: React.ReactNode
  countDisplay: React.ReactNode
}

export function SortFilterBar({
  sortOptions,
  sortValue,
  onSortChange,
  filterOptions,
  filterValue,
  onFilterChange,
  actions,
  countDisplay,
}: SortFilterBarProps) {
  return (
    <div className="flex items-center gap-2 flex-wrap">
      <ArrowUpDown className="h-4 w-4 text-muted-foreground shrink-0" />
      <span className="text-sm text-muted-foreground shrink-0">Sort:</span>
      <div className="flex gap-1">
        {sortOptions.map(({ key, label }) => (
          <Button
            key={key}
            variant={sortValue === key ? 'default' : 'ghost'}
            size="sm"
            className="h-7 text-xs"
            onClick={() => onSortChange(key)}
          >
            {label}
          </Button>
        ))}
      </div>
      {filterOptions && filterOptions.length > 0 && onFilterChange && (
        <>
          <ChevronsUpDown className="h-4 w-4 text-muted-foreground shrink-0 ml-1" />
          <span className="text-sm text-muted-foreground shrink-0">Filter:</span>
          <div className="flex gap-1">
            {filterOptions.map(({ key, label }) => (
              <Button
                key={key}
                variant={filterValue === key ? 'default' : 'ghost'}
                size="sm"
                className="h-7 text-xs"
                onClick={() => onFilterChange(key)}
              >
                {label}
              </Button>
            ))}
          </div>
        </>
      )}
      {actions && (
        <div className="flex items-center gap-2 ml-2">
          {actions}
        </div>
      )}
      <span className="text-sm text-muted-foreground ml-auto shrink-0">{countDisplay}</span>
    </div>
  )
}
