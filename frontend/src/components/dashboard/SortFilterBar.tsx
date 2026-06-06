import { ArrowUpDown, ChevronsUpDown } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { TableSearch } from '@/components/ui/table-search'

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
  /** Optional leading help affordance (e.g. a <HelpHint/>) explaining the tab. */
  help?: React.ReactNode
  countDisplay: React.ReactNode
  /** When provided, renders a leading text-filter input. */
  searchValue?: string
  onSearchChange?: (value: string) => void
  searchPlaceholder?: string
}

export function SortFilterBar({
  sortOptions,
  sortValue,
  onSortChange,
  filterOptions,
  filterValue,
  onFilterChange,
  actions,
  help,
  countDisplay,
  searchValue,
  onSearchChange,
  searchPlaceholder,
}: SortFilterBarProps) {
  return (
    <div className="flex items-center gap-2 flex-wrap">
      {help}
      {onSearchChange && (
        <TableSearch
          value={searchValue ?? ''}
          onChange={onSearchChange}
          placeholder={searchPlaceholder ?? 'Filter…'}
          className="w-full sm:w-56"
        />
      )}
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
      <div className="flex items-center gap-3 ml-auto">
        <span className="text-sm text-muted-foreground shrink-0">{countDisplay}</span>
        {actions && (
          <div className="flex items-center gap-2 border-l pl-3">
            {actions}
          </div>
        )}
      </div>
    </div>
  )
}
