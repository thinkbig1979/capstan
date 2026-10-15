import { Navigate, useParams, useSearchParams } from 'react-router'
import { Button } from '@/components/ui/button'
import { useQuery } from '@tanstack/react-query'
import { Skeleton } from '@/components/ui/skeleton'
import { ErrorBoundary } from '@/components/ErrorBoundary'
import { LoadFailedNotice } from '@/components/LoadFailedNotice'
import { RefreshFailedNotice } from '@/components/RefreshFailedNotice'
import { DiskBar } from '@/components/host/DiskBar'
import { CategoryCard } from '@/components/host/CategoryCard'
import { HostViewNav } from '@/components/host/HostViewNav'
import { FreeUpSpaceDialog } from '@/components/host/FreeUpSpaceDialog'
import { AutoCleanupPanel } from '@/components/host/AutoCleanupPanel'
import { hostCards, isHostView, type HostView } from '@/components/host/host-views'
import { ContainersOverviewTab } from '@/components/dashboard/ContainersOverviewTab'
import { ImagesTab } from '@/components/dashboard/ImagesTab'
import { VolumesTab } from '@/components/dashboard/VolumesTab'
import { NetworksTab } from '@/components/dashboard/NetworksTab'
import { BuildCacheTab } from '@/components/dashboard/BuildCacheTab'
import { useHostSummary } from '@/hooks/useHostSpace'
import { useDashboardMetricsContext } from '@/hooks/DashboardMetricsContext'
import { dashboardApi } from '@/lib/api'
import { queryKeys } from '@/lib/query-keys'
import { formatBytes } from '@/lib/format'

function HostOverviewSkeleton() {
  return (
    <div data-testid="host-page-loading" aria-busy="true" className="space-y-4">
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

function HostOverview() {
  const summaryQuery = useHostSummary()
  const summary = summaryQuery.data
  // ?free=1 (the dashboard alert's action) opens the dialog; closing it drops
  // the parameter so a reload does not reopen it.
  const [searchParams, setSearchParams] = useSearchParams()
  const freeOpen = searchParams.get('free') === '1'
  const setFreeOpen = (open: boolean) =>
    setSearchParams(
      (prev) => {
        const next = new URLSearchParams(prev)
        if (open) next.set('free', '1')
        else next.delete('free')
        return next
      },
      { replace: true },
    )

  let body
  if (summaryQuery.isPending) {
    body = <HostOverviewSkeleton />
  } else if (!summary) {
    body = (
      <LoadFailedNotice
        what="the Docker host summary"
        onRetry={() => void summaryQuery.refetch()}
      />
    )
  } else {
    body = (
      <>
        {summaryQuery.isError && !!summary && (
          <RefreshFailedNotice what="the Docker host summary" onRetry={() => void summaryQuery.refetch()} />
        )}
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
            <CategoryCard
              key={c.category}
              {...c}
              viewHref={`/host/${c.category}`}
              onCleanUp={() => setFreeOpen(true)}
            />
          ))}
        </div>
      </>
    )
  }

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div className="space-y-1">
          <p className="text-label">Docker host</p>
          <h1 className="text-2xl font-semibold tracking-tight">What's on this server</h1>
          <p className="text-sm text-muted-foreground">
            Everything Docker keeps on disk, what is in use, and what you can safely remove.
          </p>
        </div>
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
      {body}
      {/* Outside {body}: the schedule has its own queries, and an unreadable
          host summary is no reason to hide it. */}
      <AutoCleanupPanel />
      <FreeUpSpaceDialog open={freeOpen} onOpenChange={setFreeOpen} />
    </div>
  )
}

function ContainersView() {
  const { data: stats } = useQuery({
    queryKey: queryKeys.dashboardStats(),
    queryFn: dashboardApi.stats,
  })
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

/** /host (overview) and /host/:view (one of the five Docker host lists). */
export function HostPage() {
  const { view } = useParams()
  if (view !== undefined && !isHostView(view)) {
    return <Navigate to="/host" replace />
  }
  return (
    <div data-testid="host-page" className="space-y-4">
      <HostViewNav />
      {view === undefined ? <HostOverview /> : <HostListView view={view} />}
    </div>
  )
}
