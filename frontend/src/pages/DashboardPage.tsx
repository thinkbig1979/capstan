import { useState, useEffect } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { directoriesApi, stacksApi } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { StackCardSkeleton, MetricsSkeleton } from '@/components/LoadingSkeleton'
import { AlertCircle, RefreshCw, Plus, Folder, GitBranch, GitPullRequest, Play, Square, Trash2, Layers, X, ArrowUpDown, ChevronsUpDown } from 'lucide-react'
import { CreateStackDialog } from '@/components/stack/CreateStackDialog'
import { useStackStatusAnimation } from '@/hooks/useStackStatusAnimation'
import { classifyError } from '@/lib/error-handler'
import { toast } from 'sonner'
import { useConfirm } from '@/components/ConfirmDialog'
import { StatusBadge } from '@/components/dashboard/StatusBadge'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'

type SortOption = 'name-asc' | 'name-desc' | 'status' | 'created-newest' | 'created-oldest'
type StatusFilter = 'all' | 'running' | 'stopped'

export function DashboardPage() {
  const [createDialogOpen, setCreateDialogOpen] = useState(false)
  const { isAnimating } = useStackStatusAnimation()
  const queryClient = useQueryClient()
  const { confirm, ConfirmComponent } = useConfirm()
  const [deletingStackId, setDeletingStackId] = useState<string | null>(null)
  const [sortBy, setSortBy] = useState<SortOption>('name-asc')
  const [statusFilter, setStatusFilter] = useState<StatusFilter>('all')
  const [isRefreshing, setIsRefreshing] = useState(false)
  const [directoriesExpanded, setDirectoriesExpanded] = useState(true)
  const [stacksExpanded, setStacksExpanded] = useState(true)

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
    refetch: refetchDirectories
  } = useQuery({
    queryKey: ['directories'],
    queryFn: directoriesApi.list,
    retry: 1,
  })

  const {
    data: stacks,
    isLoading: isLoadingStacks,
    error: stacksError,
    refetch: refetchStacks
  } = useQuery({
    queryKey: ['stacks'],
    queryFn: () => stacksApi.list(),
    retry: 1,
  })

  const startMutation = useMutation({
    mutationFn: (id: string) => stacksApi.start(id),
    onSuccess: () => {
      toast.success('Stack started successfully')
      queryClient.invalidateQueries({ queryKey: ['stacks'] })
      queryClient.invalidateQueries({ queryKey: ['stack'] })
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
    },
    onError: () => {
      toast.error('Failed to delete stack')
      setDeletingStackId(null)
    },
  })

  const runningCount = stacks?.filter((s) => s.status === 'running').length || 0
  const stoppedCount = stacks?.filter((s) => s.status === 'stopped').length || 0
  const containerCount = stacks?.reduce((sum, s) => sum + (s.containerCount || 0), 0) || 0

  const isLoading = isLoadingDirectories || isLoadingStacks

  const sortItems = <T extends { name?: string; status?: string; createdAt?: string }>(
    items: T[] | undefined
  ): T[] => {
    if (!items) return []
    const sorted = [...items]
    switch (sortBy) {
      case 'name-asc':
        return sorted.sort((a, b) => (a.name || '').localeCompare(b.name || ''))
      case 'name-desc':
        return sorted.sort((a, b) => (b.name || '').localeCompare(a.name || ''))
      case 'status':
        return sorted.sort((a, b) => (a.status || '').localeCompare(b.status || ''))
      case 'created-newest':
        return sorted.sort((a, b) => {
          const aTime = a.createdAt ? new Date(a.createdAt).getTime() : 0
          const bTime = b.createdAt ? new Date(b.createdAt).getTime() : 0
          return bTime - aTime
        })
      case 'created-oldest':
        return sorted.sort((a, b) => {
          const aTime = a.createdAt ? new Date(a.createdAt).getTime() : 0
          const bTime = b.createdAt ? new Date(b.createdAt).getTime() : 0
          return aTime - bTime
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

  const handleClearFilters = () => {
    setSortBy('name-asc')
    setStatusFilter('all')
  }

  const handleRefresh = async () => {
    setIsRefreshing(true)
    try {
      await Promise.all([refetchDirectories(), refetchStacks()])
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
      { confirmText: 'Delete', isDangerous: true }
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
        <MetricsSkeleton />
        <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3">
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
                      onClick={() => (window.location.href = '/login')}
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

      <div className="flex flex-col sm:flex-row gap-4 items-start sm:items-center justify-between">
        <div className="flex flex-wrap gap-2 items-center">
            <div className="flex items-center gap-2">
              <ArrowUpDown className="h-4 w-4 text-muted-foreground" />
              <span className="text-sm text-muted-foreground">Sort:</span>
            </div>
            <Select value={sortBy} onValueChange={(value) => setSortBy(value as SortOption)} aria-label="Sort stacks by">
              <SelectTrigger className="w-[180px] h-9">
                <SelectValue />
              </SelectTrigger>
            <SelectContent>
              <SelectItem value="name-asc">Name A-Z</SelectItem>
              <SelectItem value="name-desc">Name Z-A</SelectItem>
              <SelectItem value="status">Status</SelectItem>
              <SelectItem value="created-newest">Created (newest)</SelectItem>
              <SelectItem value="created-oldest">Created (oldest)</SelectItem>
            </SelectContent>
          </Select>
          <div className="flex items-center gap-2">
            <ChevronsUpDown className="h-4 w-4 text-muted-foreground" />
            <span className="text-sm text-muted-foreground">Filter:</span>
          </div>
          <Select value={statusFilter} onValueChange={(value) => setStatusFilter(value as StatusFilter)} aria-label="Filter stacks by status">
            <SelectTrigger className="w-[140px] h-9">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">All</SelectItem>
              <SelectItem value="running">Running</SelectItem>
              <SelectItem value="stopped">Stopped</SelectItem>
            </SelectContent>
          </Select>
          {(sortBy !== 'name-asc' || statusFilter !== 'all') && (
            <Button variant="ghost" size="sm" onClick={handleClearFilters} className="h-9">
              <X className="mr-1 h-3 w-3" />
              Clear filters
            </Button>
          )}
        </div>
        <p className="text-sm text-muted-foreground">
          Showing <span className="font-medium">{filteredStacks.length}</span> of <span className="font-medium">{stacks?.length || 0}</span> stacks
        </p>
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

      <section className="space-y-4">
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-2">
            <Folder className="h-5 w-5 text-muted-foreground" />
            <h2 className="text-xl font-semibold">Directories</h2>
            <Badge variant="secondary">{sortedDirectories.length}</Badge>
          </div>
          <div className="flex items-center gap-2">
            <Button
              variant="outline"
              size="sm"
              className="md:hidden"
              onClick={() => setDirectoriesExpanded(!directoriesExpanded)}
            >
              {directoriesExpanded ? 'Collapse' : 'Expand'}
            </Button>
          </div>
        </div>

        {directoriesExpanded && (
          <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3">
            {sortedDirectories.length > 0 ? (
              sortedDirectories.map((dir) => (
                <Card key={dir.path} className="hover:shadow-md transition-shadow cursor-pointer">
                  <CardHeader>
                    <CardTitle className="flex items-center gap-2">
                      <Folder className="h-5 w-5" />
                      {dir.name}
                    </CardTitle>
                    <CardDescription>{dir.path}</CardDescription>
                  </CardHeader>
                  <CardContent>
                    <div className="flex items-center gap-2 mb-2">
                      <Badge variant="secondary">{dir.stackCount} stacks</Badge>
                      {dir.isGitRepo && (
                        <>
                          <Badge variant="outline" className="flex items-center gap-1">
                            <GitBranch className="h-3 w-3" />
                            {dir.gitBranch || 'main'}
                          </Badge>
                          {((dir.gitBehind ?? 0) > 0) && (
                            <Badge variant="secondary" className="flex items-center gap-1 text-yellow-600">
                              <GitPullRequest className="h-3 w-3" />
                              {dir.gitBehind}
                            </Badge>
                          )}
                        </>
                      )}
                    </div>
                  </CardContent>
                </Card>
              ))
            ) : (
              <Card className="col-span-full">
                <CardContent className="pt-6">
                  <p className="text-center text-muted-foreground">No directories found</p>
                </CardContent>
              </Card>
            )}
          </div>
        )}
      </section>

      <section className="space-y-4">
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-2">
            <Layers className="h-5 w-5 text-muted-foreground" />
            <h2 className="text-xl font-semibold">Stacks</h2>
            <Badge variant="secondary">{filteredStacks.length}</Badge>
          </div>
          <div className="flex items-center gap-2">
            <Button
              variant="outline"
              size="sm"
              className="md:hidden"
              onClick={() => setStacksExpanded(!stacksExpanded)}
            >
              {stacksExpanded ? 'Collapse' : 'Expand'}
            </Button>
          </div>
        </div>

        {stacksExpanded && (
          <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3">
            {filteredStacks.length > 0 ? (
              filteredStacks.map((stack) => (
                <Card
                  key={stack.id}
                  className="hover:shadow-md transition-shadow cursor-pointer"
                  onClick={() => (window.location.href = `/stacks/${stack.id}`)}
                >
                  <CardHeader>
                    <CardTitle>{stack.projectName}</CardTitle>
                    <CardDescription>{stack.directory}</CardDescription>
                  </CardHeader>
                  <CardContent>
                    <div className="flex items-center gap-2 mb-3">
                      <StatusBadge status={stack.status as 'running' | 'stopped' | 'partial' | 'unknown'} pulse={isAnimating(stack.id)} />
                      {stack.containerCount !== undefined && (
                        <Badge variant="outline">{stack.containerCount} containers</Badge>
                      )}
                    </div>
                    <div className="flex items-center gap-2 flex-wrap">
                      {stack.status !== 'running' && (
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={(e) => handleStart(stack.id, e)}
                    disabled={startMutation.isPending}
                    aria-label={`Start stack ${stack.projectName}`}
                  >
                    <Play className="mr-2 h-4 w-4" />
                    Start
                  </Button>
                )}
                {stack.status === 'running' && (
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={(e) => handleStop(stack.id, e)}
                    disabled={stopMutation.isPending}
                    aria-label={`Stop stack ${stack.projectName}`}
                  >
                    <Square className="mr-2 h-4 w-4" />
                    Stop
                  </Button>
                )}
                {stack.status === 'running' && (
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={(e) => handleRestart(stack.id, e)}
                    disabled={restartMutation.isPending}
                    aria-label={`Restart stack ${stack.projectName}`}
                  >
                    <RefreshCw className="mr-2 h-4 w-4" />
                    Restart
                  </Button>
                )}
                <Button
                  variant="outline"
                  size="sm"
                  onClick={(e) => handleDelete(stack.id, stack.projectName, e)}
                  disabled={deletingStackId === stack.id || deleteMutation.isPending}
                  className="text-destructive hover:text-destructive"
                  aria-label={`Delete stack ${stack.projectName}`}
                >
                  <Trash2 className="mr-2 h-4 w-4" />
                  Delete
                </Button>
                    </div>
                  </CardContent>
                </Card>
              ))
            ) : (
              <Card className="col-span-full">
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
          </div>
        )}
      </section>

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
