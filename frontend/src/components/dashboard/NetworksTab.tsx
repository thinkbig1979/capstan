import { useState, useMemo } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useNetworks } from '@/hooks/useResources'
import { resourcesApi } from '@/lib/api'
import { Badge } from '@/components/ui/badge'
import { EmptyState } from '@/components/EmptyState'
import { Skeleton } from '@/components/ui/skeleton'
import { Button } from '@/components/ui/button'
import {
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
} from '@/components/ui/table'
import { Network, Plus, Trash2 } from 'lucide-react'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'
import { toast } from 'sonner'
import { classifyError } from '@/lib/error-handler'
import { SortFilterBar } from '@/components/dashboard/SortFilterBar'
import { PruneButton } from '@/components/dashboard/PruneButton'
import { CreateNetworkDialog } from '@/components/dashboard/CreateNetworkDialog'
import { useConfirm } from '@/components/ConfirmDialog'
import type { DockerNetwork } from '@/types'

type SortKey = 'name' | 'driver' | 'scope' | 'stack' | 'containers'

// Docker's predefined networks cannot be removed, and a network with attached endpoints fails
// removal until those containers are gone. Returns the reason delete is unavailable, or null.
const SYSTEM_NETWORKS = new Set(['bridge', 'host', 'none'])
function networkDeleteBlock(net: DockerNetwork): string | null {
  if (SYSTEM_NETWORKS.has(net.name)) return "System network can't be removed"
  if (net.containers > 0) {
    return `In use by ${net.containers} container${net.containers === 1 ? '' : 's'}`
  }
  return null
}

