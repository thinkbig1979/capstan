import { useState, useMemo } from 'react'
import { useVolumes, useDeleteVolume } from '@/hooks/useResources'
import { resourcesApi } from '@/lib/api'
import { Badge } from '@/components/ui/badge'
import { Skeleton } from '@/components/ui/skeleton'
import { EmptyState } from '@/components/EmptyState'
import { Button } from '@/components/ui/button'
import {
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
} from '@/components/ui/table'
import { HardDrive, Trash2 } from 'lucide-react'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'
import { SortFilterBar } from '@/components/dashboard/SortFilterBar'
import { HelpHint } from '@/components/ui/help-hint'
import { PruneButton } from '@/components/dashboard/PruneButton'
import { TablePagination } from '@/components/dashboard/TablePagination'
import { usePagination } from '@/hooks/usePagination'
import { useConfirm } from '@/hooks/useConfirm'
import { useTextFilter } from '@/hooks/useTextFilter'
import type { DockerVolume } from '@/types'
import { formatBytes } from '@/lib/format'
import { queryKeys } from '@/lib/query-keys'

const PAGE_SIZE = 50

type SortKey = 'name' | 'driver' | 'size' | 'stack'

const VOL_SEARCH_FIELDS = [
  (v: DockerVolume) => v.name,
  (v: DockerVolume) => v.driver,
  (v: DockerVolume) => v.stack,
]

export function VolumesTab() {
  const { confirm, ConfirmComponent } = useConfirm()
  const { data: volumes, isLoading } = useVolumes()
  const [sortBy, setSortBy] = useState<SortKey>('name')
  const [deletingName, setDeletingName] = useState<string | null>(null)

  const deleteMutation = useDeleteVolume()

  const { query, setQuery, filtered } = useTextFilter(volumes ?? [], VOL_SEARCH_FIELDS)

  const handleDelete = async (vol: DockerVolume) => {
    const confirmed = await confirm(
      `Remove Volume "${vol.name}"?`,
      'This volume and its data will be permanently removed. This cannot be undone.',
      { confirmText: 'Remove', isDangerous: true },
    )
    if (confirmed) {
      setDeletingName(vol.name)
      deleteMutation.mutate(
        { name: vol.name, force: false },
        { onSettled: () => setDeletingName(null) },
      )
    }
  }

  const sortedVolumes = useMemo(() => {
    const sorted = [...filtered]
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
  }, [filtered, sortBy])

  const { page, setPage, totalPages, pageItems } = usePagination(sortedVolumes, PAGE_SIZE)

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
      <EmptyState
        icon={<HardDrive className="h-12 w-12 text-muted-foreground" />}
        title="No Volumes"
        description="No Docker volumes found on this host"
      />
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
        help={
          <HelpHint label="Volumes" title="Volumes" side="bottom" align="start">
            <p>Where containers store data meant to outlive them, like databases.</p>
            <p>
              A volume listed with no container attached is unused. Removing one deletes
              its data, and that can&apos;t be undone.
            </p>
          </HelpHint>
        }
        searchValue={query}
        onSearchChange={setQuery}
        searchPlaceholder="Filter volumes…"
        actions={
          <PruneButton
            resourceType="volume"
            pruneFn={(opts) => resourcesApi.pruneVolumes(opts)}
            options={{ all: { label: 'Remove all unused volumes, not just anonymous' } }}
            confirmMessage="Prune Unused Volumes?"
            confirmDescription="By default only anonymous volumes are removed. Enable 'all unused' to remove every volume not used by a container."
            invalidateKeys={[queryKeys.resources.volumes(), queryKeys.dashboardStats()]}
          />
        }
        countDisplay={
          query
            ? `${sortedVolumes.length} of ${volumes.length} volumes`
            : `${volumes.length} volumes`
        }
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
              <TableHead className="sticky right-0 z-20 bg-background shadow-[-8px_0_8px_-8px_rgba(0,0,0,0.25)]">Actions</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {pageItems.map((vol: DockerVolume) => (
              <TableRow key={vol.name}>
                <TableCell>
                  <span className="font-mono text-sm">{vol.name}</span>
                </TableCell>
                <TableCell>
                  <Badge variant="outline">{vol.driver}</Badge>
                </TableCell>
                <TableCell>
                  {vol.sizeKnown ? (
                    <span className="text-sm">{formatBytes(vol.size)}</span>
                  ) : (
                    <span className="text-sm text-muted-foreground" title="Size not calculated">—</span>
                  )}
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
                <TableCell className="sticky right-0 bg-background shadow-[-8px_0_8px_-8px_rgba(0,0,0,0.25)]">
                  {vol.inUse ? (
                    <TooltipProvider>
                      <Tooltip>
                        <TooltipTrigger asChild>
                          <span tabIndex={0} className="inline-flex">
                            <Button variant="ghost" size="icon" className="h-8 w-8 text-muted-foreground" disabled aria-label={`Remove volume ${vol.name} (in use, cannot be removed)`}>
                              <Trash2 className="h-3.5 w-3.5" />
                            </Button>
                          </span>
                        </TooltipTrigger>
                        <TooltipContent>In use by a container, stop it first to remove this volume</TooltipContent>
                      </Tooltip>
                    </TooltipProvider>
                  ) : (
                    <Button variant="ghost" size="icon" className="h-8 w-8 text-destructive hover:text-destructive" onClick={() => handleDelete(vol)} disabled={deletingName === vol.name || deleteMutation.isPending} title="Remove volume" aria-label={`Remove volume ${vol.name}`}>
                      <Trash2 className="h-3.5 w-3.5" />
                    </Button>
                  )}
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
        total={sortedVolumes.length}
        onPageChange={setPage}
        label="volumes"
      />
      <ConfirmComponent />
    </div>
  )
}
