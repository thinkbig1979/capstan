import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Terminal, FileText, Play, Square, AlertCircle, RefreshCw } from 'lucide-react'
import type { Container } from '@/types'
import { useCallback } from 'react'

interface ContainerListProps {
  containers: Container[]
  onContainerAction: (containerId: string, tab: 'logs' | 'terminal') => void
  onContainerNameAction?: (containerName: string, tab: 'logs' | 'terminal') => void
}

export function ContainerList({ containers, onContainerAction, onContainerNameAction }: ContainerListProps) {
  const handleTerminal = useCallback(
    (containerId: string, containerName: string) => {
      onContainerAction(containerId, 'terminal')
      onContainerNameAction?.(containerName, 'terminal')
    },
    [onContainerAction, onContainerNameAction],
  )

  const handleLogs = useCallback(
    (containerId: string, containerName: string) => {
      onContainerAction(containerId, 'logs')
      onContainerNameAction?.(containerName, 'logs')
    },
    [onContainerAction, onContainerNameAction],
  )

  if (!containers || containers.length === 0) {
    return (
      <div className="flex items-center justify-center py-8 text-muted-foreground">
        Stack is stopped. Start it to see containers.
      </div>
    )
  }

  const getHealthBadge = (health?: string) => {
    if (!health) return <Badge variant="outline">none</Badge>
    if (health === 'healthy') return <Badge className="bg-green-500 hover:bg-green-600 flex items-center gap-1"><Play className="h-3 w-3" aria-hidden="true" />Healthy</Badge>
    if (health === 'unhealthy') return <Badge className="bg-red-500 hover:bg-red-600 flex items-center gap-1"><AlertCircle className="h-3 w-3" aria-hidden="true" />Unhealthy</Badge>
    return <Badge variant="outline">{health}</Badge>
  }

  return (
    <>
      <div className="hidden md:block rounded-md border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Name</TableHead>
              <TableHead>Image</TableHead>
              <TableHead>State</TableHead>
              <TableHead>Status</TableHead>
              <TableHead>Ports</TableHead>
              <TableHead>Health</TableHead>
              <TableHead className="text-right">Actions</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {containers.map((container) => (
              <TableRow key={container.id}>
                <TableCell className="font-medium">{container.name}</TableCell>
                <TableCell className="text-sm text-muted-foreground">{container.image}</TableCell>
                <TableCell>
                  <div className="flex items-center gap-2">
                    {container.state === 'running' && <Play className="h-3 w-3 text-green-500" aria-hidden="true" />}
                    {container.state === 'exited' && <Square className="h-3 w-3 text-red-500" aria-hidden="true" />}
                    {container.state === 'dead' && <Square className="h-3 w-3 text-red-500" aria-hidden="true" />}
                    {container.state === 'restarting' && <RefreshCw className="h-3 w-3 text-yellow-500" aria-hidden="true" />}
                    <span className="capitalize">{container.state}</span>
                  </div>
                </TableCell>
                <TableCell className="text-sm">{container.status}</TableCell>
                <TableCell className="text-sm">
                  {container.ports.length > 0
                    ? container.ports.map((p, i) => (
                        <div key={i}>
                          {p.host}:{p.container}/{p.protocol}
                        </div>
                      ))
                    : '-'}
                </TableCell>
                <TableCell>{getHealthBadge(container.health)}</TableCell>
                <TableCell className="text-right">
                  <div className="flex justify-end gap-1">
                    <Button
                      variant="ghost"
                      size="icon"
                      onClick={() => handleTerminal(container.id, container.name)}
                      title="Open Terminal"
                      aria-label="Open terminal for {container.name}"
                    >
                      <Terminal className="h-4 w-4" />
                    </Button>
                    <Button
                      variant="ghost"
                      size="icon"
                      onClick={() => handleLogs(container.id, container.name)}
                      title="View Logs"
                      aria-label="View logs for {container.name}"
                    >
                      <FileText className="h-4 w-4" />
                    </Button>
                  </div>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>

      <div className="md:hidden space-y-4">
        {containers.map((container) => (
          <div key={container.id} className="rounded-lg border p-4 space-y-3">
            <div className="flex items-start justify-between gap-2">
              <div className="flex-1 min-w-0">
                <h3 className="font-medium truncate">{container.name}</h3>
                <p className="text-sm text-muted-foreground truncate">{container.image}</p>
              </div>
              <div className="flex items-center gap-2">
                {container.state === 'running' && <Play className="h-4 w-4 text-green-500" aria-hidden="true" />}
                {container.state === 'exited' && <Square className="h-4 w-4 text-red-500" aria-hidden="true" />}
                {container.state === 'dead' && <Square className="h-4 w-4 text-red-500" aria-hidden="true" />}
                {container.state === 'restarting' && <RefreshCw className="h-4 w-4 text-yellow-500" aria-hidden="true" />}
                <span className="text-sm capitalize">{container.state}</span>
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
              {container.ports.length > 0 && (
                <div className="flex items-start gap-2">
                  <span className="text-sm text-muted-foreground">Ports:</span>
                  <div className="flex flex-col gap-1 text-sm">
                    {container.ports.map((p, i) => (
                      <span key={i}>
                        {p.host}:{p.container}/{p.protocol}
                      </span>
                    ))}
                  </div>
                </div>
              )}
            </div>

            <div className="flex gap-2 pt-2 border-t">
              <Button
                variant="outline"
                size="icon"
                onClick={() => handleTerminal(container.id, container.name)}
                className="min-h-[44px] min-w-[44px] flex-1"
                aria-label="Open terminal"
              >
                <Terminal className="h-4 w-4" />
              </Button>
              <Button
                variant="outline"
                size="icon"
                onClick={() => handleLogs(container.id, container.name)}
                className="min-h-[44px] min-w-[44px] flex-1"
                aria-label="View logs"
              >
                <FileText className="h-4 w-4" />
              </Button>
            </div>
          </div>
        ))}
      </div>
    </>
  )
}
