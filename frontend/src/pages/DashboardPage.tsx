import { useState, useEffect, useCallback, useMemo } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { directoriesApi, stacksApi, dashboardApi } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { StackCardSkeleton } from '@/components/LoadingSkeleton'
import { AlertCircle, RefreshCw, Plus, Folder, GitBranch, GitPullRequest, Play, Square, Trash2 } from 'lucide-react'
import { CreateStackDialog } from '@/components/stack/CreateStackDialog'
import { useStackStatusAnimation } from '@/hooks/useStackStatusAnimation'
import { useDashboardMetrics } from '@/hooks/useDashboardMetrics'
import { DashboardMetricsTab } from '@/components/dashboard/DashboardMetricsTab'
import { ContainersOverviewTab } from '@/components/dashboard/ContainersOverviewTab'
import { ImagesTab } from '@/components/dashboard/ImagesTab'
import { VolumesTab } from '@/components/dashboard/VolumesTab'
import { NetworksTab } from '@/components/dashboard/NetworksTab'
import { BuildCacheTab } from '@/components/dashboard/BuildCacheTab'
import { UpdatesTab } from '@/components/dashboard/UpdatesTab'
import { SortFilterBar } from '@/components/dashboard/SortFilterBar'
import { classifyError } from '@/lib/error-handler'
import { toast } from 'sonner'
import { useConfirm } from '@/components/ConfirmDialog'
import { StatusBadge } from '@/components/dashboard/StatusBadge'

type SortOption = 'name' | 'status' | 'created'
type StatusFilter = 'all' | 'running' | 'stopped'

