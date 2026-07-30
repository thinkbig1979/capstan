import { useEffect, Suspense, lazy } from 'react'
import { Tabs, TabsContent } from '@/components/ui/tabs'
import { ResponsiveTabsList } from '@/components/ui/responsive-tabs-list'
import { ContainerList } from './ContainerList'
import { EnvEditor } from './EnvEditor'
import { ComposeEnvSplit } from './ComposeEnvSplit'
import { TerminalComponent } from './Terminal'
import { LogViewer } from './LogViewer'
import { StackUpdatesTab } from './StackUpdatesTab'
import { BackupsTab } from './BackupsTab'
import { OperationProgress } from './OperationProgress'
import { GitStatus as GitStatusComponent } from '../git/GitStatus'
import { GitHistory } from '../git/GitHistory'
import { Button } from '@/components/ui/button'
import { Separator } from '@/components/ui/separator'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { HelpHint } from '@/components/ui/help-hint'
import { AutoUpdateToggle } from '@/components/dashboard/AutoUpdateToggle'
import { BackupToggle } from '@/components/dashboard/BackupToggle'
import { TabErrorBoundary } from '@/components/TabErrorBoundary'
import { Download, Play, Square, RefreshCw, Info } from 'lucide-react'
import { useQueryClient } from '@tanstack/react-query'
import { useStreamingOperation } from '@/hooks/useStreamingOperation'
import { useAutoUpdatePolicies } from '@/hooks/useResources'
import { toast } from 'sonner'
import { EditorSkeleton, MetricsSkeleton } from '@/components/LoadingSkeleton'
import type { Stack, AutoUpdatePolicy } from '@/types'
import { queryKeys } from '@/lib/query-keys'

// Lazy: both pull in a heavy vendor bundle (codemirror / recharts) that most
// stack detail visits don't need — only the Compose and Metrics tabs do.
const ComposeEditor = lazy(() =>
  import('./ComposeEditor').then((m) => ({ default: m.ComposeEditor })),
)
const MetricsPanel = lazy(() =>
  import('./MetricsPanel').then((m) => ({ default: m.MetricsPanel })),
)

interface StackDetailProps {
  stack: Stack
  activeTab: string
  onTabChange: (tab: string) => void
}

function OverviewTabContent({
  stack,
  onStart,
  onStop,
  onRestart,
  onPull,
  isStarting,
  isStopping,
  isRestarting,
  isPulling,
}: {
  stack: Stack
  onStart: () => void
  onStop: () => void
  onRestart: () => void
  onPull: () => void
  isStarting: boolean
  isStopping: boolean
  isRestarting: boolean
  isPulling: boolean
}) {
  const { data: policiesData } = useAutoUpdatePolicies()

  const stackPolicy: AutoUpdatePolicy | undefined = policiesData?.policies?.find(
    (p) => p.targetType === 'stack' && p.targetId === stack.id,
  )
  const hasContainerPolicies = policiesData?.policies?.some(
    (p) => p.targetType === 'container' && stack.containers?.some((c) => c.id === p.targetId),
  )

  const canStart = stack.status === 'stopped' || stack.status === 'partial'
  const canStop = stack.status === 'running'
  const anyRunning = isStarting || isStopping || isRestarting || isPulling

  return (
    <div className="space-y-6">
      {/* Action bar sits directly under the tabs so Start is the obvious next step on a
          stopped stack, rather than being orphaned below the container list. */}
      <div className="flex flex-wrap items-center gap-2">
        <Button
          variant="outline"
          size="sm"
          onClick={onStart}
          disabled={!canStart || anyRunning}
        >
          <Play className="mr-2 h-4 w-4" />
          {isStarting ? 'Starting...' : 'Start'}
        </Button>
        <Button
          variant="outline"
          size="sm"
          onClick={onStop}
          disabled={!canStop || anyRunning}
        >
          <Square className="mr-2 h-4 w-4" />
          {isStopping ? 'Stopping...' : 'Stop'}
        </Button>
        <Button
          variant="outline"
          size="sm"
          onClick={onRestart}
          disabled={!canStop || anyRunning}
        >
          <RefreshCw className={`mr-2 h-4 w-4 ${isRestarting ? 'animate-spin' : ''}`} />
          {isRestarting ? 'Restarting...' : 'Restart'}
        </Button>
        <div className="flex items-center gap-1">
          <Button
            variant="outline"
            size="sm"
            onClick={onPull}
            disabled={anyRunning}
          >
            <Download className={`mr-2 h-4 w-4 ${isPulling ? 'animate-spin' : ''}`} />
            {isPulling ? 'Pulling...' : 'Pull Images'}
          </Button>
          <HelpHint label="Pull images" title="Pull images">
            <p>Downloads the latest image for each service without restarting anything.</p>
            <p>
              Running containers keep using the old image until you restart or recreate them,
              so this just stages the update.
            </p>
          </HelpHint>
        </div>

        <Separator orientation="vertical" className="h-6 mx-1" />

        <div className="flex items-center gap-2">
          <span className="text-sm text-muted-foreground">Auto-Update</span>
          <HelpHint label="Auto-update" title="Auto-update">
            <p>Updates this stack on its own whenever a scan finds a newer image.</p>
            <p>
              The global auto-update switch in Settings has to be on first. Updating recreates
              containers, so expect a brief interruption.
            </p>
          </HelpHint>
          <AutoUpdateToggle
            targetType="stack"
            targetId={stack.id}
            enabled={stackPolicy?.enabled ?? false}
            paused={stackPolicy?.paused ?? false}
            consecutiveFailures={stackPolicy?.consecutiveFailures ?? 0}
            globalDisabled={!policiesData?.globalEnabled}
          />
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

        <Separator orientation="vertical" className="h-6 mx-1" />

        <div className="flex items-center gap-2">
          <span className="text-sm text-muted-foreground">Backup</span>
          <HelpHint label="Backup" title="Backup">
            <p>Adds this stack to scheduled backups, covering its volumes and compose files.</p>
            <p>Set up the repository and schedule under Settings, Backup.</p>
          </HelpHint>
          <BackupToggle stackId={stack.id} />
        </div>
      </div>

      <div>
        <h3 className="mb-3 text-lg font-semibold">Containers</h3>
        <ContainerList
          containers={stack.containers || []}
        />
      </div>
    </div>
  )
}

