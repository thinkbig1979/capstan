import { useState, useMemo } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useImages } from '@/hooks/useResources'
import { resourcesApi } from '@/lib/api'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { Button } from '@/components/ui/button'
import {
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
} from '@/components/ui/table'
import { ImageIcon, Trash2, Scissors } from 'lucide-react'
import { toast } from 'sonner'
import { SortFilterBar } from '@/components/dashboard/SortFilterBar'
import { useConfirm } from '@/components/ConfirmDialog'
import type { DockerImage } from '@/types'

function formatBytes(bytes: number): string {
  if (bytes === 0) return '0 B'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return `${(bytes / Math.pow(k, i)).toFixed(1)} ${sizes[i]}`
}

function formatDate(epoch: number): string {
  if (!epoch) return '-'
  const date = new Date(epoch * 1000)
  return date.toLocaleDateString(undefined, {
    month: 'short',
    day: 'numeric',
    year: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  })
}

type SortKey = 'name' | 'size' | 'created' | 'containers'

export function ImagesTab() {
  const queryClient = useQueryClient()
  const { confirm, ConfirmComponent } = useConfirm()
  const { data: images, isLoading } = useImages()
  const [sortBy, setSortBy] = useState<SortKey>('size')
  const [deletingId, setDeletingId] = useState<string | null>(null)

  const deleteMutation = useMutation({
    mutationFn: ({ id, force }: { id: string; force: boolean }) => resourcesApi.deleteImage(id, force),
    onSuccess: () => {
      toast.success('Image removed')
      setDeletingId(null)
      queryClient.invalidateQueries({ queryKey: ['resources', 'images'] })
      queryClient.invalidateQueries({ queryKey: ['dashboard-stats'] })
    },
    onError: () => {
      toast.error('Failed to remove image')
      setDeletingId(null)
    },
  })

  const pruneMutation = useMutation({
    mutationFn: () => resourcesApi.pruneImages(),
    onSuccess: (data) => {
      const count = data.deleted?.length || 0
      const space = data.spaceReclaimed ? formatBytes(data.spaceReclaimed) : '0 B'
      toast.success(`Pruned ${count} image${count !== 1 ? 's' : ''}, ${space} reclaimed`)
      queryClient.invalidateQueries({ queryKey: ['resources', 'images'] })
      queryClient.invalidateQueries({ queryKey: ['dashboard-stats'] })
    },
    onError: () => toast.error('Failed to prune images'),
  })

  const handleDelete = async (image: DockerImage) => {
    const tag = image.repoTags[0] || image.id.substring(0, 19)
    const hasContainers = image.containers > 0
    const confirmed = await confirm(
      `Remove Image "${tag}"?`,
      hasContainers
        ? 'This image is used by containers and will be force-removed. This cannot be undone.'
        : 'This image will be removed. This cannot be undone.',
      { confirmText: 'Remove', isDangerous: true },
    )
    if (confirmed) {
      setDeletingId(image.id)
      deleteMutation.mutate({ id: image.id, force: hasContainers })
    }
  }

  const handlePrune = async () => {
    const confirmed = await confirm(
      'Prune Unused Images?',
      'All images not referenced by any container will be permanently removed. This cannot be undone.',
      { confirmText: 'Prune', isDangerous: true },
    )
    if (confirmed) pruneMutation.mutate()
  }

  const sortedImages = useMemo(() => {
    if (!images) return []
    const sorted = [...images]
    switch (sortBy) {
      case 'name':
        return sorted.sort((a, b) => (a.repoTags[0] || '').localeCompare(b.repoTags[0] || ''))
      case 'size':
        return sorted.sort((a, b) => b.size - a.size)
      case 'created':
        return sorted.sort((a, b) => b.created - a.created)
      case 'containers':
        return sorted.sort((a, b) => b.containers - a.containers)
      default:
        return sorted
    }
  }, [images, sortBy])

  if (isLoading) {
    return (
      <div className="space-y-4">
        {Array.from({ length: 5 }).map((_, i) => (
          <Skeleton key={i} className="h-12 w-full" />
        ))}
      </div>
    )
  }

  if (!images || images.length === 0) {
    return (
      <Card>
        <CardContent className="flex flex-col items-center justify-center py-12">
          <ImageIcon className="h-12 w-12 text-muted-foreground mb-4" />
          <p className="text-lg font-semibold">No Images</p>
          <p className="text-sm text-muted-foreground">
            No Docker images found on this host
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
          { key: 'size', label: 'Size' },
          { key: 'created', label: 'Created' },
          { key: 'containers', label: 'Containers' },
        ]}
        sortValue={sortBy}
        onSortChange={(key) => setSortBy(key as SortKey)}
        actions={
          <Button variant="outline" size="sm" className="h-7 text-xs" onClick={handlePrune} disabled={pruneMutation.isPending} title="Remove all unused images">
            <Scissors className="mr-1 h-3 w-3" />
            Prune
          </Button>
        }
        countDisplay={`${images.length} images, ${formatBytes(images.reduce((sum, img) => sum + img.size, 0))} total`}
      />

      <div className="rounded-md border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Repository Tags</TableHead>
              <TableHead>Image ID</TableHead>
              <TableHead>Size</TableHead>
              <TableHead>Created</TableHead>
              <TableHead className="text-center">Containers</TableHead>
              <TableHead>Actions</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {sortedImages.map((image: DockerImage) => (
              <TableRow key={image.id}>
                <TableCell>
                  <div className="flex flex-wrap gap-1">
                    {image.repoTags.map((tag) => (
                      <Badge key={tag} variant="secondary" className="text-xs font-mono">
                        {tag}
                      </Badge>
                    ))}
                  </div>
                </TableCell>
                <TableCell>
                  <span className="text-xs font-mono text-muted-foreground">
                    {image.id.replace('sha256:', '').substring(0, 19)}
                  </span>
                </TableCell>
                <TableCell>
                  <span className="text-sm">{formatBytes(image.size)}</span>
                </TableCell>
                <TableCell>
                  <span className="text-sm text-muted-foreground">{formatDate(image.created)}</span>
                </TableCell>
                <TableCell className="text-center">
                  {image.containers > 0 ? (
                    <Badge variant="outline">{image.containers}</Badge>
                  ) : (
                    <span className="text-sm text-muted-foreground">0</span>
                  )}
                </TableCell>
                <TableCell>
                  <Button variant="ghost" size="icon" className="h-7 w-7 text-destructive hover:text-destructive" onClick={() => handleDelete(image)} disabled={deletingId === image.id || deleteMutation.isPending} title="Remove image">
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
