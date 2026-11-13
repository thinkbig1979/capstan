import { Link } from 'react-router'
import { ArrowRight } from 'lucide-react'
import { Skeleton } from '@/components/ui/skeleton'
import { RefreshFailedNotice } from '@/components/RefreshFailedNotice'
import { useHostSummary } from '@/hooks/useHostSpace'
import { HOST_VIEW_ITEMS, hostTiles, type HostView } from '@/components/host/host-views'

const UNAVAILABLE = 'unavailable'

interface Tile {
  view: HostView
  label: string
  /** Undefined while the first read is in flight. */
  value?: string
  sub?: string
  reclaimable?: boolean
}

/**
 * The Docker host at a glance under the stacks table: five tiles, each a link
 * to its /host view, and a link to the overview (agent-os-hzxs, replaces the
 * plain HostStrip line).
 */
export function HostTiles() {
  const { data: summary, isPending, isError, refetch } = useHostSummary()

  // agent-os-p9e1: a failed first read is "unavailable", never a 0 nobody
  // measured. A failed refetch keeps the last summary and says so with the
  // same notice as the Host page (agent-os-wczm).
  const tiles: Tile[] = summary
    ? hostTiles(summary)
    : HOST_VIEW_ITEMS.map(({ view, label }) => ({ view, label, value: isPending ? undefined : UNAVAILABLE }))

  return (
    <section aria-labelledby="host-tiles-heading" data-testid="host-tiles" className="space-y-2">
      <div className="flex items-center justify-between gap-2">
        <h2 id="host-tiles-heading" className="text-label">
          Docker host
        </h2>
        <Link
          to="/host"
          className="inline-flex min-h-[44px] items-center gap-1 text-sm font-medium text-primary hover:underline sm:min-h-0"
        >
          Open host overview
          <ArrowRight className="h-3.5 w-3.5" aria-hidden="true" />
        </Link>
      </div>
      {isError && !!summary && (
        <RefreshFailedNotice what="the Docker host summary" onRetry={() => void refetch()} />
      )}
      <div className="grid grid-cols-2 gap-2 sm:grid-cols-3 lg:grid-cols-5">
        {tiles.map((t) => (
          <Link
            key={t.view}
            to={`/host/${t.view}`}
            data-testid={`host-tile-${t.view}`}
            className="flex min-h-[44px] flex-col gap-0.5 rounded-lg border bg-card px-3 py-2.5 transition-colors hover:border-primary/40 hover:bg-accent/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          >
            <span className="text-xs font-medium text-muted-foreground">{t.label}</span>
            {t.value === undefined ? (
              <Skeleton className="my-0.5 h-5 w-16" />
            ) : (
              <span data-testid="host-tile-value" className="font-mono text-base font-semibold tabular-nums">
                {t.value}
              </span>
            )}
            {t.sub && (
              <span
                data-testid="host-tile-sub"
                className={`text-xs ${t.reclaimable ? 'font-medium text-primary' : 'text-muted-foreground'}`}
              >
                {t.sub}
              </span>
            )}
          </Link>
        ))}
      </div>
    </section>
  )
}
