import { useState, useMemo, Suspense, lazy } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { stacksApi, resourcesApi, type LifecycleResult } from '@/lib/api'
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
import { presentError } from '@/lib/error-handler'
import { DialogLoadingFallback } from '@/components/LoadingSkeleton'
import { RefreshFailedNotice } from '@/components/RefreshFailedNotice'
import type { DashboardStats, DashboardContainerInfo, CommandResult } from '@/types'
import type { DashboardContainerMetric } from '@/hooks/useDashboardMetrics'
import { SortFilterBar } from '@/components/dashboard/SortFilterBar'
import { PruneButton } from '@/components/dashboard/PruneButton'
import { useConfirm } from '@/hooks/useConfirm'
import { useAutoUpdatePolicies } from '@/hooks/useResources'
import { AutoUpdateToggle } from '@/components/dashboard/AutoUpdateToggle'
import { toGlobalAutoUpdateState } from '@/components/dashboard/auto-update-state'
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

// agent-os-yrgn. A container is standalone when it carries no compose project at
// all, OR when it carries one that Capstan has no stack row for. The second half
// is the decision the backend already takes on the update path:
// resolveUpdateStrategy (services/docker_update.go) returns updateViaStandalone
// for exactly this state, under a docblock reading "Anything genuinely absent is
// standalone." Before this, such a row rendered mode="stack" and the three
// lifecycle mutations fell through to the single-container route while `label`,
// derived from `mode` alone, announced "Stack started".
//
// stackLookupFailed is checked FIRST, and it is not a refinement of the stackId
// test. An empty stackId has two causes on the wire and only one of them is an
// absent stack row; when the stacks table could not be READ the row stays in
// stack mode rather than being silently reclassified, because answering a failed
// read with "not a compose stack" is agent-os-g482's P2 defect (backend
// docker_update_apply_fake_test.go:240 pins it on the update path).
//
// The conservative direction is deliberate: a stack row that should have read
// standalone is a wrong LABEL, while a compose container reclassified as
// standalone is a wrong ROUTE.
function isStandaloneContainer(c: DashboardContainerInfo): boolean {
  if (!c.projectName) return true
  if (c.stackLookupFailed) return false
  return !c.stackId
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

// agent-os-yke1, re-scoped by agent-os-yrgn. A stack-mode row can still arrive
// with an EMPTY stackId, but only one of the two ways it used to.
//
// The common case -- a compose project Capstan has no stack row for -- now
// renders as STANDALONE and has no Pull button at all, so it can no longer reach
// this guard. What remains is the backend's `stackErr != nil` branch
// (resolveDashboardStackAssociation, services/docker.go): the stacks table could
// not be READ, so the dashboard defaults stackId to "" and sets
// stackLookupFailed, and isStandaloneContainer deliberately keeps that row in
// stack mode rather than reclassifying it on the strength of a failed read.
//
// So this guard is still REACHABLE through the UI, not merely defence in depth,
// and its test drives it through that state rather than by rendering the
// component directly.
//
// Wording avoids "network", "invalid" and "validation": classifyError REWRITES the
// message when it contains any of those, so the operator would see a connection
// hint instead of this sentence.
export const NO_STACK_FOR_PULL =
  'Capstan has no stack record for this compose project, so its images cannot be pulled.'

function ContainerActions({ mode, stackId, containerId, containerName, containerState, onDelete, deletePending }: ContainerActionsProps) {
  const queryClient = useQueryClient()
  const isRunning = containerState === 'running'
  // `label` names the ROW's context and feeds the button titles and aria-labels
  // only. It stays keyed on `mode` deliberately: the docblock at the top of
  // ContainersOverviewTab.test.tsx pins those singular aria-labels as the
  // discriminator proving an arm clicked a stack-mode button, and de-duplicating
  // them across modes would silently retire it.
  const label = mode === 'stack' ? 'stack' : 'container'

  // agent-os-yrgn. What was ACTIONED is a separate question from how the row
  // renders, and the two diverge for exactly one state: mode="stack" with an
  // empty stackId. After yrgn that means the stacks table could not be READ --
  // isStandaloneContainer keeps such a row in stack mode rather than
  // reclassifying it on a failed read -- and the three lifecycle mutationFns
  // below fall through to the single-container route for it. Deriving the toast
  // from `mode` there announces "Stack started" for one container and
  // invalidates a query the action did not change: the original yrgn defect,
  // surviving on its one remaining reachable path.
  const actedOnStack = mode === 'stack' && Boolean(stackId)
  const actioned = actedOnStack ? 'stack' : 'container'

  const startMutation = useMutation({
    mutationFn: async (): Promise<LifecycleResult | CommandResult> => {
      if (mode === 'stack' && stackId) return stacksApi.start(stackId)
      const res = await resourcesApi.startContainer(containerId)
      return { status: 'started', output: res.message, duration: 0 }
    },
    onSuccess: () => {
      toast.success(`${actioned.charAt(0).toUpperCase() + actioned.slice(1)} started`)
      queryClient.invalidateQueries({ queryKey: queryKeys.dashboardStats() })
      if (actedOnStack) queryClient.invalidateQueries({ queryKey: queryKeys.stacks() })
    },
    // presentError owns the branch this used to hand-roll (agent-os-5g8a).
    // What it preserves, and why each half mattered (agent-os-mc4i): in stack
    // mode start/stop/restart/pull hit stack_lifecycle.go's renderDockerResult,
    // which answers a truth.ActionResult carrying `reason` and neither `error`
    // nor `message` -- a body classifyError structurally cannot read, so the
    // Docker-outage recovery paragraph reached the operator as "503: Something
    // went wrong on the server". causeOf reads the ActionResult reason FIRST,
    // exactly as the old guard did. The container-mode route answers an AppError
    // and still lands on the classifyError arm.
    //
    // The empty-reason case is still handled: causeOf requires a TRUTHY reason
    // before preferring it, and presentFault drops an empty description rather
    // than rendering one. The one- vs two-argument split a vitest spy can see is
    // now decided inside presentFault, in one place, rather than at each site.
    onError: (err) => {
      presentError(err, { fallback: `Failed to start ${actioned}` })
    },
  })

  const stopMutation = useMutation({
    mutationFn: async (): Promise<LifecycleResult | CommandResult> => {
      if (mode === 'stack' && stackId) return stacksApi.stop(stackId)
      const res = await resourcesApi.stopContainer(containerId)
      return { status: 'stopped', output: res.message, duration: 0 }
    },
    onSuccess: () => {
      toast.success(`${actioned.charAt(0).toUpperCase() + actioned.slice(1)} stopped`)
      queryClient.invalidateQueries({ queryKey: queryKeys.dashboardStats() })
      if (actedOnStack) queryClient.invalidateQueries({ queryKey: queryKeys.stacks() })
    },
    // Same presenter as startMutation above.
    onError: (err) => {
      presentError(err, { fallback: `Failed to stop ${actioned}` })
    },
  })

  const restartMutation = useMutation({
    mutationFn: async (): Promise<LifecycleResult | CommandResult> => {
      if (mode === 'stack' && stackId) return stacksApi.restart(stackId)
      const res = await resourcesApi.restartContainer(containerId)
      return { status: 'restarted', output: res.message, duration: 0 }
    },
    onSuccess: () => {
      toast.success(`${actioned.charAt(0).toUpperCase() + actioned.slice(1)} restarted`)
      queryClient.invalidateQueries({ queryKey: queryKeys.dashboardStats() })
      if (actedOnStack) queryClient.invalidateQueries({ queryKey: queryKeys.stacks() })
    },
    // Same presenter as startMutation above.
    onError: (err) => {
      presentError(err, { fallback: `Failed to restart ${actioned}` })
    },
  })

  const pullMutation = useMutation({
    mutationFn: async (): Promise<void> => {
      if (mode !== 'stack') return
      // agent-os-yke1: throw rather than resolve. The old `if (mode === 'stack'
      // && stackId)` resolved undefined for a stack row with no stackId, which
      // React Query reads as success, so onSuccess announced "Images pulled" for
      // a request never sent. Failing here routes it to the onError arm below
      // instead of making onSuccess re-derive the same guard.
      if (!stackId) throw new Error(NO_STACK_FOR_PULL)
      await stacksApi.pull(stackId)
    },
    onSuccess: () => {
      if (mode === 'stack') {
        toast.success('Images pulled')
        queryClient.invalidateQueries({ queryKey: queryKeys.dashboardStats() })
      }
    },
    // Same presenter as startMutation above.
    onError: (err) => {
      if (mode !== 'stack') return
      presentError(err, { fallback: 'Failed to pull images' })
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
  const policiesQuery = useAutoUpdatePolicies()
  const policiesData = policiesQuery.data
  const globalAutoUpdateState = toGlobalAutoUpdateState(policiesQuery)

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
                      globalState={globalAutoUpdateState}
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

  // agent-os-6iui: read here purely to DISCLOSE a failed refresh. The data
  // still comes from ContainerTable's own copy; this call only drives the
  // notice below, and React Query dedupes by key so it is not a second request.
  //
  // Lifted rather than placed where the data is read because ContainerTable is
  // rendered once per tab panel and the SAME payload feeds the toggles in BOTH.
  // A notice inside it would therefore describe more than it sits beside, and
  // would unmount and re-mount as the operator switches tabs. (Not, as first
  // supposed, because it would render twice: Radix unmounts the inactive
  // TabsContent, so only one ContainerTable is mounted at a time — unless
  // forceMount is ever added, which this placement also survives.)
  const autoUpdatePoliciesQuery = useAutoUpdatePolicies()

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
    // Same presenter as startMutation above, but via a different route:
    // deleteContainer answers renderDockerResult at resource_mutations.go:172 in
    // both modes, so this site was never covered by the AppError repair.
    onError: (err) => {
      presentError(err, { fallback: 'Failed to remove container' })
    },
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

  // agent-os-p9e1: the server omits the list when Docker could not answer.
  // Rendering it as [] would say "0 containers" on a host that has some.
  if (stats.containers === undefined) {
    return (
      <Card>
        <CardContent className="flex flex-col items-center justify-center py-12">
          <p className="text-sm text-muted-foreground">The container list is unavailable.</p>
        </CardContent>
      </Card>
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

      {/* agent-os-6iui: one notice per rendered LIST, not per control. The same
          payload drives the global switch and every per-container toggle in
          both tabs, so a notice beside one control would leave the rest silent
          and teach the operator that silence means fresh. */}
      {autoUpdatePoliciesQuery.isError && !!autoUpdatePoliciesQuery.data && (
        <RefreshFailedNotice
          what="the auto-update settings"
          onRetry={() => autoUpdatePoliciesQuery.refetch()}
        />
      )}

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
