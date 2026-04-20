import { useState, useMemo } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { stacksApi, resourcesApi } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import {
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
} from '@/components/ui/table'
import {
  Play, Square, RefreshCw, Download, Trash2, HelpCircle, AlertCircle,
} from 'lucide-react'
import { toast } from 'sonner'
import type { DashboardStats, DashboardContainerInfo } from '@/types'
import type { DashboardContainerMetric } from '@/hooks/useDashboardMetrics'
import { SortFilterBar } from '@/components/dashboard/SortFilterBar'
import { PruneButton } from '@/components/dashboard/PruneButton'
import { useConfirm } from '@/components/ConfirmDialog'
import { useAutoUpdatePolicies, useToggleAutoUpdate } from '@/hooks/useResources'
import { formatBytes } from '@/lib/format'

function getMetricColor(percent: number): string {
  if (percent >= 80) return 'bg-red-500'
  if (percent >= 60) return 'bg-yellow-500'
  return 'bg-green-500'
}

function formatUptime(startedAt: string): string {
  if (!startedAt) return '-'
  const start = new Date(startedAt)
  const now = new Date()
  const diffMs = now.getTime() - start.getTime()
  if (diffMs < 0) return '-'
  const diffMins = Math.floor(diffMs / 60000)
  if (diffMins < 60) return `${diffMins}m`
  const diffHours = Math.floor(diffMins / 60)
  if (diffHours < 24) return `${diffHours}h ${diffMins % 60}m`
  const diffDays = Math.floor(diffHours / 24)
  return `${diffDays}d ${diffHours % 24}h`
}

function isStandaloneContainer(c: DashboardContainerInfo): boolean {
  return !c.projectName
}

interface StackContainerActionsProps {
  stackId: string
  containerId: string
  containerName: string
  containerState: string
  onDelete: (id: string, name: string, isRunning: boolean) => void
  deletePending: boolean
}

function StackContainerActions({ stackId, containerId, containerName, containerState, onDelete, deletePending }: StackContainerActionsProps) {
  const queryClient = useQueryClient()
  const isRunning = containerState === 'running'

  const startMutation = useMutation({
    mutationFn: () => stacksApi.start(stackId),
    onSuccess: () => {
      toast.success('Stack started')
      queryClient.invalidateQueries({ queryKey: ['dashboard-stats'] })
      queryClient.invalidateQueries({ queryKey: ['stacks'] })
    },
    onError: () => toast.error('Failed to start stack'),
  })

  const stopMutation = useMutation({
    mutationFn: () => stacksApi.stop(stackId),
    onSuccess: () => {
      toast.success('Stack stopped')
      queryClient.invalidateQueries({ queryKey: ['dashboard-stats'] })
      queryClient.invalidateQueries({ queryKey: ['stacks'] })
    },
    onError: () => toast.error('Failed to stop stack'),
  })

  const restartMutation = useMutation({
    mutationFn: () => stacksApi.restart(stackId),
    onSuccess: () => {
      toast.success('Stack restarted')
      queryClient.invalidateQueries({ queryKey: ['dashboard-stats'] })
      queryClient.invalidateQueries({ queryKey: ['stacks'] })
    },
    onError: () => toast.error('Failed to restart stack'),
  })

  const pullMutation = useMutation({
    mutationFn: () => stacksApi.pull(stackId),
    onSuccess: () => {
      toast.success('Images pulled')
      queryClient.invalidateQueries({ queryKey: ['dashboard-stats'] })
    },
    onError: () => toast.error('Failed to pull images'),
  })

  const anyPending = startMutation.isPending || stopMutation.isPending || restartMutation.isPending || pullMutation.isPending || deletePending

  return (
    <div className="flex items-center gap-1">
      {!isRunning && (
        <Button variant="ghost" size="icon" className="h-7 w-7" onClick={() => startMutation.mutate()} disabled={anyPending} title="Start stack">
          <Play className="h-3.5 w-3.5" />
        </Button>
      )}
      {isRunning && (
        <Button variant="ghost" size="icon" className="h-7 w-7" onClick={() => stopMutation.mutate()} disabled={anyPending} title="Stop stack">
          <Square className="h-3.5 w-3.5" />
        </Button>
      )}
      {isRunning && (
        <Button variant="ghost" size="icon" className="h-7 w-7" onClick={() => restartMutation.mutate()} disabled={anyPending} title="Restart stack">
          <RefreshCw className={`h-3.5 w-3.5 ${restartMutation.isPending ? 'animate-spin' : ''}`} />
        </Button>
      )}
      <Button variant="ghost" size="icon" className="h-7 w-7" onClick={() => pullMutation.mutate()} disabled={anyPending} title="Pull images">
        <Download className={`h-3.5 w-3.5 ${pullMutation.isPending ? 'animate-spin' : ''}`} />
      </Button>
      <Button variant="ghost" size="icon" className="h-7 w-7 text-destructive hover:text-destructive" onClick={() => onDelete(containerId, containerName, isRunning)} disabled={anyPending} title="Remove container">
        <Trash2 className="h-3.5 w-3.5" />
      </Button>
    </div>
  )
}

