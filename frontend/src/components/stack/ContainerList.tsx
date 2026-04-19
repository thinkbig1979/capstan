import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Badge } from '@/components/ui/badge'
import { Play, Square, AlertCircle, RefreshCw } from 'lucide-react'
import type { Container } from '@/types'

interface ContainerListProps {
  containers: Container[]
}

export function ContainerList({ containers }: ContainerListProps) {

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

  const formatPort = (p: { host: string; container: string; protocol: string }) => {
    const containerPart = p.container.replace(/\/(tcp|udp|sctp)$/i, '')
    return `${p.host}:${containerPart}/${p.protocol}`
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
                    ? container.ports.map((p) => (
                        <div key={`${p.host}-${p.container}`}>
                          {formatPort(p)}
                        </div>
                      ))
                    : '-'}
                </TableCell>
                <TableCell>{getHealthBadge(container.health)}</TableCell>
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
                    {container.ports.map((p) => (
                      <span key={`${p.host}-${p.container}`}>
                        {formatPort(p)}
                      </span>
                    ))}
                  </div>
                </div>
              )}
            </div>
          </div>
        ))}
      </div>
    </>
  )
}
