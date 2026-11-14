import { Link, useLocation, useNavigate, useSearchParams } from 'react-router'
import { Button } from '@/components/ui/button'
import { useQuery, type UseQueryResult } from '@tanstack/react-query'
import { Skeleton } from '@/components/ui/skeleton'
import { ErrorBoundary } from '@/components/ErrorBoundary'
import { LoadFailedNotice } from '@/components/LoadFailedNotice'
import { RefreshFailedNotice } from '@/components/RefreshFailedNotice'
import { DiskBar } from '@/components/host/DiskBar'
import { CategoryCard } from '@/components/host/CategoryCard'
import { FreeUpSpaceDialog } from '@/components/host/FreeUpSpaceDialog'
import { AutoCleanupPanel } from '@/components/host/AutoCleanupPanel'
import { HostLiveRow } from '@/components/host/HostLiveRow'
import { BusiestContainers } from '@/components/host/BusiestContainers'
import { HOST_VIEW_ITEMS, hostCards, hostViewHref, isHostView, type HostView } from '@/components/host/host-views'
import { ContainersOverviewTab } from '@/components/dashboard/ContainersOverviewTab'
import { ImagesTab } from '@/components/dashboard/ImagesTab'
import { VolumesTab } from '@/components/dashboard/VolumesTab'
import { NetworksTab } from '@/components/dashboard/NetworksTab'
import { BuildCacheTab } from '@/components/dashboard/BuildCacheTab'
import { useHostSummary } from '@/hooks/useHostSpace'
import { useDashboardMetricsContext } from '@/hooks/DashboardMetricsContext'
import { dashboardApi } from '@/lib/api'
import { queryKeys } from '@/lib/query-keys'
import { cn } from '@/lib/utils'
import { formatBytes } from '@/lib/format'
import type { HostSummary } from '@/types/generated-services'

type SummaryQuery = UseQueryResult<HostSummary>

function useDashboardStats() {
  return useQuery({ queryKey: queryKeys.dashboardStats(), queryFn: dashboardApi.stats })
}

function HostOverviewSkeleton() {
  return (
    <div data-testid="host-overview-loading" aria-busy="true" className="space-y-4">
      <p className="text-sm text-muted-foreground">Loading...</p>
      <Skeleton className="h-3 w-full" />
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        {Array.from({ length: 5 }, (_, i) => (
          <Skeleton key={i} className="h-40" />
        ))}
      </div>
    </div>
  )
}

/** The view's count from the summary; none (not 0) while it is unreadable. */
// A lookup, not a switch: helpCoverage.test.ts reads this file's switch cases
// as the list views it renders. Build cache has no count in the mockup.
const VIEW_COUNT: Partial<Record<HostView, (s: HostSummary) => number>> = {
  containers: (s) => s.containers.count,
  images: (s) => s.images.count,
  volumes: (s) => s.volumes.count,
  networks: (s) => s.networks.count,
}

function viewCount(view: HostView, summary: HostSummary | undefined): number | undefined {
  return summary ? VIEW_COUNT[view]?.(summary) : undefined
}

function HostViewSwitch({ active, summary }: { active: HostView | undefined; summary: HostSummary | undefined }) {
  const linkClass = (isActive: boolean) =>
    cn(
      'inline-flex min-h-[44px] items-center gap-1.5 rounded-md border px-2.5 py-1.5 transition-colors sm:min-h-0',
      isActive
        ? 'border-border bg-card font-medium text-foreground'
        : 'border-transparent text-muted-foreground hover:bg-card hover:border-border hover:text-foreground',
    )
  const items: { view: HostView | undefined; label: string }[] = [{ view: undefined, label: 'Overview' }, ...HOST_VIEW_ITEMS]
  return (
    <nav aria-label="Host views" className="flex flex-wrap items-center gap-1 text-sm" data-testid="host-view-switch">
      {items.map(({ view, label }) => {
        const isActive = view === active
        const count = view ? viewCount(view, summary) : undefined
        return (
          <Link
            key={label}
            to={hostViewHref(view)}
            aria-current={isActive ? 'page' : undefined}
            className={linkClass(isActive)}
          >
            {label}
            {count !== undefined && (
              <span className="font-mono text-[11px] tabular-nums text-muted-foreground">{count}</span>
            )}
          </Link>
        )
      })}
    </nav>
  )
}