interface StandaloneContainerActionsProps {
  containerId: string
  containerName: string
  containerState: string
  onDelete: (id: string, name: string, isRunning: boolean) => void
  deletePending: boolean
}

function StandaloneContainerActions({ containerId, containerName, containerState, onDelete, deletePending }: StandaloneContainerActionsProps) {
  const queryClient = useQueryClient()
  const isRunning = containerState === 'running'

  const startMutation = useMutation({
    mutationFn: () => resourcesApi.startContainer(containerId),
    onSuccess: () => {
      toast.success('Container started')
      queryClient.invalidateQueries({ queryKey: ['dashboard-stats'] })
    },
    onError: () => toast.error('Failed to start container'),
  })

  const stopMutation = useMutation({
    mutationFn: () => resourcesApi.stopContainer(containerId),
    onSuccess: () => {
      toast.success('Container stopped')
      queryClient.invalidateQueries({ queryKey: ['dashboard-stats'] })
    },
    onError: () => toast.error('Failed to stop container'),
  })

  const restartMutation = useMutation({
    mutationFn: () => resourcesApi.restartContainer(containerId),
    onSuccess: () => {
      toast.success('Container restarted')
      queryClient.invalidateQueries({ queryKey: ['dashboard-stats'] })
    },
    onError: () => toast.error('Failed to restart container'),
  })

  const anyPending = startMutation.isPending || stopMutation.isPending || restartMutation.isPending || deletePending

  return (
    <div className="flex items-center gap-1">
      {!isRunning && (
        <Button variant="ghost" size="icon" className="h-7 w-7" onClick={() => startMutation.mutate()} disabled={anyPending} title="Start container">
          <Play className="h-3.5 w-3.5" />
        </Button>
      )}
      {isRunning && (
        <Button variant="ghost" size="icon" className="h-7 w-7" onClick={() => stopMutation.mutate()} disabled={anyPending} title="Stop container">
          <Square className="h-3.5 w-3.5" />
        </Button>
      )}
      {isRunning && (
        <Button variant="ghost" size="icon" className="h-7 w-7" onClick={() => restartMutation.mutate()} disabled={anyPending} title="Restart container">
          <RefreshCw className={`h-3.5 w-3.5 ${restartMutation.isPending ? 'animate-spin' : ''}`} />
        </Button>
      )}
      <Button variant="ghost" size="icon" className="h-7 w-7 text-destructive hover:text-destructive" onClick={() => onDelete(containerId, containerName, isRunning)} disabled={anyPending} title="Remove container">
        <Trash2 className="h-3.5 w-3.5" />
      </Button>
    </div>
  )
}

