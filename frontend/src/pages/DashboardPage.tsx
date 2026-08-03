import { useState, useEffect, useCallback, useMemo, Suspense, lazy } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useSearchParams } from 'react-router'
import { directoriesApi, stacksApi, dashboardApi, settingsApi } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Tabs, TabsContent } from '@/components/ui/tabs'
import { ResponsiveTabsList } from '@/components/ui/responsive-tabs-list'
import { StackCardSkeleton, MetricsSkeleton, DialogLoadingFallback } from '@/components/LoadingSkeleton'
import { AlertCircle, RefreshCw, Plus } from 'lucide-react'
import { useStackStatusAnimation } from '@/hooks/useStackStatusAnimation'
import { useUpdateScanStore } from '@/stores/updateScanStore'
import { useDashboardMetrics } from '@/hooks/useDashboardMetrics'
import { DashboardMetricsTab } from '@/components/dashboard/DashboardMetricsTab'
import { ContainersOverviewTab } from '@/components/dashboard/ContainersOverviewTab'
import { ImagesTab } from '@/components/dashboard/ImagesTab'
import { VolumesTab } from '@/components/dashboard/VolumesTab'
import { NetworksTab } from '@/components/dashboard/NetworksTab'
import { BuildCacheTab } from '@/components/dashboard/BuildCacheTab'
import { UpdatesTab } from '@/components/dashboard/UpdatesTab'
import { DashboardHeader } from '@/components/dashboard/DashboardHeader'
import { StacksTab } from '@/components/dashboard/StacksTab'
import { DirectoriesTab } from '@/components/dashboard/DirectoriesTab'
import { classifyError } from '@/lib/error-handler'
import { useStackActions } from '@/hooks/useStackActions'
import { toast } from 'sonner'
import { useConfirm } from '@/hooks/useConfirm'
import { ErrorBoundary } from '@/components/ErrorBoundary'
import type { ConfiguredDir } from '@/types'
import { queryKeys } from '@/lib/query-keys'

// Lazy: CreateStackDialog pulls in codemirror (its inline compose editor) but was
// previously mounted unconditionally on every Dashboard visit, keeping codemirror
// in this route's eager bundle even for users who never open "New Stack".
const CreateStackDialog = lazy(() =>
  import('@/components/stack/CreateStackDialog').then((m) => ({ default: m.CreateStackDialog })),
)

type SortOption = 'name' | 'status'
type StatusFilter = 'all' | 'running' | 'stopped' | 'error'

function sortDirectories(items: ConfiguredDir[] | undefined) {
  if (!items) return []
  const sorted = [...items]
  return sorted.sort((a, b) => a.name.localeCompare(b.name))
}