function HostOverview({ summaryQuery, onCleanUp }: { summaryQuery: SummaryQuery; onCleanUp: () => void }) {
  const summary = summaryQuery.data
  const { containers, aggregates, isConnected } = useDashboardMetricsContext()
  const { data: stats } = useDashboardStats()

  let body
  if (summaryQuery.isPending) {
    body = <HostOverviewSkeleton />
  } else if (!summary) {
    body = <LoadFailedNotice what="the Docker host summary" onRetry={() => void summaryQuery.refetch()} />
  } else {
    body = (
      <>
        <section aria-label="Docker disk use" className="space-y-2 rounded-lg border bg-card p-4">
          <p className="text-sm">
            <span className="font-medium">Docker disk use: {formatBytes(summary.disk.total)}</span>
            <span className="text-muted-foreground">
              {' '}
              · In use {formatBytes(summary.disk.inUse)} · reclaimable {formatBytes(summary.disk.reclaimable)}
            </span>
          </p>
          <DiskBar inUse={summary.disk.inUse} reclaimable={summary.disk.reclaimable} />
          {!summary.disk.volumesSizeKnown && (
            <p className="text-xs text-muted-foreground">
              Docker could not measure every volume, so the totals count only the volumes it could.
            </p>
          )}
          {!summary.disk.containersSizeKnown && (
            <p className="text-xs text-muted-foreground">
              Docker could not measure every container, so the totals count only the containers it could.
            </p>
          )}
        </section>
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {hostCards(summary).map((c) => (
            <CategoryCard key={c.category} {...c} viewHref={hostViewHref(c.category)} onCleanUp={onCleanUp} />
          ))}
        </div>
      </>
    )
  }

  return (
    <div className="space-y-6">
      <HostLiveRow stats={stats} aggregates={aggregates} isConnected={isConnected} containers={containers} />
      {body}
      <BusiestContainers containers={containers} isConnected={isConnected} total={summary?.containers.count} />
      {/* Outside {body}: the schedule has its own queries, and an unreadable
          host summary is no reason to hide it. Its #automatic-cleanup scroll
          waits until the summary has settled and the content above has its
          height. */}
      <AutoCleanupPanel ready={!summaryQuery.isPending} />
    </div>
  )
}

function ContainersView() {
  const { data: stats } = useDashboardStats()
  const { latestMetrics, ws } = useDashboardMetricsContext()
  return <ContainersOverviewTab stats={stats} latestMetrics={latestMetrics} metricsStatus={ws.status} />
}

function HostListView({ view }: { view: HostView }) {
  let content
  switch (view) {
    case 'containers':
      content = <ContainersView />
      break
    case 'images':
      content = <ImagesTab />
      break
    case 'volumes':
      content = <VolumesTab />
      break
    case 'networks':
      content = <NetworksTab />
      break
    case 'build-cache':
      content = <BuildCacheTab />
      break
  }
  return <ErrorBoundary>{content}</ErrorBoundary>
}

/**
 * The dashboard's Host tab (agent-os-mi4r): the overview, or one of the five
 * Docker host lists at /?tab=host&view=<view>. No view, or one it does not
 * know, shows the overview. The Free-up-space button sits beside the view
 * switch on every view; ?free=1 opens its dialog.
 */
export function HostTab() {
  const [searchParams] = useSearchParams()
  const location = useLocation()
  const navigate = useNavigate()
  const rawView = searchParams.get('view')
  const view = isHostView(rawView) ? rawView : undefined
  const summaryQuery = useHostSummary()
  const summary = summaryQuery.data

  // Closing drops free so a reload does not reopen it. navigate, not
  // setSearchParams: that would drop the #hash, and view must survive too.
  // The state carries a pending #automatic-cleanup scroll request through.
  const freeOpen = searchParams.get('free') === '1'
  const setFreeOpen = (open: boolean) => {
    const next = new URLSearchParams(location.search)
    if (open) next.set('free', '1')
    else next.delete('free')
    navigate({ search: `?${next}`, hash: location.hash }, { replace: true, state: location.state })
  }

  return (
    <div data-testid="host-tab" className="space-y-4">
      {/* Above the view row, not in the overview: the counts and the Free up
          figure on every view come from the same summary, so every view says
          when they are the last good ones. */}
      {summaryQuery.isError && !!summary && (
        <RefreshFailedNotice what="the Docker host summary" onRetry={() => void summaryQuery.refetch()} />
      )}
      <div className="flex flex-wrap items-center justify-between gap-3">
        <HostViewSwitch active={view} summary={summary} />
        {summary && (
          <Button
            data-testid="host-free-space-button"
            disabled={summary.disk.reclaimable <= 0}
            onClick={() => setFreeOpen(true)}
          >
            {summary.disk.reclaimable > 0 ? `Free up ${formatBytes(summary.disk.reclaimable)}` : 'Nothing to free up'}
          </Button>
        )}
      </div>
      {view === undefined ? (
        <HostOverview summaryQuery={summaryQuery} onCleanUp={() => setFreeOpen(true)} />
      ) : (
        <HostListView view={view} />
      )}
      <FreeUpSpaceDialog open={freeOpen} onOpenChange={setFreeOpen} />
    </div>
  )
}
