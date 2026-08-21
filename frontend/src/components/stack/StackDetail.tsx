import { Suspense, lazy } from 'react'
import { Tabs, TabsContent } from '@/components/ui/tabs'
import { ResponsiveTabsList } from '@/components/ui/responsive-tabs-list'
import { ContainerList } from './ContainerList'
import { ComposeEnvSplit } from './ComposeEnvSplit'
import { TerminalComponent } from './Terminal'
import { LogViewer } from './LogViewer'
import { ActivityTab } from './ActivityTab'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { HelpHint } from '@/components/ui/help-hint'
import { AutoUpdateToggle } from '@/components/dashboard/AutoUpdateToggle'
import { BackupToggle } from '@/components/dashboard/BackupToggle'
import { TabErrorBoundary } from '@/components/TabErrorBoundary'
import { Info } from 'lucide-react'
import { useSearchParams } from 'react-router'
import { useAutoUpdatePolicies } from '@/hooks/useResources'
import { useMetricsBase } from '@/hooks/useMetricsBase'
import { MetricsSkeleton } from '@/components/LoadingSkeleton'
import type { Stack, AutoUpdatePolicy } from '@/types'

// Lazy: pulls in recharts, which most stack detail visits don't need — only
// the Metrics tab does. (The Editor tab's codemirror is lazy inside
// ComposeEnvSplit.)
const MetricsPanel = lazy(() =>
  import('./MetricsPanel').then((m) => ({ default: m.MetricsPanel })),
)

interface StackDetailProps {
  stack: Stack
  activeTab: string
  onTabChange: (tab: string) => void
}

function KvRow({ k, children }: { k: string; children: React.ReactNode }) {
  return (
    <div className="flex items-center justify-between gap-3 border-b border-border/60 py-2 text-sm last:border-b-0">
      <span className="text-muted-foreground">{k}</span>
      <span className="min-w-0 truncate text-right font-mono text-xs" title={typeof children === 'string' ? children : undefined}>
        {children}
      </span>
    </div>
  )
}

function OverviewTabContent({
  stack,
  onTabChange,
}: {
  stack: Stack
  onTabChange: (tab: string) => void
}) {
  const { data: policiesData } = useAutoUpdatePolicies()

  const stackPolicy: AutoUpdatePolicy | undefined = policiesData?.policies?.find(
    (p) => p.targetType === 'stack' && p.targetId === stack.id,
  )
  const hasContainerPolicies = policiesData?.policies?.some(
    (p) => p.targetType === 'container' && stack.containers?.some((c) => c.id === p.targetId),
  )

  // Per-stack metrics stream — the same socket path the Metrics tab uses, but
  // only one of the two tabs is ever mounted, so at most one socket is open.
  const { latestMetrics, containers: metricContainers } = useMetricsBase(`/ws/metrics/${stack.id}`)

  return (
    <div className="grid items-start gap-4 min-[980px]:grid-cols-[minmax(0,1fr)_280px]">
      <div className="min-w-0 overflow-hidden rounded-lg border bg-card">
        <div className="border-b px-4 py-2.5 text-[11px] font-bold uppercase tracking-[0.14em] text-muted-foreground">
          Services
        </div>
        <ContainerList
          containers={stack.containers || []}
          stackId={stack.id}
          latestMetrics={latestMetrics}
          metricNames={metricContainers}
          onShowLogs={(name) => onTabChange(`logs?container=${encodeURIComponent(name)}`)}
          onOpenShell={(containerId) => onTabChange(`terminal?container=${encodeURIComponent(containerId)}`)}
        />
      </div>

      <div className="flex flex-col gap-4">
        <div className="overflow-hidden rounded-lg border bg-card">
          <div className="border-b px-4 py-2.5 text-[11px] font-bold uppercase tracking-[0.14em] text-muted-foreground">
            Stack
          </div>
          <div className="px-4 pb-1 pt-1">
            <KvRow k="Compose file">{stack.composeFile}</KvRow>
            <KvRow k="Env file">{stack.envFile || '—'}</KvRow>
            <KvRow k="Services">{String(stack.containers?.length ?? 0)}</KvRow>
            {stack.isGitRepo && <KvRow k="Branch">{stack.gitBranch || '—'}</KvRow>}
          </div>
          <div className="flex items-center justify-between gap-2 border-t px-4 py-2.5">
            <div className="flex items-center gap-1.5 text-sm">
              Auto-update
              <HelpHint label="Auto-update" title="Auto-update">
                <p>Updates this stack on its own whenever a scan finds a newer image.</p>
                <p>
                  The global auto-update switch in Settings has to be on first. Updating recreates
                  containers, so expect a brief interruption.
                </p>
              </HelpHint>
              {hasContainerPolicies && (
                <Tooltip>
                  <TooltipTrigger asChild>
                    <Info className="h-3.5 w-3.5 text-muted-foreground cursor-help" />
                  </TooltipTrigger>
                  <TooltipContent>
                    <p>Individual container settings override this stack-level toggle</p>
                  </TooltipContent>
                </Tooltip>
              )}
            </div>
            <AutoUpdateToggle
              targetType="stack"
              targetId={stack.id}
              enabled={stackPolicy?.enabled ?? false}
              paused={stackPolicy?.paused ?? false}
              consecutiveFailures={stackPolicy?.consecutiveFailures ?? 0}
              globalDisabled={!policiesData?.globalEnabled}
            />
          </div>
          <div className="flex items-center justify-between gap-2 border-t px-4 py-2.5">
            <div className="flex items-center gap-1.5 text-sm">
              Backup
              <HelpHint
                label="Backup"
                title="Backup"
                href="https://github.com/thinkbig1979/capstan/blob/main/docs/how-to/configure-backups.md"
              >
                <p>Adds this stack to scheduled backups, covering its volumes and compose files.</p>
                <p>Set up the repository and schedule under Settings, Backup.</p>
              </HelpHint>
            </div>
            <BackupToggle stackId={stack.id} />
          </div>
        </div>
      </div>
    </div>
  )
}

