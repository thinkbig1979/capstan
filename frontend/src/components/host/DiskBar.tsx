import { formatBytes } from '@/lib/format'

interface DiskBarProps {
  inUse: number
  reclaimable: number
}

/**
 * Docker's disk use split into what is in use and what one click can free.
 * The bar is drawn, so its meaning is carried by the aria-label and the
 * legend below it, not by colour alone.
 */
export function DiskBar({ inUse, reclaimable }: DiskBarProps) {
  const total = inUse + reclaimable
  const inUsePct = total > 0 ? (inUse / total) * 100 : 0
  const label = `${formatBytes(inUse)} in use, ${formatBytes(reclaimable)} reclaimable`
  return (
    <div className="space-y-2">
      <div
        data-testid="host-disk-bar"
        role="img"
        aria-label={label}
        className="flex h-3 w-full overflow-hidden rounded-full bg-muted"
      >
        <div className="h-full bg-info" style={{ width: `${inUsePct}%` }} />
        <div className="h-full bg-primary" style={{ width: `${total > 0 ? 100 - inUsePct : 0}%` }} />
      </div>
      <div className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground" aria-hidden="true">
        <span className="inline-flex items-center gap-1.5">
          <span className="h-2.5 w-2.5 rounded-sm bg-info" />
          In use by running or stopped stacks
        </span>
        <span className="inline-flex items-center gap-1.5">
          <span className="h-2.5 w-2.5 rounded-sm bg-primary" />
          Unused: safe to remove
        </span>
      </div>
    </div>
  )
}
