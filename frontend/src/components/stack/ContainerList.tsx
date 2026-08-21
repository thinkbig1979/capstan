import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Status, StatusDot, type StatusTone } from '@/components/ui/status'
import { Play, Square, AlertCircle, RefreshCw, ScrollText, SquareTerminal, RotateCw } from 'lucide-react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'
import { resourcesApi } from '@/lib/api'
import { classifyError } from '@/lib/error-handler'
import { queryKeys } from '@/lib/query-keys'
import { formatBytes } from '@/lib/format'
import type { Container } from '@/types'
import type { ContainerMetric, ContainerMetricHistory } from '@/hooks/useMetricsBase'
import { useTextFilter } from '@/hooks/useTextFilter'
import { TableSearch } from '@/components/ui/table-search'

interface ContainerListProps {
  containers: Container[]
  /** Stack the containers belong to — used to refresh it after a per-container restart. */
  stackId: string
  /** Live metrics keyed by container id, from the per-stack metrics stream. */
  latestMetrics?: Record<string, ContainerMetric>
  /** Metric histories, used to match metrics to containers by name when ids differ. */
  metricNames?: ContainerMetricHistory[]
  onShowLogs?: (containerName: string) => void
  onOpenShell?: (containerId: string) => void
}

const CONTAINER_SEARCH_FIELDS = [
  (c: Container) => c.name,
  (c: Container) => c.image,
  (c: Container) => c.state,
  (c: Container) => c.status,
]

function getHealthBadge(health?: string) {
  if (!health) return <Badge variant="outline">none</Badge>
  if (health === 'healthy') return (
    <Status tone="success" className="gap-1">
      <Play className="h-3 w-3" aria-hidden="true" />Healthy
    </Status>
  )
  if (health === 'unhealthy') return (
    <Status tone="error" className="gap-1">
      <AlertCircle className="h-3 w-3" aria-hidden="true" />Unhealthy
    </Status>
  )
  return <Badge variant="outline">{health}</Badge>
}

function stateDotTone(c: Container): StatusTone {
  if (c.health === 'unhealthy') return 'error'
  if (c.state === 'running') return 'success'
  if (c.state === 'restarting') return 'warning'
  if (c.state === 'exited' || c.state === 'dead') return 'neutral'
  return 'neutral'
}

function StateCell({ container }: { container: Container }) {
  return (
    <div className="flex items-center gap-2" title={container.status}>
      {container.state === 'running' && <Play className="h-3 w-3 text-success" aria-hidden="true" />}
      {container.state === 'exited' && <Square className="h-3 w-3 text-muted-foreground" aria-hidden="true" />}
      {container.state === 'dead' && <Square className="h-3 w-3 text-destructive" aria-hidden="true" />}
      {container.state === 'restarting' && <RefreshCw className="h-3 w-3 text-warning" aria-hidden="true" />}
      <span className="capitalize">{container.state}</span>
    </div>
  )
}

// "ghcr.io/immich-app/server:v1.119" → repo part muted, tag emphasized, so the
// version reads at a glance. A digest or untagged image just renders whole.
function ImageCell({ image }: { image: string }) {
  const slash = image.lastIndexOf('/')
  const colon = image.lastIndexOf(':')
  if (colon > slash) {
    return (
      <span className="font-mono text-xs text-muted-foreground">
        {image.slice(0, colon + 1)}
        <span className="text-foreground">{image.slice(colon + 1)}</span>
      </span>
    )
  }
  return <span className="font-mono text-xs text-muted-foreground">{image}</span>
}

interface ParsedPort {
  key: string
  label: string
  href?: string
}

