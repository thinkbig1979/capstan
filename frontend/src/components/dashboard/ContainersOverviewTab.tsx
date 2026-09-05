import { useState, useMemo, Suspense, lazy } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { stacksApi, resourcesApi } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
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
  Info,
} from 'lucide-react'
import { toast } from 'sonner'
import { classifyError } from '@/lib/error-handler'
import { DialogLoadingFallback } from '@/components/LoadingSkeleton'
import type { DashboardStats, DashboardContainerInfo, CommandResult } from '@/types'
import type { DashboardContainerMetric } from '@/hooks/useDashboardMetrics'
import { SortFilterBar } from '@/components/dashboard/SortFilterBar'
import { PruneButton } from '@/components/dashboard/PruneButton'
import { useConfirm } from '@/hooks/useConfirm'
import { useAutoUpdatePolicies } from '@/hooks/useResources'
import { AutoUpdateToggle } from '@/components/dashboard/AutoUpdateToggle'
import { useTextFilter } from '@/hooks/useTextFilter'
import { formatBytes, formatUptime } from '@/lib/format'
import { queryKeys } from '@/lib/query-keys'

// Lazy: the inspect dialog pulls in codemirror to render formatted JSON, but most
// container-tab visits never open it. Keeping it out of this tab's static import
// graph keeps codemirror off the Containers tab's initial load.
const ContainerInspectDialog = lazy(() =>
  import('./ContainerInspectDialog').then((m) => ({ default: m.ContainerInspectDialog })),
)

const CONTAINER_SEARCH_FIELDS = [
  (c: DashboardContainerInfo) => c.name,
  (c: DashboardContainerInfo) => c.projectName,
  (c: DashboardContainerInfo) => c.image,
]

function getMetricColor(percent: number): string {
  if (percent >= 80) return 'bg-destructive'
  if (percent >= 60) return 'bg-warning'
  return 'bg-success'
}

type MetricsStatus = 'connecting' | 'connected' | 'disconnected' | 'reconnecting'

// Distinguishes the three reasons a live-stat cell can be empty so it never reads as a dead
// feature: a stopped container has no stats (—), a connecting stream shows a loading skeleton,
// and a dropped stream shows an explicit "unavailable" hint.
function StatPlaceholder({ state, status }: { state: string; status: MetricsStatus }) {
  if (state !== 'running') {
    return <span className="text-xs text-muted-foreground" title="No live stats for a stopped container">—</span>
  }
  if (status === 'disconnected') {
    return <span className="text-xs italic text-muted-foreground" title="Metrics stream disconnected">unavailable</span>
  }
  return <span className="block h-1.5 w-16 rounded-full bg-muted animate-pulse" aria-label="Loading stats" />
}

function isStandaloneContainer(c: DashboardContainerInfo): boolean {
  return !c.projectName
}

interface ContainerActionsProps {
  mode: 'stack' | 'standalone'
  stackId?: string
  containerId: string
  containerName: string
  containerState: string
  onDelete: (id: string, name: string, isRunning: boolean) => void
  deletePending: boolean
}

