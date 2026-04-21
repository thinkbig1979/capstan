import { useEffect } from 'react'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { ContainerList } from './ContainerList'
import { ComposeEditor } from './ComposeEditor'
import { EnvEditor } from './EnvEditor'
import { TerminalComponent } from './Terminal'
import { LogViewer } from './LogViewer'
import { MetricsPanel } from './MetricsPanel'
import { OperationProgress } from './OperationProgress'
import { GitStatus as GitStatusComponent } from '../git/GitStatus'
import { Button } from '@/components/ui/button'
import { Separator } from '@/components/ui/separator'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { AutoUpdateToggle } from '@/components/dashboard/AutoUpdateToggle'
import { Download, Play, Square, RefreshCw, Info } from 'lucide-react'
import { useQueryClient } from '@tanstack/react-query'
import { useStreamingOperation } from '@/hooks/useStreamingOperation'
import { useAutoUpdatePolicies } from '@/hooks/useResources'
import { toast } from 'sonner'
import type { Stack, AutoUpdatePolicy } from '@/types'

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
      <div>
        <h3 className="mb-3 text-lg font-semibold">Containers</h3>
        <ContainerList
          containers={stack.containers || []}
        />
      </div>

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
        <Button
          variant="outline"
          size="sm"
          onClick={onPull}
          disabled={anyRunning}
        >
          <Download className={`mr-2 h-4 w-4 ${isPulling ? 'animate-spin' : ''}`} />
          {isPulling ? 'Pulling...' : 'Pull Images'}
        </Button>

        <Separator orientation="vertical" className="h-6 mx-1" />

        <div className="flex items-center gap-2">
          <span className="text-sm text-muted-foreground">Auto-Update</span>
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
      queryClient.invalidateQueries({ queryKey: ['stack', stack.id] })
      queryClient.invalidateQueries({ queryKey: ['stacks'] })
      toast.success(`${operation.action.charAt(0).toUpperCase() + operation.action.slice(1)} completed`)
    }
  }, [operation.status, operation.action, stack.id, queryClient])

  return (
    <div className="h-full flex flex-col gap-4">
      <GitStatusComponent stack={stack} />
      <Tabs value={activeTab} onValueChange={onTabChange} className="flex-1">
        <TabsList variant="line" className="w-full">
          <TabsTrigger value="overview">Overview</TabsTrigger>
          <TabsTrigger value="compose">Compose</TabsTrigger>
          <TabsTrigger value="environment">Environment</TabsTrigger>
          <TabsTrigger value="logs">Logs</TabsTrigger>
          <TabsTrigger value="terminal">Terminal</TabsTrigger>
          <TabsTrigger value="metrics">Metrics</TabsTrigger>
        </TabsList>

        <TabsContent value="overview" className="mt-4">
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
        </TabsContent>

        <TabsContent value="compose" className="mt-4">
          <ComposeEditor stackId={stack.id} />
        </TabsContent>

        <TabsContent value="environment" className="mt-4">
          <EnvEditor stackId={stack.id} />
        </TabsContent>

        <TabsContent value="logs" className="mt-4">
          <LogViewer stackId={stack.id} initialContainer={undefined} hasRunningContainers={stack.status !== 'stopped' && (stack.containers?.length ?? 0) > 0} />
        </TabsContent>

        <TabsContent value="terminal" className="mt-4">
          <TerminalComponent stack={stack} initialContainer={undefined} />
        </TabsContent>

        <TabsContent value="metrics" className="mt-4">
          <MetricsPanel stackId={stack.id} />
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