export function DashboardPage() {
  const [searchParams, setSearchParams] = useSearchParams()
  const activeTab = searchParams.get('tab') || 'overview'
  const setActiveTab = useCallback((tab: string) => {
    setSearchParams(tab === 'overview' ? {} : { tab }, { replace: true })
  }, [setSearchParams])
  const [createDialogOpen, setCreateDialogOpen] = useState(false)
  // Once opened, keep CreateStackDialog mounted for the rest of the session so
  // its own close animation (Radix data-[state=closed]:animate-out) always has
  // something to animate — unmounting it the instant createDialogOpen flips
  // false would cut that animation off. Guarded setState-during-render (React's
  // documented adjust-state-during-render pattern, not a ref mutation) so the
  // lazy import starts in the same pass that sets createDialogOpen.
  const [hasOpenedCreateDialog, setHasOpenedCreateDialog] = useState(false)
  if (createDialogOpen && !hasOpenedCreateDialog) setHasOpenedCreateDialog(true)
  const { isAnimating } = useStackStatusAnimation()
  const isScanningUpdates = useUpdateScanStore((s) => s.isScanning)
  const queryClient = useQueryClient()
  const { confirm, ConfirmComponent } = useConfirm()
  const [deletingStackId, setDeletingStackId] = useState<string | null>(null)
  // Read the persisted sort/filter in the initialiser rather than in a mount
  // effect. An effect would render once with the default and again with the
  // stored value, which is both a visible flash of the wrong sort order and
  // what react-hooks/set-state-in-effect flags.
  const [sortBy, setSortBy] = useState<SortOption>(
    () => (localStorage.getItem('dashboard-sort') as SortOption) || 'name',
  )
  const [statusFilter, setStatusFilter] = useState<StatusFilter>(
    () => (localStorage.getItem('dashboard-filter') as StatusFilter) || 'all',
  )
  const [isRefreshing, setIsRefreshing] = useState(false)

  const { containers: metricsContainers, aggregates, latestMetrics, isConnected: metricsConnected, ws: metricsWs } = useDashboardMetrics()

  useEffect(() => {
    localStorage.setItem('dashboard-sort', sortBy)
    localStorage.setItem('dashboard-filter', statusFilter)
  }, [sortBy, statusFilter])

  const {
    data: directories,
    isLoading: isLoadingDirectories,
    error: directoriesError,
    refetch: refetchDirectories,
  } = useQuery({
    queryKey: queryKeys.directories(),
    queryFn: directoriesApi.list,
    retry: 1,
  })

  const {
    data: stacks,
    isLoading: isLoadingStacks,
    error: stacksError,
    refetch: refetchStacks,
  } = useQuery({
    queryKey: queryKeys.stacks(),
    queryFn: () => stacksApi.list(),
    retry: 1,
  })

  const {
    data: dashboardStats,
  } = useQuery({
    queryKey: queryKeys.dashboardStats(),
    queryFn: dashboardApi.stats,
    retry: 1,
  })

  const { data: config } = useQuery({
    queryKey: queryKeys.config(),
    queryFn: settingsApi.getConfig,
    staleTime: Infinity,
  })

  const configuredDirs = useMemo(
    () => config?.stacksDirectories || [],
    [config],
  )

  const { start: startMutation, stop: stopMutation, restart: restartMutation, delete: deleteMutation } = useStackActions({
    onSuccess: (action) => {
      if (action === 'delete') setDeletingStackId(null)
    },
    onError: (action) => {
      if (action === 'delete') setDeletingStackId(null)
    },
    // Lets a 428 STACK_DELETE_COLLATERAL response re-confirm with the user
    // (via the same dialog as the initial delete confirmation) instead of
    // surfacing as a generic failure. See deleteStackWithCollateralConfirm.
    confirmCollateral: confirm,
  })

  const runningCount = stacks?.filter((s) => s.status === 'running').length || 0
  const stoppedCount = stacks?.filter((s) => s.status === 'stopped').length || 0
  const containerCount = stacks?.reduce((sum, s) => sum + (s.containers?.length || 0), 0) || 0

  const isLoading = isLoadingDirectories || isLoadingStacks

  const sortStacks = (items: typeof stacks) => {
    if (!items) return []
    const sorted = [...items]
    switch (sortBy) {
      case 'name':
        return sorted.sort((a, b) => a.projectName.localeCompare(b.projectName))
      case 'status':
        return sorted.sort((a, b) => a.status.localeCompare(b.status))
      default:
        return sorted
    }
  }

  const filterStacks = (stacksList: typeof stacks) => {
    if (!stacksList) return []
    // Exact status match, mirroring the sidebar filter so the two surfaces agree.
    switch (statusFilter) {
      case 'running':
        return stacksList.filter((s) => s.status === 'running')
      case 'stopped':
        return stacksList.filter((s) => s.status === 'stopped')
      case 'error':
        return stacksList.filter((s) => s.status === 'error')
      case 'all':
      default:
        return stacksList
    }
  }

  const sortedDirectories = sortDirectories(directories)
  const sortedStacks = sortStacks(stacks)
  const filteredStacks = filterStacks(sortedStacks)

  const handleRefresh = async () => {
    setIsRefreshing(true)
    try {
      await directoriesApi.scan()
      await Promise.all([refetchDirectories(), refetchStacks()])
      queryClient.invalidateQueries({ queryKey: queryKeys.dashboardStats() })
      toast.success('Dashboard refreshed')
    } catch (error) {
      const appError = classifyError(error)
      toast.error(`Failed to refresh: ${appError.message}`)
    } finally {
      setIsRefreshing(false)
    }
  }

  const handleRetry = () => {
    refetchDirectories()
    refetchStacks()
  }

  const handleStart = (stackId: string, e: React.MouseEvent) => {
    e.stopPropagation()
    startMutation.mutate(stackId)
  }

  const handleStop = (stackId: string, e: React.MouseEvent) => {
    e.stopPropagation()
    stopMutation.mutate(stackId)
  }

  const handleRestart = (stackId: string, e: React.MouseEvent) => {
    e.stopPropagation()
    restartMutation.mutate(stackId)
  }

  const handleDelete = async (stackId: string, stackName: string, e: React.MouseEvent) => {
    e.stopPropagation()
    const confirmed = await confirm(
      `Delete Stack "${stackName}"?`,
      'This action cannot be undone. The stack and all its data will be permanently removed.',
      { confirmText: 'Delete', isDangerous: true },
    )
    if (confirmed) {
      setDeletingStackId(stackId)
      deleteMutation.mutate(stackId)
    }
  }

  if (isLoading) {
    return (
      <div className="space-y-6">
        <div className="flex items-center justify-between">
          <div>
            <h1 className="text-3xl font-bold tracking-tight">Dashboard</h1>
            <p className="text-muted-foreground">Loading...</p>
          </div>
        </div>
        <div className="h-10 w-full bg-muted animate-pulse rounded-md" />
        <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-4">
          <StackCardSkeleton />
          <StackCardSkeleton />
          <StackCardSkeleton />
          <StackCardSkeleton />
        </div>
        <MetricsSkeleton />
      </div>
    )
  }

  const error = directoriesError || stacksError
  if (error) {
    const appError = classifyError(error)

    return (
      <div className="space-y-6">
        <div className="flex items-center justify-between">
          <div>
            <h1 className="text-3xl font-bold tracking-tight">Dashboard</h1>
            <p className="text-muted-foreground">Error loading data</p>
          </div>
        </div>
        <Card className="border-destructive">
          <CardContent className="pt-6">
            <div className="flex items-start gap-4">
              <AlertCircle className="h-5 w-5 text-destructive mt-0.5" />
              <div className="flex-1 space-y-2">
                <h3 className="font-semibold">Failed to load dashboard</h3>
                <p className="text-sm text-muted-foreground">{appError.message}</p>
                <div className="flex gap-2">
                  <Button onClick={handleRetry} disabled={!appError.retryable} size="sm">
                    <RefreshCw className="mr-2 h-4 w-4" />
                    Retry
                  </Button>
                  <Button variant="outline" size="sm" onClick={handleRefresh} disabled={isRefreshing}>
                    <RefreshCw className={`mr-2 h-4 w-4 ${isRefreshing ? 'animate-spin' : ''}`} />
                    Refresh
                  </Button>
                </div>
              </div>
            </div>
          </CardContent>
        </Card>
      </div>
    )
  }

  return (
    <div className="space-y-6">
      <DashboardHeader
        onRefresh={handleRefresh}
        onCreateStack={() => setCreateDialogOpen(true)}
        isRefreshing={isRefreshing}
        subtitle={stacks
          ? `${stacks.length} ${stacks.length === 1 ? 'stack' : 'stacks'} · ${runningCount} running · ${containerCount} ${containerCount === 1 ? 'container' : 'containers'}`
          : undefined}
      />

      <Tabs value={activeTab} onValueChange={setActiveTab}>
        <ResponsiveTabsList
          value={activeTab}
          onValueChange={setActiveTab}
          variant="line"
          tabs={[
            { value: 'overview', label: 'Metrics' },
            { value: 'stacks', label: 'Stacks' },
            { value: 'containers', label: 'Containers' },
            { value: 'directories', label: 'Dirs' },
            {
              value: 'updates',
              label: isScanningUpdates ? (
                <span className="flex items-center gap-1.5">
                  Updates
                  <RefreshCw className="h-3 w-3 animate-spin" />
                </span>
              ) : (
                'Updates'
              ),
            },
            { value: 'images', label: 'Images' },
            { value: 'volumes', label: 'Volumes' },
            { value: 'networks', label: 'Networks' },
            { value: 'build-cache', label: 'Build Cache' },
          ]}
        />

        <TabsContent value="overview" className="mt-4">
          <ErrorBoundary>
            <DashboardMetricsTab
              stats={dashboardStats}
              aggregates={aggregates}
              isConnected={metricsConnected}
              totalStacks={stacks?.length || 0}
              runningStacks={runningCount}
              stoppedStacks={stoppedCount}
              totalContainers={containerCount}
              runningContainers={dashboardStats?.runningContainers || 0}
              directoryCount={directories?.length || 0}
              containers={metricsContainers}
            />
          </ErrorBoundary>
        </TabsContent>

        <TabsContent value="stacks" className="mt-4">
          <ErrorBoundary>
            <StacksTab
              stacks={stacks || []}
              filteredStacks={filteredStacks}
              configuredDirs={configuredDirs}
              sortBy={sortBy}
              statusFilter={statusFilter}
              onSortChange={setSortBy}
              onFilterChange={setStatusFilter}
              onNavigateToDirectories={() => setActiveTab('directories')}
              onCreateStack={() => setCreateDialogOpen(true)}
              onStart={handleStart}
              onStop={handleStop}
              onRestart={handleRestart}
              onDelete={handleDelete}
              deletingStackId={deletingStackId}
              startPending={startMutation.isPending}
              stopPending={stopMutation.isPending}
              restartPending={restartMutation.isPending}
              deletePending={deleteMutation.isPending}
              isAnimating={isAnimating}
            />
          </ErrorBoundary>
        </TabsContent>

        <TabsContent value="containers" className="mt-4">
          <ErrorBoundary>
            <ContainersOverviewTab stats={dashboardStats} latestMetrics={latestMetrics} metricsStatus={metricsWs.status} />
          </ErrorBoundary>
        </TabsContent>

        <TabsContent value="directories" className="mt-4">
          <ErrorBoundary>
            <DirectoriesTab
              directories={sortedDirectories}
              stacks={stacks || []}
              configuredDirs={configuredDirs}
            />
          </ErrorBoundary>
        </TabsContent>

        <TabsContent value="updates" className="mt-4">
          <ErrorBoundary>
            <UpdatesTab />
          </ErrorBoundary>
        </TabsContent>

        <TabsContent value="images" className="mt-4">
          <ErrorBoundary>
            <ImagesTab />
          </ErrorBoundary>
        </TabsContent>

        <TabsContent value="volumes" className="mt-4">
          <ErrorBoundary>
            <VolumesTab />
          </ErrorBoundary>
        </TabsContent>

        <TabsContent value="networks" className="mt-4">
          <ErrorBoundary>
            <NetworksTab />
          </ErrorBoundary>
        </TabsContent>

        <TabsContent value="build-cache" className="mt-4">
          <ErrorBoundary>
            <BuildCacheTab />
          </ErrorBoundary>
        </TabsContent>
      </Tabs>

      {sortedDirectories.length === 0 && filteredStacks.length === 0 && statusFilter === 'all' && (
        <Card>
          <CardHeader>
            <CardTitle>Quick Start</CardTitle>
            <CardDescription>Get started by creating your first stack</CardDescription>
          </CardHeader>
          <CardContent>
            <p className="text-sm text-muted-foreground mb-4">
              No stacks configured yet. Click "New Stack" to create your first Docker Compose stack.
            </p>
            <Button onClick={() => setCreateDialogOpen(true)}>
              <Plus className="mr-2 h-4 w-4" />
              Create Your First Stack
            </Button>
          </CardContent>
        </Card>
      )}

      {hasOpenedCreateDialog && (
        <Suspense fallback={<DialogLoadingFallback testId="create-stack-dialog-loading" />}>
          <CreateStackDialog open={createDialogOpen} onOpenChange={setCreateDialogOpen} />
        </Suspense>
      )}
      <ConfirmComponent />
    </div>
  )
}