export function NetworksTab() {
  const queryClient = useQueryClient()
  const { confirm, ConfirmComponent } = useConfirm()
  const { data: networks, isLoading } = useNetworks()
  const [sortBy, setSortBy] = useState<SortKey>('name')
  const [deletingId, setDeletingId] = useState<string | null>(null)
  const [createOpen, setCreateOpen] = useState(false)

  const deleteMutation = useMutation({
    mutationFn: (id: string) => resourcesApi.deleteNetwork(id),
    onSuccess: () => {
      toast.success('Network removed')
      setDeletingId(null)
      queryClient.invalidateQueries({ queryKey: ['resources', 'networks'] })
    },
    onError: (err) => {
      toast.error(classifyError(err).message || 'Failed to remove network')
      setDeletingId(null)
    },
  })

  const handleDelete = async (net: DockerNetwork) => {
    const confirmed = await confirm(
      `Remove Network "${net.name}"?`,
      'This network will be permanently removed. This cannot be undone.',
      { confirmText: 'Remove', isDangerous: true },
    )
    if (confirmed) {
      setDeletingId(net.id)
      deleteMutation.mutate(net.id)
    }
  }

  const sortedNetworks = useMemo(() => {
    if (!networks) return []
    const sorted = [...networks]
    switch (sortBy) {
      case 'name':
        return sorted.sort((a, b) => a.name.localeCompare(b.name))
      case 'driver':
        return sorted.sort((a, b) => a.driver.localeCompare(b.driver))
      case 'scope':
        return sorted.sort((a, b) => a.scope.localeCompare(b.scope))
      case 'stack':
        return sorted.sort((a, b) => (a.stack || '').localeCompare(b.stack || ''))
      case 'containers':
        return sorted.sort((a, b) => b.containers - a.containers)
      default:
        return sorted
    }
  }, [networks, sortBy])

  if (isLoading) {
    return (
      <div className="space-y-4">
        {Array.from({ length: 5 }).map((_, i) => (
          <Skeleton key={i} className="h-12 w-full" />
        ))}
      </div>
    )
  }

  if (!networks || networks.length === 0) {
    return (
      <>
        <EmptyState
          icon={<Network className="h-12 w-12 text-muted-foreground" />}
          title="No Networks"
          description="No Docker networks found on this host"
          action={
            <Button onClick={() => setCreateOpen(true)}>
              <Plus className="mr-2 h-4 w-4" />
              Create Network
            </Button>
          }
        />
        <CreateNetworkDialog open={createOpen} onOpenChange={setCreateOpen} />
      </>
    )
  }

  return (
    <div className="space-y-4">
      <SortFilterBar
        sortOptions={[
          { key: 'name', label: 'Name' },
          { key: 'driver', label: 'Driver' },
          { key: 'scope', label: 'Scope' },
          { key: 'stack', label: 'Stack' },
          { key: 'containers', label: 'Containers' },
        ]}
        sortValue={sortBy}
        onSortChange={(key) => setSortBy(key as SortKey)}
        actions={
          <>
            <Button size="sm" onClick={() => setCreateOpen(true)}>
              <Plus className="mr-2 h-4 w-4" />
              Create
            </Button>
            <PruneButton
              resourceType="network"
              pruneFn={() => resourcesApi.pruneNetworks()}
              confirmMessage="Prune Unused Networks?"
              confirmDescription="All networks not referenced by any container will be permanently removed."
              invalidateKeys={[['resources', 'networks']]}
            />
          </>
        }
        countDisplay={`${networks.length} networks`}
      />

      <div className="rounded-md border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Name</TableHead>
              <TableHead>ID</TableHead>
              <TableHead>Driver</TableHead>
              <TableHead>Scope</TableHead>
              <TableHead>Internal</TableHead>
              <TableHead className="text-center">Containers</TableHead>
              <TableHead>Stack</TableHead>
              <TableHead className="sticky right-0 z-20 bg-background shadow-[-8px_0_8px_-8px_rgba(0,0,0,0.25)]">Actions</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {sortedNetworks.map((net: DockerNetwork) => (
              <TableRow key={net.id}>
                <TableCell>
                  <span className="font-medium text-sm">{net.name}</span>
                </TableCell>
                <TableCell>
                  <span className="text-xs font-mono text-muted-foreground">
                    {net.id.substring(0, 19)}
                  </span>
                </TableCell>
                <TableCell>
                  <Badge variant="outline">{net.driver}</Badge>
                </TableCell>
                <TableCell>
                  <Badge variant="secondary" className="text-xs">{net.scope}</Badge>
                </TableCell>
                <TableCell>
                  {net.internal ? (
                    <Badge variant="secondary" className="text-xs">Yes</Badge>
                  ) : (
                    <span className="text-sm text-muted-foreground">No</span>
                  )}
                </TableCell>
                <TableCell className="text-center">
                  {net.containers > 0 ? (
                    <Badge variant="outline">{net.containers}</Badge>
                  ) : (
                    <span className="text-sm text-muted-foreground">0</span>
                  )}
                </TableCell>
                <TableCell>
                  {net.stack ? (
                    <Badge variant="secondary">{net.stack}</Badge>
                  ) : (
                    <span className="text-sm text-muted-foreground">-</span>
                  )}
                </TableCell>
                <TableCell className="sticky right-0 bg-background shadow-[-8px_0_8px_-8px_rgba(0,0,0,0.25)]">
                  {(() => {
                    const blocked = networkDeleteBlock(net)
                    if (blocked) {
                      return (
                        <TooltipProvider>
                          <Tooltip>
                            <TooltipTrigger asChild>
                              <span tabIndex={0} className="inline-flex">
                                <Button variant="ghost" size="icon" className="h-8 w-8 text-muted-foreground" disabled aria-label={`Remove network ${net.name} (${blocked})`}>
                                  <Trash2 className="h-3.5 w-3.5" />
                                </Button>
                              </span>
                            </TooltipTrigger>
                            <TooltipContent>{blocked}</TooltipContent>
                          </Tooltip>
                        </TooltipProvider>
                      )
                    }
                    return (
                      <Button variant="ghost" size="icon" className="h-8 w-8 text-destructive hover:text-destructive" onClick={() => handleDelete(net)} disabled={deletingId === net.id || deleteMutation.isPending} title="Remove network" aria-label={`Remove network ${net.name}`}>
                        <Trash2 className="h-3.5 w-3.5" />
                      </Button>
                    )
                  })()}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
      <ConfirmComponent />
      <CreateNetworkDialog open={createOpen} onOpenChange={setCreateOpen} />
    </div>
  )
}
