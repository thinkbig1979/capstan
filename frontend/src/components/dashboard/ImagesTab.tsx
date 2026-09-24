import { useState, useMemo } from 'react'
import { Link } from 'react-router'
import {
  useImages,
  useDeleteImage,
  useScheduledCleanupPreview,
  useDockerCleanupPolicy,
} from '@/hooks/useResources'
import { resourcesApi } from '@/lib/api'
import { Badge } from '@/components/ui/badge'
import { Skeleton } from '@/components/ui/skeleton'
import { EmptyState } from '@/components/EmptyState'
import { LoadFailedNotice } from '@/components/LoadFailedNotice'
import { RefreshFailedNotice } from '@/components/RefreshFailedNotice'
import { Button } from '@/components/ui/button'
import {
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
} from '@/components/ui/table'
import { ImageIcon, Trash2 } from 'lucide-react'
import { SortFilterBar } from '@/components/dashboard/SortFilterBar'
import { HelpHint } from '@/components/ui/help-hint'
import { PruneButton } from '@/components/dashboard/PruneButton'
import { TablePagination } from '@/components/dashboard/TablePagination'
import { usePagination } from '@/hooks/usePagination'
import { useConfirm } from '@/hooks/useConfirm'
import { useTextFilter } from '@/hooks/useTextFilter'
import type { DockerImage } from '@/types'
import { formatBytes, formatDate } from '@/lib/format'
import { queryKeys } from '@/lib/query-keys'

const PAGE_SIZE = 50

type SortKey = 'name' | 'size' | 'created' | 'containers'

const IMAGE_SEARCH_FIELDS = [
  (img: DockerImage) => img.repoTags.join(' '),
  (img: DockerImage) => img.id,
]

export function ImagesTab() {
  const { confirm, ConfirmComponent } = useConfirm()
  const { data: images, isLoading, isError, refetch } = useImages()
  const [sortBy, setSortBy] = useState<SortKey>('size')
  const [deletingId, setDeletingId] = useState<string | null>(null)

  const deleteMutation = useDeleteImage()
  const cleanupPreview = useScheduledCleanupPreview()
  const cleanupPolicy = useDockerCleanupPolicy()

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

  // agent-os-v824: a failed Docker read with nothing loaded is not an empty
  // host. Checked before the empty state, which would otherwise say "none".
  if (isError && !images) {
    return <LoadFailedNotice what="the image list" onRetry={() => void refetch()} />
  }

  // Error WITH data: a refetch failed over a list the server did send. Keep it
  // and say so (agent-os-wczm); a retained empty list counts too.
  const refreshNotice = isError && (
    <RefreshFailedNotice what="the image list" onRetry={() => void refetch()} />
  )

  if (!images || images.length === 0) {
    return (
      <div className="space-y-4">
        {refreshNotice}
        <EmptyState
          icon={<ImageIcon className="h-12 w-12 text-muted-foreground" />}
          title="No Images"
          description="No Docker images found on this host"
        />
      </div>
    )
  }

  return (
    <div className="space-y-4">
      {refreshNotice}
      <SortFilterBar
        sortOptions={[
          { key: 'name', label: 'Name' },
          { key: 'size', label: 'Size' },
          { key: 'created', label: 'Created' },
          { key: 'containers', label: 'Containers' },
        ]}
        sortValue={sortBy}
        onSortChange={(key) => setSortBy(key as SortKey)}
        help={
          <HelpHint label="Images" title="Images" side="bottom" align="start">
            <p>The read-only templates your containers run from.</p>
            <p>
              Untagged &apos;dangling&apos; images are leftovers from rebuilds and safe to remove.
              The &apos;all unused&apos; prune option also clears any image no container is using.
            </p>
          </HelpHint>
        }
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
            invalidateKeys={[
              queryKeys.resources.images(),
              queryKeys.dashboardStats(),
              queryKeys.resources.cleanupPreview(),
            ]}
          />
        }
        countDisplay={
          query
            ? `${sortedImages.length} of ${images.length} images`
            : `${images.length} images, ${formatBytes(images.reduce((sum, img) => sum + img.size, 0))} total`
        }
      />

      {/* One block, gated wholly on the preview having data. On a preview error
          nothing here renders — not the schedule sentence, not the link — even
          when the policy query succeeded: a schedule line with no figure beside
          it reads as "nothing to reclaim", and a fabricated "0 B" reads as a
          measurement. Both name the floor the SERVER echoed, never a local one.
          "created more than", not "unused for": the filter keys on creation
          time. */}
      {cleanupPreview.data && (
        <p data-testid="cleanup-reclaimable" className="text-sm text-muted-foreground">
          {cleanupPreview.data.candidates.length === 0
            ? `Nothing for scheduled cleanup to reclaim: no dangling image was created more than ${cleanupPreview.data.minAgeHours} hours ago.`
            : `Scheduled cleanup would reclaim ${formatBytes(cleanupPreview.data.reclaimableBytes)} from ${cleanupPreview.data.candidates.length} dangling image${
                cleanupPreview.data.candidates.length === 1 ? '' : 's'
              } created more than ${cleanupPreview.data.minAgeHours} hours ago.`}
          {cleanupPolicy.data && (
            <>
              {' '}
              {cleanupPolicy.data.enabled
                ? 'Scheduled cleanup is on.'
                : 'Scheduled cleanup is off.'}{' '}
              <Link
                to="/settings/docker-cleanup"
                className="text-primary hover:underline"
              >
                Cleanup settings
              </Link>
            </>
          )}
        </p>
      )}

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
