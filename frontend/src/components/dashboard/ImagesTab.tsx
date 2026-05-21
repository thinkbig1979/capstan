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
import { ImageIcon, Trash2 } from 'lucide-react'
import { toast } from 'sonner'
import { classifyError } from '@/lib/error-handler'
import { SortFilterBar } from '@/components/dashboard/SortFilterBar'
import { PruneButton } from '@/components/dashboard/PruneButton'
import { useConfirm } from '@/components/ConfirmDialog'
import type { DockerImage } from '@/types'
import { formatBytes, formatDate } from '@/lib/format'

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
    onError: (err) => {
      toast.error(classifyError(err).message || 'Failed to remove image')
      setDeletingId(null)
    },
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
          <PruneButton
            resourceType="image"
            pruneFn={() => resourcesApi.pruneImages()}
            confirmMessage="Prune Unused Images?"
            confirmDescription="All images not referenced by any container will be permanently removed."
            invalidateKeys={[['resources', 'images'], ['dashboard-stats']]}
          />
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
                  <Button variant="ghost" size="icon" className="h-7 w-7 text-destructive hover:text-destructive" onClick={() => handleDelete(image)} disabled={deletingId === image.id || deleteMutation.isPending} title="Remove image" aria-label={`Remove image ${image.repoTags?.[0] ?? image.id}`}>
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
