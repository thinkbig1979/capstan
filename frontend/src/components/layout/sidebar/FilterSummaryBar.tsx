interface FilterSummaryBarProps {
  visibleCount: number
  totalCount: number
  onClear: () => void
}

export function FilterSummaryBar({ visibleCount, totalCount, onClear }: FilterSummaryBarProps) {
  return (
    <div className="px-3 py-1 border-b text-[10px] text-muted-foreground">
      {visibleCount} of {totalCount} stacks
      <button
        type="button"
        onClick={onClear}
        className="ml-1.5 text-primary hover:underline"
      >
        Clear
      </button>
    </div>
  )
}
