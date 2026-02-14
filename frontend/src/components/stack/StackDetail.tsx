import { useState, useCallback } from 'react'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { ContainerList } from './ContainerList'
import { ComposeEditor } from './ComposeEditor'
import { EnvEditor } from './EnvEditor'
import { Terminal } from './Terminal'
import { LogViewer } from './LogViewer'
import { MetricsPanel } from './MetricsPanel'
import { GitStatus as GitStatusComponent } from '../git/GitStatus'
import { Button } from '@/components/ui/button'
import { Play, Square, RefreshCw } from 'lucide-react'
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
}: {
  stack: Stack
  onContainerAction: (containerId: string, tab: 'logs' | 'terminal') => void
  onContainerNameAction?: (containerName: string, tab: 'logs' | 'terminal') => void
}) {
  return (
    <div className="space-y-6">
      {stack.isGitRepo && <GitStatusComponent directoryPath={stack.directory} />}

      <div>
        <h3 className="mb-3 text-lg font-semibold">Containers</h3>
        <ContainerList
          containers={stack.containers || []}
          onContainerAction={onContainerAction}
          onContainerNameAction={onContainerNameAction}
        />
      </div>

      <div className="flex gap-2">
        <Button variant="outline" size="sm">
          <Play className="mr-2 h-4 w-4" />
          Start
        </Button>
        <Button variant="outline" size="sm">
          <Square className="mr-2 h-4 w-4" />
          Stop
        </Button>
        <Button variant="outline" size="sm">
          <RefreshCw className="mr-2 h-4 w-4" />
          Restart
        </Button>
      </div>
    </div>
  )
}

export function StackDetail({ stack, activeTab, onTabChange, onContainerAction }: StackDetailProps) {
  const [selectedContainer, setSelectedContainer] = useState<string | undefined>()

  const handleContainerAction = useCallback(
    (containerId: string, tab: 'logs' | 'terminal') => {
      onContainerAction(containerId, tab)
    },
    [onContainerAction],
  )

  const handleContainerNameAction = useCallback(
    (containerName: string, tab: 'logs' | 'terminal') => {
      if (tab === 'logs') {
        setSelectedContainer(containerName)
        onTabChange('logs')
      }
    },
    [onTabChange],
  )
  return (
    <Tabs value={activeTab} onValueChange={onTabChange} className="h-full">
      <TabsList className="grid w-full grid-cols-6">
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
        <Terminal stack={stack} initialContainer={selectedContainer} />
      </TabsContent>

      <TabsContent value="metrics" className="mt-4">
        <MetricsPanel stackId={stack.id} />
      </TabsContent>
    </Tabs>
  )
}