export function StackDetail({ stack, activeTab, onTabChange }: StackDetailProps) {
  // Service-row deep links (Logs / Shell) carry the target container in
  // ?container=; the tab content picks it up as its initial selection.
  const [searchParams] = useSearchParams()
  const containerParam = searchParams.get('container') ?? undefined

  return (
    <div className="h-full flex flex-col gap-4">
      <Tabs value={activeTab} onValueChange={onTabChange} className="flex-1">
        <ResponsiveTabsList
          value={activeTab}
          onValueChange={onTabChange}
          variant="line"
          tabs={[
            { value: 'overview', label: 'Overview' },
            { value: 'editor', label: 'Editor' },
            { value: 'logs', label: 'Logs' },
            { value: 'terminal', label: 'Terminal' },
            { value: 'metrics', label: 'Metrics' },
            { value: 'activity', label: 'Activity' },
          ]}
        />

        <TabsContent value="overview" className="mt-4">
          <TabErrorBoundary>
            <OverviewTabContent stack={stack} onTabChange={onTabChange} />
          </TabErrorBoundary>
        </TabsContent>

        <TabsContent value="editor" className="mt-4">
          {/* Compose + Environment as one split view; the standalone Compose
              and Environment tabs merged into it. */}
          <ComposeEnvSplit stackId={stack.id} />
        </TabsContent>

        <TabsContent value="logs" className="mt-4">
          <TabErrorBoundary>
            <LogViewer stackId={stack.id} initialContainer={containerParam} hasRunningContainers={stack.status !== 'stopped' && (stack.containers?.length ?? 0) > 0} />
          </TabErrorBoundary>
        </TabsContent>

        <TabsContent value="terminal" className="mt-4">
          <TabErrorBoundary>
            <TerminalComponent stack={stack} initialContainer={containerParam} />
          </TabErrorBoundary>
        </TabsContent>

        <TabsContent value="metrics" className="mt-4">
          <TabErrorBoundary>
            <Suspense fallback={<MetricsSkeleton />}>
              <MetricsPanel stackId={stack.id} />
            </Suspense>
          </TabErrorBoundary>
        </TabsContent>

        <TabsContent value="activity" className="mt-4">
          <TabErrorBoundary>
            <ActivityTab stackId={stack.id} />
          </TabErrorBoundary>
        </TabsContent>
      </Tabs>
    </div>
  )
}