export function StackDetail({ stack, activeTab, onTabChange }: StackDetailProps) {
  const queryClient = useQueryClient()
  const operation = useStreamingOperation()

  const isStarting = operation.status === 'running' && operation.action === 'start'
  const isStopping = operation.status === 'running' && operation.action === 'stop'
  const isRestarting = operation.status === 'running' && operation.action === 'restart'
  const isPulling = operation.status === 'running' && operation.action === 'pull'

  useEffect(() => {
    if (operation.status === 'success') {
      queryClient.invalidateQueries({ queryKey: queryKeys.stack.detail(stack.id) })
      queryClient.invalidateQueries({ queryKey: queryKeys.stacks() })
      toast.success(`${operation.action.charAt(0).toUpperCase() + operation.action.slice(1)} completed`)
    }
  }, [operation.status, operation.action, stack.id, queryClient])

  return (
    <div className="h-full flex flex-col gap-4">
      <GitStatusComponent stack={stack} />
      <Tabs value={activeTab} onValueChange={onTabChange} className="flex-1">
        <ResponsiveTabsList
          value={activeTab}
          onValueChange={onTabChange}
          variant="line"
          tabs={[
            { value: 'overview', label: 'Overview' },
            { value: 'history', label: 'History' },
            { value: 'compose', label: 'Compose' },
            { value: 'environment', label: 'Environment' },
            { value: 'split', label: 'Compose + Env' },
            { value: 'logs', label: 'Logs' },
            { value: 'terminal', label: 'Terminal' },
            { value: 'metrics', label: 'Metrics' },
            { value: 'updates', label: 'Updates' },
            { value: 'backups', label: 'Backups' },
          ]}
        />

        <TabsContent value="overview" className="mt-4">
          <TabErrorBoundary>
            <OverviewTabContent
              stack={stack}
              onStart={() => operation.execute(stack.id, 'start')}
              onStop={() => operation.execute(stack.id, 'stop')}
              onRestart={() => operation.execute(stack.id, 'restart')}
              onPull={() => operation.execute(stack.id, 'pull')}
              isStarting={isStarting}
              isStopping={isStopping}
              isRestarting={isRestarting}
              isPulling={isPulling}
            />
          </TabErrorBoundary>
        </TabsContent>

        <TabsContent value="history" className="mt-4">
          <TabErrorBoundary>
            <GitHistory stackId={stack.id} />
          </TabErrorBoundary>
        </TabsContent>

        <TabsContent value="compose" className="mt-4">
          <TabErrorBoundary>
            <Suspense fallback={<EditorSkeleton />}>
              <ComposeEditor stackId={stack.id} />
            </Suspense>
          </TabErrorBoundary>
        </TabsContent>

        <TabsContent value="environment" className="mt-4">
          <TabErrorBoundary>
            <EnvEditor stackId={stack.id} />
          </TabErrorBoundary>
        </TabsContent>

        <TabsContent value="split" className="mt-4">
          <ComposeEnvSplit stackId={stack.id} />
        </TabsContent>

        <TabsContent value="logs" className="mt-4">
          <TabErrorBoundary>
            <LogViewer stackId={stack.id} initialContainer={undefined} hasRunningContainers={stack.status !== 'stopped' && (stack.containers?.length ?? 0) > 0} />
          </TabErrorBoundary>
        </TabsContent>

        <TabsContent value="terminal" className="mt-4">
          <TabErrorBoundary>
            <TerminalComponent stack={stack} initialContainer={undefined} />
          </TabErrorBoundary>
        </TabsContent>

        <TabsContent value="metrics" className="mt-4">
          <TabErrorBoundary>
            <Suspense fallback={<MetricsSkeleton />}>
              <MetricsPanel stackId={stack.id} />
            </Suspense>
          </TabErrorBoundary>
        </TabsContent>

        <TabsContent value="updates" className="mt-4">
          <TabErrorBoundary>
            <StackUpdatesTab stackId={stack.id} />
          </TabErrorBoundary>
        </TabsContent>

        <TabsContent value="backups" className="mt-4">
          <TabErrorBoundary>
            <BackupsTab stackId={stack.id} />
          </TabErrorBoundary>
        </TabsContent>
      </Tabs>

      <OperationProgress
        status={operation.status}
        lines={operation.lines}
        action={operation.action}
        error={operation.error}
        onDismiss={operation.reset}
      />
    </div>
  )
}