function ContainerActions({ mode, stackId, containerId, containerName, containerState, onDelete, deletePending }: ContainerActionsProps) {
  const queryClient = useQueryClient()
  const isRunning = containerState === 'running'
  const label = mode === 'stack' ? 'stack' : 'container'

  const startMutation = useMutation({
    mutationFn: async (): Promise<CommandResult> => {
      if (mode === 'stack' && stackId) return stacksApi.start(stackId)
      const res = await resourcesApi.startContainer(containerId)
      return { status: 'started', output: res.message, duration: 0 }
    },
    onSuccess: () => {
      toast.success(`${label.charAt(0).toUpperCase() + label.slice(1)} started`)
      queryClient.invalidateQueries({ queryKey: queryKeys.dashboardStats() })
      if (mode === 'stack') queryClient.invalidateQueries({ queryKey: queryKeys.stacks() })
    },
    onError: (err) => toast.error(classifyError(err).message || `Failed to start ${label}`),
  })

  const stopMutation = useMutation({
    mutationFn: async (): Promise<CommandResult> => {
      if (mode === 'stack' && stackId) return stacksApi.stop(stackId)
      const res = await resourcesApi.stopContainer(containerId)
      return { status: 'stopped', output: res.message, duration: 0 }
    },
    onSuccess: () => {
      toast.success(`${label.charAt(0).toUpperCase() + label.slice(1)} stopped`)
      queryClient.invalidateQueries({ queryKey: queryKeys.dashboardStats() })
      if (mode === 'stack') queryClient.invalidateQueries({ queryKey: queryKeys.stacks() })
    },
    onError: (err) => toast.error(classifyError(err).message || `Failed to stop ${label}`),
  })

  const restartMutation = useMutation({
    mutationFn: async (): Promise<CommandResult> => {
      if (mode === 'stack' && stackId) return stacksApi.restart(stackId)
      const res = await resourcesApi.restartContainer(containerId)
      return { status: 'restarted', output: res.message, duration: 0 }
    },
    onSuccess: () => {
      toast.success(`${label.charAt(0).toUpperCase() + label.slice(1)} restarted`)
      queryClient.invalidateQueries({ queryKey: queryKeys.dashboardStats() })
      if (mode === 'stack') queryClient.invalidateQueries({ queryKey: queryKeys.stacks() })
    },
    onError: (err) => toast.error(classifyError(err).message || `Failed to restart ${label}`),
  })

  const pullMutation = useMutation({
    mutationFn: async (): Promise<void> => {
      if (mode === 'stack' && stackId) await stacksApi.pull(stackId)
    },
    onSuccess: () => {
      if (mode === 'stack') {
        toast.success('Images pulled')
        queryClient.invalidateQueries({ queryKey: queryKeys.dashboardStats() })
      }
    },
    onError: (err) => {
      if (mode === 'stack') toast.error(classifyError(err).message || 'Failed to pull images')
    },
  })

  const anyPending = startMutation.isPending || stopMutation.isPending || restartMutation.isPending || (mode === 'stack' && pullMutation.isPending) || deletePending

  return (
    <div className="flex items-center gap-1">
      {!isRunning && (
        <Button variant="ghost" size="icon" className="h-8 w-8" onClick={() => startMutation.mutate()} disabled={anyPending} title={`Start ${label}`} aria-label={`Start ${label}`}>
          <Play className="h-3.5 w-3.5" />
        </Button>
      )}
      {isRunning && (
        <Button variant="ghost" size="icon" className="h-8 w-8" onClick={() => stopMutation.mutate()} disabled={anyPending} title={`Stop ${label}`} aria-label={`Stop ${label}`}>
          <Square className="h-3.5 w-3.5" />
        </Button>
      )}
      {isRunning && (
        <Button variant="ghost" size="icon" className="h-8 w-8" onClick={() => restartMutation.mutate()} disabled={anyPending} title={`Restart ${label}`} aria-label={`Restart ${label}`}>
          <RefreshCw className={`h-3.5 w-3.5 ${restartMutation.isPending ? 'animate-spin' : ''}`} />
        </Button>
      )}
      {mode === 'stack' && (
        <Button variant="ghost" size="icon" className="h-8 w-8" onClick={() => pullMutation.mutate()} disabled={anyPending} title="Pull images" aria-label={`Pull images for ${label}`}>
          <Download className={`h-3.5 w-3.5 ${pullMutation.isPending ? 'animate-spin' : ''}`} />
        </Button>
      )}
      <div className="mx-0.5 h-5 w-px bg-border" aria-hidden="true" />
      <Button variant="ghost" size="icon" className="h-8 w-8 text-destructive hover:text-destructive" onClick={() => onDelete(containerId, containerName, isRunning)} disabled={anyPending} title="Remove container" aria-label={`Remove ${label}`}>
        <Trash2 className="h-3.5 w-3.5" />
      </Button>
    </div>
  )
}

const STATUS_ICON_CONFIG: Record<string, { icon: React.ReactNode; color: string; label: string }> = {
  running: { icon: <Play className="h-3.5 w-3.5" />, color: 'text-success', label: 'Running' },
  exited: { icon: <Square className="h-3.5 w-3.5" />, color: 'text-destructive', label: 'Stopped' },
  dead: { icon: <AlertCircle className="h-3.5 w-3.5" />, color: 'text-destructive', label: 'Dead' },
  restarting: { icon: <RefreshCw className="h-3.5 w-3.5" />, color: 'text-warning', label: 'Restarting' },
  paused: { icon: <Square className="h-3.5 w-3.5" />, color: 'text-warning', label: 'Paused' },
  created: { icon: <Square className="h-3.5 w-3.5" />, color: 'text-muted-foreground', label: 'Created' },
}

