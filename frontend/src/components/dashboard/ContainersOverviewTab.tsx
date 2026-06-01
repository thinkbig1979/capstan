import { useState, useMemo, useEffect, useRef } from 'react'
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
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
} from '@/components/ui/dialog'
import {
  Play, Square, RefreshCw, Download, Trash2, HelpCircle, AlertCircle,
  Info, Copy, Check,
} from 'lucide-react'
import { toast } from 'sonner'
import { classifyError } from '@/lib/error-handler'
import { EditorView, basicSetup } from 'codemirror'
import { EditorState } from '@codemirror/state'
import { json } from '@codemirror/lang-json'
import { oneDark } from '@codemirror/theme-one-dark'
import { useUIStore } from '@/stores/uiStore'
import type { DashboardStats, DashboardContainerInfo, CommandResult } from '@/types'
import type { DashboardContainerMetric } from '@/hooks/useDashboardMetrics'
import { SortFilterBar } from '@/components/dashboard/SortFilterBar'
import { PruneButton } from '@/components/dashboard/PruneButton'
import { useConfirm } from '@/components/ConfirmDialog'
import { useAutoUpdatePolicies } from '@/hooks/useResources'
import { AutoUpdateToggle } from '@/components/dashboard/AutoUpdateToggle'
import { formatBytes } from '@/lib/format'

function getMetricColor(percent: number): string {
  if (percent >= 80) return 'bg-destructive'
  if (percent >= 60) return 'bg-warning'
  return 'bg-success'
}