function StatusIcon({ state }: { state: string }) {
  const config: Record<string, { icon: React.ReactNode; color: string; label: string }> = {
    running: { icon: <Play className="h-3.5 w-3.5" />, color: 'text-green-500', label: 'Running' },
    exited: { icon: <Square className="h-3.5 w-3.5" />, color: 'text-red-500', label: 'Stopped' },
    dead: { icon: <AlertCircle className="h-3.5 w-3.5" />, color: 'text-red-500', label: 'Dead' },
    restarting: { icon: <RefreshCw className="h-3.5 w-3.5" />, color: 'text-yellow-500', label: 'Restarting' },
    paused: { icon: <Square className="h-3.5 w-3.5" />, color: 'text-yellow-500', label: 'Paused' },
    created: { icon: <Square className="h-3.5 w-3.5" />, color: 'text-gray-500', label: 'Created' },
  }
  const c = config[state] || { icon: <HelpCircle className="h-3.5 w-3.5" />, color: 'text-gray-500', label: state }

  return (
    <TooltipProvider>
      <Tooltip>
        <TooltipTrigger asChild>
          <span className={`inline-flex items-center justify-center ${c.color}`}>
            {state === 'running' && (
              <span className="relative flex h-3.5 w-3.5">
                <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-green-400 opacity-75" />
                <span className="relative inline-flex rounded-full h-3.5 w-3.5">{c.icon}</span>
              </span>
            )}
            {state !== 'running' && c.icon}
          </span>
        </TooltipTrigger>
        <TooltipContent>
          <p>{c.label}</p>
        </TooltipContent>
      </Tooltip>
    </TooltipProvider>
  )
}

type SortKey = 'name' | 'cpu' | 'memory' | 'stack'