function StatusIcon({ state }: { state: string }) {
  const c = STATUS_ICON_CONFIG[state] || { icon: <HelpCircle className="h-3.5 w-3.5" />, color: 'text-muted-foreground', label: state }

  return (
    <TooltipProvider>
      <Tooltip>
        <TooltipTrigger asChild>
          <span className={`inline-flex items-center justify-center ${c.color}`}>
            {state === 'running' && (
              <span className="relative flex h-3.5 w-3.5">
                <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-success opacity-75" />
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
  metricsStatus,
  sortBy,
  stackDirMap,
  renderActions,
  onInspect,
}: {
  containers: DashboardContainerInfo[]
  latestMetrics: Record<string, DashboardContainerMetric>
  metricsStatus: MetricsStatus
  sortBy: SortKey
  stackDirMap: Map<string, string>
  renderActions: (container: DashboardContainerInfo, deletePending: boolean) => React.ReactNode
  onInspect: (container: DashboardContainerInfo) => void
}) {
  const { data: policiesData } = useAutoUpdatePolicies()

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

  return (
    <div className="rounded-md border">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead className="w-10" />
            <TableHead>Name</TableHead>
            <TableHead>CPU</TableHead>
            <TableHead>Memory</TableHead>
            <TableHead className="hidden lg:table-cell">Network</TableHead>
            <TableHead className="hidden xl:table-cell">Disk</TableHead>
            <TableHead className="hidden lg:table-cell">Uptime</TableHead>
            <TableHead className="hidden md:table-cell">Restarts</TableHead>
            <TableHead className="hidden md:table-cell">Auto-Update</TableHead>
            <TableHead className="sticky right-0 z-20 bg-background shadow-[-8px_0_8px_-8px_rgba(0,0,0,0.25)]">Actions</TableHead>
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
                  <div className="flex items-center gap-1.5">
                    <Button
                      variant="ghost"
                      size="icon"
                      className="h-6 w-6 shrink-0"
                      onClick={() => onInspect(container)}
                      title="Inspect container"
                    >
                      <Info className="h-3.5 w-3.5 text-muted-foreground" />
                    </Button>
                    <div className="flex flex-col min-w-0">
                      <span className="font-medium font-mono text-[13px] truncate">{container.name}</span>
                      <span className="text-xs text-muted-foreground font-mono truncate max-w-[200px]">{container.image}</span>
                      {container.stackId ? (
                        <TooltipProvider>
                          <Tooltip>
                            <TooltipTrigger asChild>
                              <a
                                href={`/stacks/${container.stackId}`}
                                className="text-xs text-info hover:underline truncate max-w-[200px]"
                              >
                                {container.projectName}
                              </a>
                            </TooltipTrigger>
                            <TooltipContent side="top" className="max-w-md">
                              <p className="font-mono text-xs break-all">{stackDirMap.get(container.stackId) || container.stackId}</p>
                            </TooltipContent>
                          </Tooltip>
                        </TooltipProvider>
                      ) : container.projectName ? (
                        <span className="text-xs text-muted-foreground truncate max-w-[200px]">{container.projectName}</span>
                      ) : null}
                    </div>
                  </div>
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
                    <StatPlaceholder state={container.state} status={metricsStatus} />
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
                    <StatPlaceholder state={container.state} status={metricsStatus} />
                  )}
                </TableCell>
                <TableCell className="hidden lg:table-cell">
                  {m ? (
                    <div className="text-xs space-y-0.5 whitespace-nowrap tabular-nums">
                      <div>↓ {formatBytes(m.netRx)}</div>
                      <div>↑ {formatBytes(m.netTx)}</div>
                    </div>
                  ) : (
                    <StatPlaceholder state={container.state} status={metricsStatus} />
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
                    <AutoUpdateToggle
                      targetType="container"
                      targetId={container.id}
                      enabled={policyMap.get(container.id) ?? false}
                      paused={false}
                      consecutiveFailures={0}
                      globalDisabled={!policiesData?.globalEnabled}
                    />
                  </div>
                </TableCell>
                <TableCell className="sticky right-0 bg-background shadow-[-8px_0_8px_-8px_rgba(0,0,0,0.25)]">
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
  metricsStatus: MetricsStatus
}

export function ContainersOverviewTab({ stats, latestMetrics, metricsStatus }: ContainersOverviewTabProps) {
  const queryClient = useQueryClient()
  const { confirm, ConfirmComponent } = useConfirm()
  const [sortBy, setSortBy] = useState<SortKey>('name')
  const [activeTab, setActiveTab] = useState<string>('stack')
  const [inspectTarget, setInspectTarget] = useState<DashboardContainerInfo | null>(null)

  const { data: stacks = [] } = useQuery({
    queryKey: queryKeys.stacks(),
    queryFn: () => stacksApi.list(),
  })

  const stackDirMap = useMemo(() => {
    const map = new Map<string, string>()
    for (const s of stacks) map.set(s.id, s.directory)
    return map
  }, [stacks])

  const deleteContainerMutation = useMutation({
    mutationFn: ({ id, isRunning }: { id: string; isRunning: boolean }) => resourcesApi.deleteContainer(id, isRunning),
    onSuccess: () => {
      toast.success('Container removed')
      queryClient.invalidateQueries({ queryKey: queryKeys.dashboardStats() })
      queryClient.invalidateQueries({ queryKey: queryKeys.stacks() })
    },
    onError: (err) => toast.error(classifyError(err).message || 'Failed to remove container'),
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

  const allContainers = stats?.containers ?? []
  const { query, setQuery, filtered } = useTextFilter(allContainers, CONTAINER_SEARCH_FIELDS)

  const { stackContainers, otherContainers } = useMemo(() => {
    const stackContainers: DashboardContainerInfo[] = []
    const otherContainers: DashboardContainerInfo[] = []
    for (const c of filtered) {
      if (isStandaloneContainer(c)) {
        otherContainers.push(c)
      } else {
        stackContainers.push(c)
      }
    }
    return { stackContainers, otherContainers }
  }, [filtered])

  if (!stats) {
    return (
      <div className="space-y-4">
        {Array.from({ length: 5 }).map((_, i) => (
          <Skeleton key={i} className="h-12 w-full" />
        ))}
      </div>
    )
  }

  const totalCount = stats.containers?.length || 0
  const filteredCount = filtered.length

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
        searchValue={query}
        onSearchChange={setQuery}
        searchPlaceholder="Filter containers…"
        actions={
          <PruneButton
            resourceType="stopped container"
            pruneFn={(opts) => resourcesApi.pruneContainers(opts)}
            options={{ until: true }}
            confirmMessage="Prune Stopped Containers?"
            confirmDescription="All stopped containers will be permanently removed."
            invalidateKeys={[queryKeys.dashboardStats(), queryKeys.stacks()]}
          />
        }
        countDisplay={
          query
            ? `${filteredCount} of ${totalCount} container${totalCount !== 1 ? 's' : ''}`
            : `${totalCount} container${totalCount !== 1 ? 's' : ''}`
        }
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
              metricsStatus={metricsStatus}
              sortBy={sortBy}
              stackDirMap={stackDirMap}
              onInspect={setInspectTarget}
              renderActions={(container, deletePending) => (
                <ContainerActions
                  mode="stack"
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
              metricsStatus={metricsStatus}
              sortBy={sortBy}
              stackDirMap={stackDirMap}
              onInspect={setInspectTarget}
              renderActions={(container, deletePending) => (
                <ContainerActions
                  mode="standalone"
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
      {inspectTarget && (
        <Suspense fallback={<DialogLoadingFallback testId="inspect-dialog-loading" />}>
          <ContainerInspectDialog
            containerId={inspectTarget.id}
            containerName={inspectTarget.name}
            open={!!inspectTarget}
            onOpenChange={(open) => { if (!open) setInspectTarget(null) }}
          />
        </Suspense>
      )}
    </div>
  )
}
