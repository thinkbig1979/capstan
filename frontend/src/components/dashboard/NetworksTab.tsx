import { useState, useMemo } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useNetworks } from '@/hooks/useResources'
import { resourcesApi } from '@/lib/api'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { Button } from '@/components/ui/button'
import {
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
} from '@/components/ui/table'
import { Network, Trash2, Scissors } from 'lucide-react'
import { toast } from 'sonner'
import { SortFilterBar } from '@/components/dashboard/SortFilterBar'
import { useConfirm } from '@/components/ConfirmDialog'
import type { DockerNetwork } from '@/types'

type SortKey = 'name' | 'driver' | 'scope' | 'stack' | 'containers'

export function NetworksTab() {
  const queryClient = useQueryClient()
  const { confirm, ConfirmComponent } = useConfirm()
  const { data: networks, isLoading } = useNetworks()
  const [sortBy, setSortBy] = useState<SortKey>('name')
  const [deletingId, setDeletingId] = useState<string | null>(null)

  const deleteMutation = useMutation({
    mutationFn: (id: string) => resourcesApi.deleteNetwork(id),
    onSuccess: () => {
      toast.success('Network removed')
      setDeletingId(null)
      queryClient.invalidateQueries({ queryKey: ['resources', 'networks'] })
    },
    onError: () => {
      toast.error('Failed to remove network')
      setDeletingId(null)
    },
  })

  const pruneMutation = useMutation({
    mutationFn: () => resourcesApi.pruneNetworks(),
    onSuccess: (data) => {
      const count = data.deleted?.length || 0
      toast.success(`Pruned ${count} network${count !== 1 ? 's' : ''}`)
      queryClient.invalidateQueries({ queryKey: ['resources', 'networks'] })
    },
    onError: () => toast.error('Failed to prune networks'),
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

  const handlePrune = async () => {
    const confirmed = await confirm(
      'Prune Unused Networks?',
      'All networks not referenced by any container will be permanently removed. This cannot be undone.',
      { confirmText: 'Prune', isDangerous: true },
    )
    if (confirmed) pruneMutation.mutate()
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
      <Card>
        <CardContent className="flex flex-col items-center justify-center py-12">
          <Network className="h-12 w-12 text-muted-foreground mb-4" />
          <p className="text-lg font-semibold">No Networks</p>
          <p className="text-sm text-muted-foreground">
            No Docker networks found on this host
          </p>
        </CardContent>
      </Card>
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
          <Button variant="outline" size="sm" className="h-7 text-xs" onClick={handlePrune} disabled={pruneMutation.isPending} title="Remove all unused networks">
            <Scissors className="mr-1 h-3 w-3" />
            Prune
          </Button>
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
              <TableHead>Actions</TableHead>
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
                <TableCell>
                  <Button variant="ghost" size="icon" className="h-7 w-7 text-destructive hover:text-destructive" onClick={() => handleDelete(net)} disabled={deletingId === net.id || deleteMutation.isPending} title="Remove network">
                    <Trash2 className="h-3.5 w-3.5" />
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
      <ConfirmComponent />
    </div>
  )
}