function ContainerTable({
  containers,
  latestMetrics,
  sortBy,
  renderActions,
}: {
  containers: DashboardContainerInfo[]
  latestMetrics: Record<string, DashboardContainerMetric>
  sortBy: SortKey
  renderActions: (container: DashboardContainerInfo, deletePending: boolean) => React.ReactNode
}) {
  const { data: policiesData } = useAutoUpdatePolicies()
  const toggleAutoUpdate = useToggleAutoUpdate()

  const policyMap = useMemo(() => {
    if (!policiesData?.policies) return new Map<string, boolean>()
    const map = new Map<string, boolean>()
    for (const p of policiesData.policies) {
      if (p.targetType === 'container') {
        map.set(p.targetId, p.enabled)
      }
    }
    return map
  }, [policiesData])

  const sorted = useMemo(() => {
    const sorted = [...containers]
    switch (sortBy) {
      case 'name':
        return sorted.sort((a, b) => a.name.localeCompare(b.name))
      case 'cpu':
        return sorted.sort((a, b) => (latestMetrics[b.id]?.cpuPercent || 0) - (latestMetrics[a.id]?.cpuPercent || 0))
      case 'memory':
        return sorted.sort((a, b) => (latestMetrics[b.id]?.memUsage || 0) - (latestMetrics[a.id]?.memUsage || 0))
      case 'stack':
        return sorted.sort((a, b) => (a.projectName || '').localeCompare(b.projectName || ''))
      default:
        return sorted
    }
  }, [containers, latestMetrics, sortBy])

  const handleToggleAutoUpdate = (containerId: string, enabled: boolean) => {
    toggleAutoUpdate.mutate(
      { targetType: 'container', targetId: containerId, enabled },
      {
        onSuccess: () => toast.success(enabled ? 'Auto-update enabled' : 'Auto-update disabled'),
        onError: () => toast.error('Failed to update auto-update policy'),
      },
    )
  }

  return (
    <div className="rounded-md border">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead className="w-10" />
            <TableHead>Name</TableHead>
            <TableHead>Stack</TableHead>
            <TableHead>CPU</TableHead>
            <TableHead>Memory</TableHead>
            <TableHead className="hidden lg:table-cell">Network</TableHead>
            <TableHead className="hidden xl:table-cell">Disk</TableHead>
            <TableHead className="hidden lg:table-cell">Uptime</TableHead>
            <TableHead className="hidden md:table-cell">Restarts</TableHead>
            <TableHead className="hidden md:table-cell">Auto-Update</TableHead>
            <TableHead>Actions</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {sorted.map((container: DashboardContainerInfo) => {
            const m = latestMetrics[container.id]
            return (
              <TableRow key={container.id}>
                <TableCell>
                  <StatusIcon state={container.state} />
                </TableCell>
                <TableCell>
                  <div className="flex flex-col">
                    <span className="font-medium text-sm">{container.name}</span>
                    <span className="text-xs text-muted-foreground truncate max-w-[200px]">{container.image}</span>
                  </div>
                </TableCell>
                <TableCell>
                  {container.stackId ? (
                    <a
                      href={`/stacks/${container.stackId}`}
                      className="text-sm text-blue-500 hover:underline"
                    >
                      {container.projectName}
                    </a>
                  ) : container.projectName ? (
                    <span className="text-sm text-muted-foreground">{container.projectName}</span>
                  ) : (
                    <span className="text-xs text-muted-foreground italic">standalone</span>
                  )}
                </TableCell>
                <TableCell>
                  {m ? (
                    <div className="space-y-1">
                      <span className="text-sm font-medium">{m.cpuPercent.toFixed(1)}%</span>
                      <div className="h-1.5 w-16 rounded-full bg-muted">
                        <div
                          className={`h-full rounded-full ${getMetricColor(m.cpuPercent)}`}
                          style={{ width: `${Math.min(m.cpuPercent, 100)}%` }}
                        />
                      </div>
                    </div>
                  ) : (
                    <span className="text-xs text-muted-foreground">-</span>
                  )}
                </TableCell>
                <TableCell>
                  {m ? (
                    <div className="space-y-1">
                      <span className="text-sm">{formatBytes(m.memUsage)}</span>
                      <div className="h-1.5 w-16 rounded-full bg-muted">
                        <div
                          className={`h-full rounded-full ${getMetricColor(m.memPercent)}`}
                          style={{ width: `${Math.min(m.memPercent, 100)}%` }}
                        />
                      </div>
                    </div>
                  ) : (
                    <span className="text-xs text-muted-foreground">-</span>
                  )}
                </TableCell>
                <TableCell className="hidden lg:table-cell">
                  {m ? (
                    <div className="text-xs space-y-0.5">
                      <div>↓ {formatBytes(m.netRx)}</div>
                      <div>↑ {formatBytes(m.netTx)}</div>
                    </div>
                  ) : (
                    <span className="text-xs text-muted-foreground">-</span>
                  )}
                </TableCell>
                <TableCell className="hidden xl:table-cell">
                  {container.imageSize > 0 ? (
                    <span className="text-sm">{formatBytes(container.imageSize)}</span>
                  ) : (
                    <span className="text-xs text-muted-foreground">-</span>
                  )}
                </TableCell>
                <TableCell className="hidden lg:table-cell">
                  <span className="text-sm">{formatUptime(container.startedAt)}</span>
                </TableCell>
                <TableCell className="hidden md:table-cell">
                  {container.restartCount > 0 ? (
                    <Badge variant="secondary" className="text-xs">{container.restartCount}</Badge>
                  ) : (
                    <span className="text-xs text-muted-foreground">0</span>
                  )}
                </TableCell>
                <TableCell className="hidden md:table-cell">
                  <div className="flex items-center">
                    <Switch
                      checked={policyMap.get(container.id) ?? false}
                      onCheckedChange={(checked) => handleToggleAutoUpdate(container.id, checked)}
                      disabled={toggleAutoUpdate.isPending}
                      aria-label="Toggle auto-update"
                    />
                  </div>
                </TableCell>
                <TableCell>
                  {renderActions(container, false)}
                </TableCell>
              </TableRow>
            )
          })}
        </TableBody>
      </Table>
    </div>
  )
}

interface ContainersOverviewTabProps {
  stats: DashboardStats | undefined
  latestMetrics: Record<string, DashboardContainerMetric>
}

