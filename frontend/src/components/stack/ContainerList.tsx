import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Terminal, FileText } from 'lucide-react'
import type { Container } from '@/types'
import { useCallback } from 'react'

interface ContainerListProps {
  containers: Container[]
  onContainerAction: (containerId: string, tab: 'logs' | 'terminal') => void
  onContainerNameAction?: (containerName: string, tab: 'logs' | 'terminal') => void
}

export function ContainerList({ containers, onContainerAction, onContainerNameAction }: ContainerListProps) {
  const handleTerminal = useCallback(
    (containerId: string) => {
      onContainerAction(containerId, 'terminal')
    },
    [onContainerAction],
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

  const getStatusColor = (state: string) => {
    if (state === 'running') return 'bg-green-500'
    if (state === 'exited' || state === 'dead') return 'bg-red-500'
    if (state === 'restarting') return 'bg-yellow-500'
    return 'bg-gray-500'
  }

  const getHealthBadge = (health?: string) => {
    if (!health) return <Badge variant="outline">none</Badge>
    if (health === 'healthy') return <Badge className="bg-green-500 hover:bg-green-600">healthy</Badge>
    if (health === 'unhealthy') return <Badge className="bg-red-500 hover:bg-red-600">unhealthy</Badge>
    return <Badge variant="outline">{health}</Badge>
  }

  return (
    <div className="rounded-md border">
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
                  <span className={`h-2 w-2 rounded-full ${getStatusColor(container.state)}`} />
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
                    onClick={() => handleTerminal(container.id)}
                    title="Open Terminal"
                  >
                    <Terminal className="h-4 w-4" />
                  </Button>
                  <Button
                    variant="ghost"
                    size="icon"
                    onClick={() => handleLogs(container.id, container.name)}
                    title="View Logs"
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
  )
}
