import { useState, useMemo } from 'react'
import { useImages, useDeleteImage } from '@/hooks/useResources'
import { resourcesApi } from '@/lib/api'
import { Badge } from '@/components/ui/badge'
import { Skeleton } from '@/components/ui/skeleton'
import { EmptyState } from '@/components/EmptyState'
import { Button } from '@/components/ui/button'
import {
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
} from '@/components/ui/table'
import { ImageIcon, Trash2 } from 'lucide-react'
import { SortFilterBar } from '@/components/dashboard/SortFilterBar'
import { PruneButton } from '@/components/dashboard/PruneButton'
import { TablePagination, usePagination } from '@/components/dashboard/TablePagination'
import { useConfirm } from '@/components/ConfirmDialog'
import { useTextFilter } from '@/hooks/useTextFilter'
import type { DockerImage } from '@/types'
import { formatBytes, formatDate } from '@/lib/format'

const PAGE_SIZE = 50

type SortKey = 'name' | 'size' | 'created' | 'containers'

const IMAGE_SEARCH_FIELDS = [
  (img: DockerImage) => img.repoTags.join(' '),
  (img: DockerImage) => img.id,
]

export function ImagesTab() {
  const { confirm, ConfirmComponent } = useConfirm()
  const { data: images, isLoading } = useImages()
  const [sortBy, setSortBy] = useState<SortKey>('size')
  const [deletingId, setDeletingId] = useState<string | null>(null)

  const deleteMutation = useDeleteImage()

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
      deleteMutation.mutate(
        { id: image.id, force: hasContainers },
        { onSettled: () => setDeletingId(null) },
      )
    }
  }

  const { query, setQuery, filtered } = useTextFilter(images ?? [], IMAGE_SEARCH_FIELDS)

  const sortedImages = useMemo(() => {
    const sorted = [...filtered]
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
  }, [filtered, sortBy])

  const { page, setPage, totalPages, pageItems } = usePagination(sortedImages, PAGE_SIZE)

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
      <EmptyState
        icon={<ImageIcon className="h-12 w-12 text-muted-foreground" />}
        title="No Images"
        description="No Docker images found on this host"
      />
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
        searchValue={query}
        onSearchChange={setQuery}
        searchPlaceholder="Filter images…"
        actions={
          <PruneButton
            resourceType="image"
            pruneFn={(opts) => resourcesApi.pruneImages(opts)}
            options={{ all: { label: 'Remove all unused images, not just dangling' }, until: true }}
            confirmMessage="Prune Unused Images?"
            confirmDescription="By default only dangling (untagged) images are removed. Enable 'all unused' to remove every image not used by a container."
            invalidateKeys={[['resources', 'images'], ['dashboard-stats']]}
          />
        }
        countDisplay={
          query
            ? `${sortedImages.length} of ${images.length} images`
            : `${images.length} images, ${formatBytes(images.reduce((sum, img) => sum + img.size, 0))} total`
        }
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
              <TableHead className="sticky right-0 z-20 bg-background shadow-[-8px_0_8px_-8px_rgba(0,0,0,0.25)]">Actions</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {pageItems.map((image: DockerImage) => (
              <TableRow key={image.id}>
                <TableCell>
                  <div className="flex flex-wrap gap-1">
                    {image.repoTags.length > 0 ? (
                      image.repoTags.map((tag) => (
                        <Badge key={tag} variant="secondary" className="text-xs font-mono">
                          {tag}
                        </Badge>
                      ))
                    ) : (
                      <Badge variant="outline" className="text-xs font-mono text-muted-foreground" title="Untagged (dangling) image">
                        &lt;none&gt;:&lt;none&gt;
                      </Badge>
                    )}
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
                <TableCell className="sticky right-0 bg-background shadow-[-8px_0_8px_-8px_rgba(0,0,0,0.25)]">
                  <Button variant="ghost" size="icon" className="h-8 w-8 text-destructive hover:text-destructive" onClick={() => handleDelete(image)} disabled={deletingId === image.id || deleteMutation.isPending} title="Remove image" aria-label={`Remove image ${image.repoTags?.[0] ?? image.id}`}>
                    <Trash2 className="h-3.5 w-3.5" />
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
      <TablePagination
        page={page}
        totalPages={totalPages}
        pageSize={PAGE_SIZE}
        total={sortedImages.length}
        onPageChange={setPage}
        label="images"
      />
      <ConfirmComponent />
    </div>
  )
}