export function DashboardPage() {
  const navigate = useNavigate()
  const [searchParams, setSearchParams] = useSearchParams()
  const activeTab = searchParams.get('tab') || 'overview'
  const setActiveTab = useCallback((tab: string) => {
    setSearchParams(tab === 'overview' ? {} : { tab }, { replace: true })
  }, [setSearchParams])
  const [createDialogOpen, setCreateDialogOpen] = useState(false)
  const { isAnimating } = useStackStatusAnimation()
  const queryClient = useQueryClient()
  const { confirm, ConfirmComponent } = useConfirm()
  const [deletingStackId, setDeletingStackId] = useState<string | null>(null)
  const [sortBy, setSortBy] = useState<SortOption>('name')
  const [statusFilter, setStatusFilter] = useState<StatusFilter>('all')
  const [isRefreshing, setIsRefreshing] = useState(false)

  const { aggregates, latestMetrics, isConnected: metricsConnected } = useDashboardMetrics()

  useEffect(() => {
    const savedSort = localStorage.getItem('dashboard-sort') as SortOption
    const savedFilter = localStorage.getItem('dashboard-filter') as StatusFilter
    if (savedSort) setSortBy(savedSort)
    if (savedFilter) setStatusFilter(savedFilter)
  }, [])

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
    queryKey: ['directories'],
    queryFn: directoriesApi.list,
    retry: 1,
  })

  const {
    data: stacks,
    isLoading: isLoadingStacks,
    error: stacksError,
    refetch: refetchStacks,
  } = useQuery({
    queryKey: ['stacks'],
    queryFn: () => stacksApi.list(),
    retry: 1,
  })

  const {
    data: dashboardStats,
  } = useQuery({
    queryKey: ['dashboard-stats'],
    queryFn: dashboardApi.stats,
    retry: 1,
  })

  const startMutation = useMutation({
    mutationFn: (id: string) => stacksApi.start(id),
    onSuccess: () => {
      toast.success('Stack started successfully')
      queryClient.invalidateQueries({ queryKey: ['stacks'] })
      queryClient.invalidateQueries({ queryKey: ['stack'] })
      queryClient.invalidateQueries({ queryKey: ['dashboard-stats'] })
    },
    onError: () => {
      toast.error('Failed to start stack')
    },
  })

  const stopMutation = useMutation({
    mutationFn: (id: string) => stacksApi.stop(id),
    onSuccess: () => {
      toast.success('Stack stopped successfully')
      queryClient.invalidateQueries({ queryKey: ['stacks'] })
      queryClient.invalidateQueries({ queryKey: ['stack'] })
      queryClient.invalidateQueries({ queryKey: ['dashboard-stats'] })
    },
    onError: () => {
      toast.error('Failed to stop stack')
    },
  })

  const restartMutation = useMutation({
    mutationFn: (id: string) => stacksApi.restart(id),
    onSuccess: () => {
      toast.success('Stack restarted successfully')
      queryClient.invalidateQueries({ queryKey: ['stacks'] })
      queryClient.invalidateQueries({ queryKey: ['stack'] })
      queryClient.invalidateQueries({ queryKey: ['dashboard-stats'] })
    },
    onError: () => {
      toast.error('Failed to restart stack')
    },
  })

  const deleteMutation = useMutation({
    mutationFn: (id: string) => stacksApi.delete(id),
    onSuccess: () => {
      toast.success('Stack deleted successfully')
      setDeletingStackId(null)
      queryClient.invalidateQueries({ queryKey: ['stacks'] })
      queryClient.invalidateQueries({ queryKey: ['dashboard-stats'] })
    },
    onError: () => {
      toast.error('Failed to delete stack')
      setDeletingStackId(null)
    },
  })

  const runningCount = stacks?.filter((s) => s.status === 'running').length || 0
  const stoppedCount = stacks?.filter((s) => s.status === 'stopped').length || 0
  const containerCount = stacks?.reduce((sum, s) => sum + (s.containers?.length || s.containerCount || 0), 0) || 0

  const isLoading = isLoadingDirectories || isLoadingStacks

  const sortItems = <T extends { name?: string; status?: string; createdAt?: string }>(
    items: T[] | undefined,
  ): T[] => {
    if (!items) return []
    const sorted = [...items]
    switch (sortBy) {
      case 'name':
        return sorted.sort((a, b) => (a.name || '').localeCompare(b.name || ''))
      case 'status':
        return sorted.sort((a, b) => (a.status || '').localeCompare(b.status || ''))
      case 'created':
        return sorted.sort((a, b) => {
          const aTime = a.createdAt ? new Date(a.createdAt).getTime() : 0
          const bTime = b.createdAt ? new Date(b.createdAt).getTime() : 0
          return bTime - aTime
        })
      default:
        return sorted
    }
  }

  const filterStacks = (stacksList: typeof stacks) => {
    if (!stacksList) return []
    switch (statusFilter) {
      case 'running':
        return stacksList.filter((s) => s.status === 'running')
      case 'stopped':
        return stacksList.filter((s) => s.status === 'stopped')
      case 'all':
      default:
        return stacksList
    }
  }

  const sortedDirectories = sortItems(directories)
  const sortedStacks = sortItems(stacks)
  const filteredStacks = filterStacks(sortedStacks)

  const groupedStacks = useMemo(() => {
    const groups = new Map<string, typeof filteredStacks>()
    for (const stack of filteredStacks) {
      if (!groups.has(stack.directory)) groups.set(stack.directory, [])
      groups.get(stack.directory)!.push(stack)
    }
    const result: { dirName: string; dirPath: string; stacks: typeof filteredStacks }[] = []
    for (const [dirPath, dirStacks] of groups) {
      const parts = dirPath.split('/')
      const dirName = parts[parts.length - 1] || dirPath
      result.push({ dirName, dirPath, stacks: dirStacks })
    }
    return result.sort((a, b) => a.dirName.localeCompare(b.dirName))
  }, [filteredStacks])
  const hasMultiStackGroups = groupedStacks.some((g) => g.stacks.length > 1)

  const handleRefresh = async () => {
    setIsRefreshing(true)
    try {
      await directoriesApi.scan()
      await Promise.all([refetchDirectories(), refetchStacks()])
      queryClient.invalidateQueries({ queryKey: ['dashboard-stats'] })
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
        <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-4">
          <StackCardSkeleton />
          <StackCardSkeleton />
          <StackCardSkeleton />
          <StackCardSkeleton />
        </div>
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
                  {appError.type === 'auth' && (
                    <Button
                      variant="outline"
                      onClick={() => navigate('/login')}
                      size="sm"
                    >
                      Login
                    </Button>
                  )}
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
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-3xl font-bold tracking-tight">Dashboard</h1>
          <p className="text-muted-foreground">Welcome to Docker Manager</p>
        </div>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="icon" onClick={handleRefresh} disabled={isRefreshing} aria-label="Refresh dashboard">
            <RefreshCw className={`h-4 w-4 ${isRefreshing ? 'animate-spin' : ''}`} />
          </Button>
          <Button onClick={() => setCreateDialogOpen(true)} aria-label="Create new stack">
            <Plus className="mr-2 h-4 w-4" />
            New Stack
          </Button>
        </div>
      </div>

      <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-4">
        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium">Total Stacks</CardTitle>
            <Folder className="h-4 w-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold">{stacks?.length || 0}</div>
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium">Running</CardTitle>
            <div className="h-2 w-2 rounded-full bg-green-500" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold">{runningCount}</div>
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium">Stopped</CardTitle>
            <div className="h-2 w-2 rounded-full bg-red-500" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold">{stoppedCount}</div>
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium">Containers</CardTitle>
            <div className="h-2 w-2 rounded-full bg-blue-500" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold">{containerCount}</div>
          </CardContent>
        </Card>
      </div>

      <Tabs value={activeTab} onValueChange={setActiveTab}>
        <TabsList className="grid w-full sm:w-auto grid-cols-9">
          <TabsTrigger value="overview">Metrics</TabsTrigger>
          <TabsTrigger value="stacks">Stacks</TabsTrigger>
          <TabsTrigger value="directories">Dirs</TabsTrigger>
          <TabsTrigger value="containers">Containers</TabsTrigger>
          <TabsTrigger value="updates">Updates</TabsTrigger>
          <TabsTrigger value="images">Images</TabsTrigger>
          <TabsTrigger value="volumes">Volumes</TabsTrigger>
          <TabsTrigger value="networks">Networks</TabsTrigger>
          <TabsTrigger value="build-cache">Build Cache</TabsTrigger>
        </TabsList>

        <TabsContent value="overview" className="mt-4">
          <DashboardMetricsTab
            stats={dashboardStats}
            aggregates={aggregates}
            isConnected={metricsConnected}
          />
        </TabsContent>

        <TabsContent value="stacks" className="mt-4">
          <SortFilterBar
            sortOptions={[
              { key: 'name', label: 'Name' },
              { key: 'status', label: 'Status' },
              { key: 'created', label: 'Created' },
            ]}
            sortValue={sortBy}
            onSortChange={(key) => setSortBy(key as SortOption)}
            filterOptions={[
              { key: 'all', label: 'All' },
              { key: 'running', label: 'Running' },
              { key: 'stopped', label: 'Stopped' },
            ]}
            filterValue={statusFilter}
            onFilterChange={(key) => setStatusFilter(key as StatusFilter)}
            countDisplay={
              <>
                <span className="font-medium">{filteredStacks.length}</span> of <span className="font-medium">{stacks?.length || 0}</span>
              </>
            }
          />
          {filteredStacks.length > 0 ? (
            <div className="rounded-md border">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Name</TableHead>
                    <TableHead>Directory</TableHead>
                    <TableHead>Status</TableHead>
                    <TableHead>Containers</TableHead>
                    <TableHead>Actions</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {hasMultiStackGroups ? (
                    groupedStacks.flatMap((group) => [
                      <TableRow key={`group-${group.dirPath}`} className="bg-muted/50 hover:bg-muted/50">
                        <TableCell colSpan={5} className="py-1.5 px-4">
                          <span className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
                            {group.dirName}
                          </span>
                          {group.stacks[0]?.isGitRepo && (
                            <GitBranch className="inline h-3 w-3 ml-1.5 text-muted-foreground align-middle" />
                          )}
                          <span className="text-xs text-muted-foreground ml-2">
                            {group.stacks.length} stack{group.stacks.length !== 1 ? 's' : ''}
                          </span>
                        </TableCell>
                      </TableRow>,
                      ...group.stacks.map((stack) => (
                        <TableRow
                          key={stack.id}
                          className="cursor-pointer"
                          onClick={() => navigate(`/stacks/${stack.id}`)}
                        >
                           <TableCell className="font-medium pl-6">{stack.projectName}</TableCell>
                           <TableCell className="text-sm text-muted-foreground">
                             <span className="inline-flex items-center gap-1.5">
                               {stack.composeFile}
                             </span>
                           </TableCell>
                           <TableCell>
                             <StatusBadge status={stack.status as 'running' | 'stopped' | 'partial' | 'unknown'} pulse={isAnimating(stack.id)} />
                           </TableCell>
                           <TableCell>
                             {stack.containerCount !== undefined ? (
                               <Badge variant="outline">{stack.containerCount}</Badge>
                             ) : (
                               <span className="text-sm text-muted-foreground">-</span>
                             )}
                           </TableCell>
                           <TableCell>
                             <div className="flex items-center gap-1" onClick={(e) => e.stopPropagation()} role="group" aria-label="Stack actions">
                              {stack.status !== 'running' && (
                                <Button variant="ghost" size="icon" className="h-7 w-7" onClick={(e) => handleStart(stack.id, e)} disabled={startMutation.isPending} title="Start">
                                  <Play className="h-3.5 w-3.5" />
                                </Button>
                              )}
                              {stack.status === 'running' && (
                                <Button variant="ghost" size="icon" className="h-7 w-7" onClick={(e) => handleStop(stack.id, e)} disabled={stopMutation.isPending} title="Stop">
                                  <Square className="h-3.5 w-3.5" />
                                </Button>
                              )}
                              {stack.status === 'running' && (
                                <Button variant="ghost" size="icon" className="h-7 w-7" onClick={(e) => handleRestart(stack.id, e)} disabled={restartMutation.isPending} title="Restart">
                                  <RefreshCw className={`h-3.5 w-3.5 ${restartMutation.isPending ? 'animate-spin' : ''}`} />
                                </Button>
                              )}
                              <Button variant="ghost" size="icon" className="h-7 w-7 text-destructive hover:text-destructive" onClick={(e) => handleDelete(stack.id, stack.projectName, e)} disabled={deletingStackId === stack.id || deleteMutation.isPending} title="Delete">
                                <Trash2 className="h-3.5 w-3.5" />
                              </Button>
                            </div>
                          </TableCell>
                        </TableRow>
                      )),
                    ])
                  ) : (
                    filteredStacks.map((stack) => (
                      <TableRow
                        key={stack.id}
                        className="cursor-pointer"
                        onClick={() => navigate(`/stacks/${stack.id}`)}
                      >
                        <TableCell className="font-medium">{stack.projectName}</TableCell>
                        <TableCell className="text-sm text-muted-foreground">
                           <span
                            className="inline-flex items-center gap-1.5 cursor-pointer hover:text-foreground hover:underline"
                            role="button"
                            tabIndex={0}
                            onClick={(e) => {
                              e.stopPropagation()
                              setActiveTab('directories')
                            }}
                            onKeyDown={(e) => {
                              if (e.key === 'Enter' || e.key === ' ') {
                                e.preventDefault()
                                e.stopPropagation()
                                setActiveTab('directories')
                              }
                            }}
                          >
                            {stack.directory}
                            {stack.isGitRepo && (
                              <GitBranch className="h-3 w-3 text-muted-foreground" />
                            )}
                          </span>
                        </TableCell>
                        <TableCell>
                          <StatusBadge status={stack.status as 'running' | 'stopped' | 'partial' | 'unknown'} pulse={isAnimating(stack.id)} />
                        </TableCell>
                        <TableCell>
                          {stack.containerCount !== undefined ? (
                            <Badge variant="outline">{stack.containerCount}</Badge>
                          ) : (
                            <span className="text-sm text-muted-foreground">-</span>
                          )}
                        </TableCell>
                        <TableCell>
                           <div className="flex items-center gap-1" onClick={(e) => e.stopPropagation()} role="group" aria-label="Stack actions">
                             {stack.status !== 'running' && (
                               <Button variant="ghost" size="icon" className="h-7 w-7" onClick={(e) => handleStart(stack.id, e)} disabled={startMutation.isPending} title="Start">
                                 <Play className="h-3.5 w-3.5" />
                               </Button>
                             )}
                             {stack.status === 'running' && (
                               <Button variant="ghost" size="icon" className="h-7 w-7" onClick={(e) => handleStop(stack.id, e)} disabled={stopMutation.isPending} title="Stop">
                                 <Square className="h-3.5 w-3.5" />
                               </Button>
                             )}
                             {stack.status === 'running' && (
                               <Button variant="ghost" size="icon" className="h-7 w-7" onClick={(e) => handleRestart(stack.id, e)} disabled={restartMutation.isPending} title="Restart">
                                 <RefreshCw className={`h-3.5 w-3.5 ${restartMutation.isPending ? 'animate-spin' : ''}`} />
                               </Button>
                             )}
                             <Button variant="ghost" size="icon" className="h-7 w-7 text-destructive hover:text-destructive" onClick={(e) => handleDelete(stack.id, stack.projectName, e)} disabled={deletingStackId === stack.id || deleteMutation.isPending} title="Delete">
                               <Trash2 className="h-3.5 w-3.5" />
                             </Button>
                            </div>
                         </TableCell>
                       </TableRow>
                     ))
                   )}
                 </TableBody>
               </Table>
            </div>
          ) : (
            <Card>
              <CardContent className="pt-6">
                <div className="text-center space-y-4">
                  <p className="text-muted-foreground">
                    {statusFilter === 'all' ? 'No stacks configured yet' : `No ${statusFilter} stacks found`}
                  </p>
                  {statusFilter === 'all' && (
                    <Button onClick={() => setCreateDialogOpen(true)}>
                      <Plus className="mr-2 h-4 w-4" />
                      Create Your First Stack
                    </Button>
                  )}
                </div>
              </CardContent>
            </Card>
          )}
        </TabsContent>

        <TabsContent value="directories" className="mt-4">
          <SortFilterBar
            sortOptions={[
              { key: 'name', label: 'Name' },
              { key: 'created', label: 'Created' },
            ]}
            sortValue={sortBy === 'status' ? 'name' : sortBy}
            onSortChange={(key) => setSortBy(key as SortOption)}
            countDisplay={`${sortedDirectories.length} directories`}
          />
          {sortedDirectories.length > 0 ? (
            <div className="rounded-md border">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Name</TableHead>
                    <TableHead>Path</TableHead>
                    <TableHead>Stacks</TableHead>
                    <TableHead>Git Branch</TableHead>
                    <TableHead>Behind</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {sortedDirectories.map((dir) => (
                    <TableRow key={dir.path}>
                      <TableCell className="font-medium">
                        <div className="flex items-center gap-2">
                          <Folder className="h-4 w-4 text-muted-foreground" />
                          {dir.name}
                        </div>
                      </TableCell>
                      <TableCell className="text-sm text-muted-foreground">{dir.path}</TableCell>
                      <TableCell>
                        <Badge
                          variant="secondary"
                          className="cursor-pointer hover:bg-secondary/80"
                          onClick={() => {
                            const firstStack = stacks?.find(s => s.directory === dir.path)
                            if (firstStack) {
                              navigate(`/stacks/${firstStack.id}`)
                            }
                          }}
                        >
                          {dir.stackCount}
                        </Badge>
                      </TableCell>
                      <TableCell>
                        {dir.isGitRepo ? (
                          <Badge variant="outline" className="flex items-center gap-1 w-fit">
                            <GitBranch className="h-3 w-3" />
                            {dir.gitBranch || 'main'}
                          </Badge>
                        ) : (
                          <span className="text-sm text-muted-foreground">-</span>
                        )}
                      </TableCell>
                      <TableCell>
                        {dir.isGitRepo && ((dir.gitBehind ?? 0) > 0) ? (
                          <Badge variant="secondary" className="flex items-center gap-1 text-yellow-600">
                            <GitPullRequest className="h-3 w-3" />
                            {dir.gitBehind}
                          </Badge>
                        ) : (
                          <span className="text-sm text-muted-foreground">-</span>
                        )}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          ) : (
            <Card>
              <CardContent className="pt-6">
                <p className="text-center text-muted-foreground">No directories found</p>
              </CardContent>
            </Card>
          )}
        </TabsContent>

        <TabsContent value="containers" className="mt-4">
          <ContainersOverviewTab stats={dashboardStats} latestMetrics={latestMetrics} />
        </TabsContent>

        <TabsContent value="updates" className="mt-4">
          <UpdatesTab />
        </TabsContent>

        <TabsContent value="images" className="mt-4">
          <ImagesTab />
        </TabsContent>

        <TabsContent value="volumes" className="mt-4">
          <VolumesTab />
        </TabsContent>

        <TabsContent value="networks" className="mt-4">
          <NetworksTab />
        </TabsContent>

        <TabsContent value="build-cache" className="mt-4">
          <BuildCacheTab />
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

      <CreateStackDialog open={createDialogOpen} onOpenChange={setCreateDialogOpen} />
      <ConfirmComponent />
    </div>
  )
}