// PortBinding.host arrives as "<bind-ip>:<host-port>" (see backend
// services/docker.go). Bind-alls link via the page's own hostname, since
// 0.0.0.0 is meaningless as a link target from the browser.
function parsePort(p: { host: string; container: string; protocol: string }): ParsedPort {
  const key = `${p.host}-${p.container}`
  const containerPort = p.container.replace(/\/(tcp|udp|sctp)$/i, '')
  const colon = p.host.lastIndexOf(':')
  const suffix = p.protocol.toLowerCase() === 'tcp' ? '' : `/${p.protocol}`
  if (colon === -1) {
    return { key, label: `${p.host}:${containerPort}${suffix}` }
  }
  const ip = p.host.slice(0, colon)
  const hostPort = p.host.slice(colon + 1)
  const label = `${hostPort}→${containerPort}${suffix}`
  if (p.protocol.toLowerCase() !== 'tcp') return { key, label }
  const linkHost =
    ip === '' || ip === '0.0.0.0' || ip === '::' || ip === '[::]'
      ? window.location.hostname
      : ip
  return { key, label, href: `http://${linkHost}:${hostPort}` }
}

function PortChips({ ports }: { ports: Container['ports'] }) {
  if (ports.length === 0) return <span className="text-muted-foreground">—</span>
  // Docker reports a dual-stack publish as two bindings (0.0.0.0 and ::) that
  // read identically once formatted — show one chip per distinct mapping.
  const seen = new Set<string>()
  const parsedPorts = ports.map(parsePort).filter((p) => {
    if (seen.has(p.label)) return false
    seen.add(p.label)
    return true
  })
  return (
    <div className="flex flex-wrap gap-1">
      {parsedPorts.map((parsed) => {
        return parsed.href ? (
          <a
            key={parsed.key}
            href={parsed.href}
            target="_blank"
            rel="noopener noreferrer"
            className="rounded bg-muted px-1.5 py-0.5 font-mono text-[11px] text-info hover:underline"
          >
            {parsed.label}
          </a>
        ) : (
          <span key={parsed.key} className="rounded bg-muted px-1.5 py-0.5 font-mono text-[11px] text-muted-foreground">
            {parsed.label}
          </span>
        )
      })}
    </div>
  )
}

