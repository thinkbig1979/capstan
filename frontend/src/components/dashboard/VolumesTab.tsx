import { useState, useMemo } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useVolumes } from '@/hooks/useResources'
import { resourcesApi } from '@/lib/api'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { Button } from '@/components/ui/button'
import {
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
} from '@/components/ui/table'
import { HardDrive, Trash2, Scissors } from 'lucide-react'
import { toast } from 'sonner'
import { SortFilterBar } from '@/components/dashboard/SortFilterBar'
import { useConfirm } from '@/components/ConfirmDialog'
import type { DockerVolume } from '@/types'

function formatBytes(bytes: number): string {
  if (bytes <= 0) return '-'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return `${(bytes / Math.pow(k, i)).toFixed(1)} ${sizes[i]}`
}

type SortKey = 'name' | 'driver' | 'size' | 'stack'

export function VolumesTab() {
  const queryClient = useQueryClient()
  const { confirm, ConfirmComponent } = useConfirm()
  const { data: volumes, isLoading } = useVolumes()
  const [sortBy, setSortBy] = useState<SortKey>('name')
  const [deletingName, setDeletingName] = useState<string | null>(null)

  const deleteMutation = useMutation({
    mutationFn: ({ name, force }: { name: string; force: boolean }) => resourcesApi.deleteVolume(name, force),
    onSuccess: () => {
      toast.success('Volume removed')
      setDeletingName(null)
      queryClient.invalidateQueries({ queryKey: ['resources', 'volumes'] })
    },
    onError: () => {
      toast.error('Failed to remove volume')
      setDeletingName(null)
    },
  })

  const pruneMutation = useMutation({
    mutationFn: () => resourcesApi.pruneVolumes(),
    onSuccess: (data) => {
      const count = data.deleted?.length || 0
      const space = data.spaceReclaimed ? formatBytes(data.spaceReclaimed) : '0 B'
      toast.success(`Pruned ${count} volume${count !== 1 ? 's' : ''}, ${space} reclaimed`)
      queryClient.invalidateQueries({ queryKey: ['resources', 'volumes'] })
    },
    onError: () => toast.error('Failed to prune volumes'),
  })

  const handleDelete = async (vol: DockerVolume) => {
    const confirmed = await confirm(
      `Remove Volume "${vol.name}"?`,
      'This volume and its data will be permanently removed. This cannot be undone.',
      { confirmText: 'Remove', isDangerous: true },
    )
    if (confirmed) {
      setDeletingName(vol.name)
      deleteMutation.mutate({ name: vol.name, force: false })
    }
  }

  const handlePrune = async () => {
    const confirmed = await confirm(
      'Prune Unused Volumes?',
      'All volumes not referenced by any container will be permanently removed. This cannot be undone.',
      { confirmText: 'Prune', isDangerous: true },
    )
    if (confirmed) pruneMutation.mutate()
  }

  const sortedVolumes = useMemo(() => {
    if (!volumes) return []
    const sorted = [...volumes]
    switch (sortBy) {
      case 'name':
        return sorted.sort((a, b) => a.name.localeCompare(b.name))
      case 'driver':
        return sorted.sort((a, b) => a.driver.localeCompare(b.driver))
      case 'size':
        return sorted.sort((a, b) => b.size - a.size)
      case 'stack':
        return sorted.sort((a, b) => (a.stack || '').localeCompare(b.stack || ''))
      default:
        return sorted
    }
  }, [volumes, sortBy])

  if (isLoading) {
    return (
      <div className="space-y-4">
        {Array.from({ length: 5 }).map((_, i) => (
          <Skeleton key={i} className="h-12 w-full" />
        ))}
      </div>
    )
  }

  if (!volumes || volumes.length === 0) {
    return (
      <Card>
        <CardContent className="flex flex-col items-center justify-center py-12">
          <HardDrive className="h-12 w-12 text-muted-foreground mb-4" />
          <p className="text-lg font-semibold">No Volumes</p>
          <p className="text-sm text-muted-foreground">
            No Docker volumes found on this host
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
          { key: 'size', label: 'Size' },
          { key: 'stack', label: 'Stack' },
        ]}
        sortValue={sortBy}
        onSortChange={(key) => setSortBy(key as SortKey)}
        actions={
          <Button variant="outline" size="sm" className="h-7 text-xs" onClick={handlePrune} disabled={pruneMutation.isPending} title="Remove all unused volumes">
            <Scissors className="mr-1 h-3 w-3" />
            Prune
          </Button>
        }
        countDisplay={`${volumes.length} volumes`}
      />

      <div className="rounded-md border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Name</TableHead>
              <TableHead>Driver</TableHead>
              <TableHead>Size</TableHead>
              <TableHead>Stack</TableHead>
              <TableHead className="hidden lg:table-cell">Mountpoint</TableHead>
              <TableHead>Actions</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {sortedVolumes.map((vol: DockerVolume) => (
              <TableRow key={vol.name}>
                <TableCell>
                  <span className="font-mono text-sm">{vol.name}</span>
                </TableCell>
                <TableCell>
                  <Badge variant="outline">{vol.driver}</Badge>
                </TableCell>
                <TableCell>
                  <span className="text-sm">{formatBytes(vol.size)}</span>
                </TableCell>
                <TableCell>
                  {vol.stack ? (
                    <Badge variant="secondary">{vol.stack}</Badge>
                  ) : (
                    <span className="text-sm text-muted-foreground">-</span>
                  )}
                </TableCell>
                <TableCell className="hidden lg:table-cell">
                  <span className="text-xs font-mono text-muted-foreground truncate block max-w-[300px]" title={vol.mountpoint}>
                    {vol.mountpoint}
                  </span>
                </TableCell>
                <TableCell>
                  <Button variant="ghost" size="icon" className="h-7 w-7 text-destructive hover:text-destructive" onClick={() => handleDelete(vol)} disabled={deletingName === vol.name || deleteMutation.isPending} title="Remove volume">
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