export function formatUptime(startedAt: string): string {
  if (!startedAt) return '—'
  const start = new Date(startedAt)
  // Guard unset/zero timestamps: a stopped or never-started container reports
  // Go's zero time (0001-01-01T00:00:00Z), which would otherwise render as ~739766d.
  if (isNaN(start.getTime()) || start.getUTCFullYear() < 2000) return '—'
  const now = new Date()
  const diffMs = now.getTime() - start.getTime()
  if (diffMs < 0) return '—'
  const diffMins = Math.floor(diffMs / 60000)
  if (diffMins < 60) return `${diffMins}m`
  const diffHours = Math.floor(diffMins / 60)
  if (diffHours < 24) return `${diffHours}h ${diffMins % 60}m`
  const diffDays = Math.floor(diffHours / 24)
  return `${diffDays}d ${diffHours % 24}h`
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
      queryClient.invalidateQueries({ queryKey: ['dashboard-stats'] })
      if (mode === 'stack') queryClient.invalidateQueries({ queryKey: ['stacks'] })
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
      queryClient.invalidateQueries({ queryKey: ['dashboard-stats'] })
      if (mode === 'stack') queryClient.invalidateQueries({ queryKey: ['stacks'] })
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
      queryClient.invalidateQueries({ queryKey: ['dashboard-stats'] })
      if (mode === 'stack') queryClient.invalidateQueries({ queryKey: ['stacks'] })
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
        queryClient.invalidateQueries({ queryKey: ['dashboard-stats'] })
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

function StatusIcon({ state }: { state: string }) {
  const config: Record<string, { icon: React.ReactNode; color: string; label: string }> = {
    running: { icon: <Play className="h-3.5 w-3.5" />, color: 'text-success', label: 'Running' },
    exited: { icon: <Square className="h-3.5 w-3.5" />, color: 'text-destructive', label: 'Stopped' },
    dead: { icon: <AlertCircle className="h-3.5 w-3.5" />, color: 'text-destructive', label: 'Dead' },
    restarting: { icon: <RefreshCw className="h-3.5 w-3.5" />, color: 'text-warning', label: 'Restarting' },
    paused: { icon: <Square className="h-3.5 w-3.5" />, color: 'text-warning', label: 'Paused' },
    created: { icon: <Square className="h-3.5 w-3.5" />, color: 'text-muted-foreground', label: 'Created' },
  }
  const c = config[state] || { icon: <HelpCircle className="h-3.5 w-3.5" />, color: 'text-muted-foreground', label: state }

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

function ContainerInspectDialog({
  containerId,
  containerName,
  open,
  onOpenChange,
}: {
  containerId: string
  containerName: string
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const [inspectData, setInspectData] = useState<Record<string, unknown> | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [copied, setCopied] = useState(false)
  const editorRef = useRef<HTMLDivElement>(null)
  const viewRef = useRef<EditorView | null>(null)
  const { theme } = useUIStore()

  const isDark = useMemo(
    () =>
      theme === 'dark' ||
      (theme === 'system' &&
        typeof window !== 'undefined' &&
        window.matchMedia('(prefers-color-scheme: dark)').matches),
    [theme],
  )

  useEffect(() => {
    if (!open) return
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setCopied(false)
     
    setLoading(true)
     
    setError(null)
    resourcesApi
      .inspectContainer(containerId)
      .then((data) => setInspectData(data))
      .catch(() => setError('Failed to inspect container'))
      .finally(() => setLoading(false))
  }, [containerId, open])

  useEffect(() => {
    if (!inspectData || !editorRef.current || loading) return

    viewRef.current?.destroy()
    viewRef.current = null

    const formattedJson = JSON.stringify(inspectData, null, 2)

    const extensions = [
      basicSetup,
      json(),
      EditorState.readOnly.of(true),
      EditorView.theme({
        '&': {
          fontSize: '13px',
          height: '60vh',
        },
        '.cm-scroller': {
          fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace',
          overflow: 'auto',
        },
        '.cm-content': {
          caretColor: 'transparent',
        },
        '.cm-cursor': {
          display: 'none',
        },
        '.cm-gutters': {
          backgroundColor: 'transparent',
        },
      }),
    ]

    if (isDark) {
      extensions.push(oneDark)
    }

    const state = EditorState.create({
      doc: formattedJson,
      extensions,
    })

    viewRef.current = new EditorView({
      state,
      parent: editorRef.current,
    })

    return () => {
      viewRef.current?.destroy()
      viewRef.current = null
    }
  }, [inspectData, isDark, loading])

  const handleCopy = async () => {
    if (!inspectData) return
    await navigator.clipboard.writeText(JSON.stringify(inspectData, null, 2))
    setCopied(true)
    toast.success('Copied to clipboard')
    setTimeout(() => setCopied(false), 2000)
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-4xl max-h-[85vh] flex flex-col p-0">
        <DialogHeader className="px-6 pt-6 pb-2 shrink-0">
          <DialogTitle className="flex items-center gap-2">
            <Info className="h-5 w-5" />
            Inspect: {containerName}
          </DialogTitle>
          <DialogDescription>
            Container ID: {containerId.slice(0, 12)}
          </DialogDescription>
        </DialogHeader>
        <div className="flex-1 min-h-0 px-6 pb-2 flex items-center justify-end">
          {inspectData && (
            <Button variant="outline" size="sm" onClick={handleCopy} className="h-7 text-xs gap-1.5">
              {copied ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />}
              {copied ? 'Copied' : 'Copy JSON'}
            </Button>
          )}
        </div>
        <div className="flex-1 min-h-0 px-6 pb-6">
          {loading && (
            <div className="space-y-2">
              {Array.from({ length: 8 }).map((_, i) => (
                <Skeleton key={i} className="h-5 w-full" />
              ))}
            </div>
          )}
          {error && (
            <div className="flex items-center justify-center py-12 text-destructive">
              <AlertCircle className="h-5 w-5 mr-2" />
              {error}
            </div>
          )}
          {inspectData && !loading && (
            <div ref={editorRef} className="rounded-md border overflow-hidden" />
          )}
        </div>
      </DialogContent>
    </Dialog>
  )
}

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
            <TableHead>Stack</TableHead>
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
                      <span className="font-medium text-sm truncate">{container.name}</span>
                      <span className="text-xs text-muted-foreground truncate max-w-[200px]">{container.image}</span>
                    </div>
                  </div>
                </TableCell>
                <TableCell>
                  {container.stackId ? (
                    <TooltipProvider>
                      <Tooltip>
                        <TooltipTrigger asChild>
                          <a
                            href={`/stacks/${container.stackId}`}
                            className="text-sm text-info hover:underline"
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
                    <div className="text-xs space-y-0.5">
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
    queryKey: ['stacks'],
    queryFn: () => stacksApi.list(),
    staleTime: 30_000,
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
      queryClient.invalidateQueries({ queryKey: ['dashboard-stats'] })
      queryClient.invalidateQueries({ queryKey: ['stacks'] })
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
        <ContainerInspectDialog
          containerId={inspectTarget.id}
          containerName={inspectTarget.name}
          open={!!inspectTarget}
          onOpenChange={(open) => { if (!open) setInspectTarget(null) }}
        />
      )}
    </div>
  )
}