export function ContainersOverviewTab({ stats, latestMetrics }: ContainersOverviewTabProps) {
  const queryClient = useQueryClient()
  const { confirm, ConfirmComponent } = useConfirm()
  const [sortBy, setSortBy] = useState<SortKey>('name')
  const [activeTab, setActiveTab] = useState<string>('stack')

  const deleteContainerMutation = useMutation({
    mutationFn: ({ id, isRunning }: { id: string; isRunning: boolean }) => resourcesApi.deleteContainer(id, isRunning),
    onSuccess: () => {
      toast.success('Container removed')
      queryClient.invalidateQueries({ queryKey: ['dashboard-stats'] })
      queryClient.invalidateQueries({ queryKey: ['stacks'] })
    },
    onError: () => toast.error('Failed to remove container'),
  })

  const handleDeleteContainer = async (containerId: string, containerName: string, isRunning: boolean) => {
    const confirmed = await confirm(
      `Remove Container "${containerName}"?`,
      isRunning
        ? 'This container is running and will be force-removed. This cannot be undone.'
        : 'This stopped container will be removed. This cannot be undone.',
      { confirmText: 'Remove', isDangerous: true },
    )
    if (confirmed) deleteContainerMutation.mutate({ id: containerId, isRunning })
  }

  const { stackContainers, otherContainers } = useMemo(() => {
    if (!stats?.containers) return { stackContainers: [], otherContainers: [] }
    const stackContainers: DashboardContainerInfo[] = []
    const otherContainers: DashboardContainerInfo[] = []
    for (const c of stats.containers) {
      if (isStandaloneContainer(c)) {
        otherContainers.push(c)
      } else {
        stackContainers.push(c)
      }
    }
    return { stackContainers, otherContainers }
  }, [stats])

  if (!stats) {
    return (
      <div className="space-y-4">
        {Array.from({ length: 5 }).map((_, i) => (
          <Skeleton key={i} className="h-12 w-full" />
        ))}
      </div>
    )
  }

  const totalCount = (stats.containers?.length || 0)

  return (
    <div className="space-y-4">
      <SortFilterBar
        sortOptions={[
          { key: 'name', label: 'Name' },
          { key: 'stack', label: 'Stack' },
          { key: 'cpu', label: 'CPU' },
          { key: 'memory', label: 'Memory' },
        ]}
        sortValue={sortBy}
        onSortChange={(key) => setSortBy(key as SortKey)}
        actions={
          <PruneButton
            resourceType="stopped container"
            pruneFn={() => resourcesApi.pruneContainers()}
            confirmMessage="Prune Stopped Containers?"
            confirmDescription="All stopped containers will be permanently removed."
            invalidateKeys={[['dashboard-stats'], ['stacks']]}
          />
        }
        countDisplay={`${totalCount} container${totalCount !== 1 ? 's' : ''}`}
      />

      <Tabs value={activeTab} onValueChange={setActiveTab}>
        <TabsList>
          <TabsTrigger value="stack">
            Stack Containers
          </TabsTrigger>
          <TabsTrigger value="other">
            Other Containers
          </TabsTrigger>
        </TabsList>

        <TabsContent value="stack" className="mt-4">
          {stackContainers.length === 0 ? (
            <Card>
              <CardContent className="flex flex-col items-center justify-center py-12">
                <p className="text-lg font-semibold">No Stack Containers</p>
                <p className="text-sm text-muted-foreground">
                  No containers managed by compose stacks
                </p>
              </CardContent>
            </Card>
          ) : (
            <ContainerTable
              containers={stackContainers}
              latestMetrics={latestMetrics}
              sortBy={sortBy}
              renderActions={(container, deletePending) => (
                <StackContainerActions
                  stackId={container.stackId}
                  containerId={container.id}
                  containerName={container.name}
                  containerState={container.state}
                  onDelete={handleDeleteContainer}
                  deletePending={deletePending}
                />
              )}
            />
          )}
        </TabsContent>

        <TabsContent value="other" className="mt-4">
          {otherContainers.length === 0 ? (
            <Card>
              <CardContent className="flex flex-col items-center justify-center py-12">
                <p className="text-lg font-semibold">No Standalone Containers</p>
                <p className="text-sm text-muted-foreground">
                  No containers found outside of compose stacks
                </p>
              </CardContent>
            </Card>
          ) : (
            <ContainerTable
              containers={otherContainers}
              latestMetrics={latestMetrics}
              sortBy={sortBy}
              renderActions={(container, deletePending) => (
                <StandaloneContainerActions
                  containerId={container.id}
                  containerName={container.name}
                  containerState={container.state}
                  onDelete={handleDeleteContainer}
                  deletePending={deletePending}
                />
              )}
            />
          )}
        </TabsContent>
      </Tabs>
      <ConfirmComponent />
    </div>
  )
}