export function ContainerList({
  containers,
  stackId,
  latestMetrics,
  metricNames,
  onShowLogs,
  onOpenShell,
}: ContainerListProps) {
  const { query, setQuery, filtered } = useTextFilter(containers ?? [], CONTAINER_SEARCH_FIELDS)
  const queryClient = useQueryClient()

  const restartMutation = useMutation({
    mutationFn: (containerId: string) => resourcesApi.restartContainer(containerId),
    onSuccess: () => {
      toast.success('Container restarted')
      queryClient.invalidateQueries({ queryKey: queryKeys.stack.detail(stackId) })
      queryClient.invalidateQueries({ queryKey: queryKeys.stacks() })
    },
    onError: (err) => {
      toast.error(classifyError(err).message || 'Failed to restart container')
    },
  })

  const metricFor = (c: Container): ContainerMetric | undefined => {
    const byId = latestMetrics?.[c.id]
    if (byId) return byId
    // Stack containers and the metrics stream both use docker ids, but fall
    // back to a name match so a stale id (e.g. right after a recreate) still
    // finds its numbers.
    const byName = metricNames?.find((m) => m.name === c.name)
    return byName ? latestMetrics?.[byName.containerId] : undefined
  }

  const restartingId = restartMutation.isPending ? restartMutation.variables : null

  if (!containers || containers.length === 0) {
    return (
      <div className="flex items-center justify-center py-8 text-muted-foreground">
        Stack is stopped. Start it to see containers.
      </div>
    )
  }

  const rowActions = (container: Container) => (
    <div className="flex items-center justify-end gap-0.5">
      {onShowLogs && (
        <Button
          variant="ghost"
          size="icon"
          className="h-7 w-7"
          title={`Logs: ${container.name}`}
          aria-label={`Logs: ${container.name}`}
          onClick={() => onShowLogs(container.name)}
        >
          <ScrollText className="h-3.5 w-3.5" />
        </Button>
      )}
      {onOpenShell && (
        <Button
          variant="ghost"
          size="icon"
          className="h-7 w-7"
          title={`Shell: ${container.name}`}
          aria-label={`Shell: ${container.name}`}
          disabled={container.state !== 'running'}
          onClick={() => onOpenShell(container.id)}
        >
          <SquareTerminal className="h-3.5 w-3.5" />
        </Button>
      )}
      <Button
        variant="ghost"
        size="icon"
        className="h-7 w-7"
        title={`Restart: ${container.name}`}
        aria-label={`Restart: ${container.name}`}
        disabled={restartMutation.isPending}
        onClick={() => restartMutation.mutate(container.id)}
      >
        <RotateCw className={`h-3.5 w-3.5 ${restartingId === container.id ? 'animate-spin' : ''}`} />
      </Button>
    </div>
  )

  return (
    <div className="p-3">
      <div className="mb-3">
        <TableSearch
          value={query}
          onChange={setQuery}
          placeholder="Filter services…"
          className="w-full sm:w-56"
        />
        {query && filtered.length === 0 && (
          <p className="mt-2 text-sm text-muted-foreground">No services match your filter.</p>
        )}
      </div>

      <div className="hidden md:block">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Service</TableHead>
              <TableHead>Image</TableHead>
              <TableHead>State</TableHead>
              <TableHead>Health</TableHead>
              <TableHead className="text-right">CPU</TableHead>
              <TableHead className="text-right">Mem</TableHead>
              <TableHead>Ports</TableHead>
              <TableHead />
            </TableRow>
          </TableHeader>
          <TableBody>
            {filtered.map((container) => {
              const metric = metricFor(container)
              return (
                <TableRow key={container.id}>
                  <TableCell className="font-medium font-mono text-[13px]">
                    <div className="flex items-center gap-2">
                      <StatusDot tone={stateDotTone(container)} />
                      {container.name}
                    </div>
                  </TableCell>
                  <TableCell><ImageCell image={container.image} /></TableCell>
                  <TableCell><StateCell container={container} /></TableCell>
                  <TableCell>{getHealthBadge(container.health)}</TableCell>
                  <TableCell className="text-right font-mono text-xs tabular-nums">
                    {metric ? `${metric.cpuPercent.toFixed(1)}%` : '—'}
                  </TableCell>
                  <TableCell className="text-right font-mono text-xs tabular-nums">
                    {metric ? formatBytes(metric.memUsage) : '—'}
                  </TableCell>
                  <TableCell><PortChips ports={container.ports} /></TableCell>
                  <TableCell>{rowActions(container)}</TableCell>
                </TableRow>
              )
            })}
          </TableBody>
        </Table>
      </div>

      <div className="md:hidden space-y-4">
        {filtered.map((container) => {
          const metric = metricFor(container)
          return (
            <div key={container.id} className="rounded-lg border p-4 space-y-3">
              <div className="flex items-start justify-between gap-2">
                <div className="flex-1 min-w-0">
                  <h3 className="font-medium font-mono truncate">{container.name}</h3>
                  <p className="text-sm truncate"><ImageCell image={container.image} /></p>
                </div>
                <div className="flex items-center gap-2">
                  <StateCell container={container} />
                </div>
              </div>

              <div className="space-y-2">
                <div className="flex items-center gap-2">
                  <span className="text-sm text-muted-foreground">Status:</span>
                  <span className="text-sm">{container.status}</span>
                </div>
                <div className="flex items-center gap-2">
                  <span className="text-sm text-muted-foreground">Health:</span>
                  {getHealthBadge(container.health)}
                </div>
                {metric && (
                  <div className="flex items-center gap-4">
                    <span className="text-sm text-muted-foreground">
                      CPU: <span className="font-mono text-foreground">{metric.cpuPercent.toFixed(1)}%</span>
                    </span>
                    <span className="text-sm text-muted-foreground">
                      Mem: <span className="font-mono text-foreground">{formatBytes(metric.memUsage)}</span>
                    </span>
                  </div>
                )}
                {container.ports.length > 0 && (
                  <div className="flex items-start gap-2">
                    <span className="text-sm text-muted-foreground">Ports:</span>
                    <PortChips ports={container.ports} />
                  </div>
                )}
              </div>

              {rowActions(container)}
            </div>
          )
        })}
      </div>
    </div>
  )
}
