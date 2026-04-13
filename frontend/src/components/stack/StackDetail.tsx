import { useState, useCallback } from 'react'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { ContainerList } from './ContainerList'
import { ComposeEditor } from './ComposeEditor'
import { EnvEditor } from './EnvEditor'
import { TerminalComponent } from './Terminal'
import { LogViewer } from './LogViewer'
import { MetricsPanel } from './MetricsPanel'
import { GitStatus as GitStatusComponent } from '../git/GitStatus'
import { Button } from '@/components/ui/button'
import { Play, Square, RefreshCw } from 'lucide-react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { stacksApi } from '@/lib/api'
import { toast } from 'sonner'
import type { Stack } from '@/types'

interface StackDetailProps {
  stack: Stack
  activeTab: string
  onTabChange: (tab: string) => void
  onContainerAction: (containerId: string, tab: 'logs' | 'terminal') => void
}

function OverviewTabContent({
  stack,
  onContainerAction,
  onContainerNameAction,
  onStart,
  onStop,
  onRestart,
  isStarting,
  isStopping,
  isRestarting,
}: {
  stack: Stack
  onContainerAction: (containerId: string, tab: 'logs' | 'terminal') => void
  onContainerNameAction?: (containerName: string, _tab: 'logs' | 'terminal') => void
  onStart: () => void
  onStop: () => void
  onRestart: () => void
  isStarting: boolean
  isStopping: boolean
  isRestarting: boolean
}) {
  const canStart = stack.status === 'stopped' || stack.status === 'partial'
  const canStop = stack.status === 'running'

  return (
    <div className="space-y-6">
      <div>
        <h3 className="mb-3 text-lg font-semibold">Containers</h3>
        <ContainerList
          containers={stack.containers || []}
          onContainerAction={onContainerAction}
          onContainerNameAction={onContainerNameAction}
        />
      </div>

      <div className="flex gap-2">
        <Button
          variant="outline"
          size="sm"
          onClick={onStart}
          disabled={!canStart || isStarting || isRestarting}
        >
          <Play className="mr-2 h-4 w-4" />
          {isStarting ? 'Starting...' : 'Start'}
        </Button>
        <Button
          variant="outline"
          size="sm"
          onClick={onStop}
          disabled={!canStop || isStopping || isRestarting}
        >
          <Square className="mr-2 h-4 w-4" />
          {isStopping ? 'Stopping...' : 'Stop'}
        </Button>
        <Button
          variant="outline"
          size="sm"
          onClick={onRestart}
          disabled={!canStop || isRestarting}
        >
          <RefreshCw className={`mr-2 h-4 w-4 ${isRestarting ? 'animate-spin' : ''}`} />
          {isRestarting ? 'Restarting...' : 'Restart'}
        </Button>
      </div>
    </div>
  )
}

export function StackDetail({ stack, activeTab, onTabChange, onContainerAction }: StackDetailProps) {
  const [selectedContainer, setSelectedContainer] = useState<string | undefined>()
  const queryClient = useQueryClient()

  const startMutation = useMutation({
    mutationFn: () => stacksApi.start(stack.id),
    onSuccess: () => {
      toast.success('Stack started successfully')
      queryClient.invalidateQueries({ queryKey: ['stack', stack.id] })
      queryClient.invalidateQueries({ queryKey: ['stacks'] })
    },
    onError: () => {
      toast.error('Failed to start stack')
    },
  })

  const stopMutation = useMutation({
    mutationFn: () => stacksApi.stop(stack.id),
    onSuccess: () => {
      toast.success('Stack stopped successfully')
      queryClient.invalidateQueries({ queryKey: ['stack', stack.id] })
      queryClient.invalidateQueries({ queryKey: ['stacks'] })
    },
    onError: () => {
      toast.error('Failed to stop stack')
    },
  })

  const restartMutation = useMutation({
    mutationFn: () => stacksApi.restart(stack.id),
    onSuccess: () => {
      toast.success('Stack restarted successfully')
      queryClient.invalidateQueries({ queryKey: ['stack', stack.id] })
      queryClient.invalidateQueries({ queryKey: ['stacks'] })
    },
    onError: () => {
      toast.error('Failed to restart stack')
    },
  })

  const handleContainerAction = useCallback(
    (containerId: string, tab: 'logs' | 'terminal') => {
      onContainerAction(containerId, tab)
    },
    [onContainerAction],
  )

  const handleContainerNameAction = useCallback(
    (containerName: string, _tab: 'logs' | 'terminal') => {
      setSelectedContainer(containerName)
    },
    [],
  )

  return (
    <div className="h-full flex flex-col">
      {stack.isGitRepo && <GitStatusComponent directoryPath={stack.directory} />}
      <Tabs value={activeTab} onValueChange={onTabChange} className="flex-1">
        <TabsList className="grid w-full grid-cols-3 sm:grid-cols-6">
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
            onContainerAction={handleContainerAction}
            onContainerNameAction={handleContainerNameAction}
            onStart={() => startMutation.mutate()}
            onStop={() => stopMutation.mutate()}
            onRestart={() => restartMutation.mutate()}
            isStarting={startMutation.isPending}
            isStopping={stopMutation.isPending}
            isRestarting={restartMutation.isPending}
          />
        </TabsContent>

        <TabsContent value="compose" className="mt-4">
          <ComposeEditor stackId={stack.id} />
        </TabsContent>

        <TabsContent value="environment" className="mt-4">
          <EnvEditor stackId={stack.id} />
        </TabsContent>

        <TabsContent value="logs" className="mt-4">
          <LogViewer stackId={stack.id} initialContainer={selectedContainer} />
        </TabsContent>

        <TabsContent value="terminal" className="mt-4">
          <TerminalComponent stack={stack} initialContainer={selectedContainer} />
        </TabsContent>

        <TabsContent value="metrics" className="mt-4">
          <MetricsPanel stackId={stack.id} />
        </TabsContent>
      </Tabs>
    </div>
  )
}
