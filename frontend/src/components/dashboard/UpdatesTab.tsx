import { useState, useMemo } from 'react'
import { useCheckUpdates, useUpdateContainer } from '@/hooks/useResources'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
} from '@/components/ui/table'
import { RefreshCw, Download, ArrowUpDown } from 'lucide-react'
import { toast } from 'sonner'
import { SortFilterBar } from '@/components/dashboard/SortFilterBar'
import { StatusBadge } from '@/components/dashboard/StatusBadge'
import type { ContainerUpdateInfo } from '@/types'

type SortKey = 'name' | 'image' | 'state' | 'stack'

export function UpdatesTab() {
  const { data: updates, isLoading, isError, refetch } = useCheckUpdates()
  const updateMutation = useUpdateContainer()
  const [sortBy, setSortBy] = useState<SortKey>('name')
  const [updatingId, setUpdatingId] = useState<string | null>(null)

  const handleCheck = () => {
    refetch()
  }

  const handleUpdate = (container: ContainerUpdateInfo) => {
    setUpdatingId(container.containerId)
    updateMutation.mutate(container.containerId, {
      onSuccess: () => {
        const action = container.state === 'running' ? 'updated and restarted' : 'updated'
        toast.success(`${container.containerName} ${action}`)
        setUpdatingId(null)
      },
      onError: () => {
        toast.error(`Failed to update ${container.containerName}`)
        setUpdatingId(null)
      },
    })
  }

  const sortedUpdates = useMemo(() => {
    if (!updates) return []
    const sorted = [...updates]
    switch (sortBy) {
      case 'name':
        return sorted.sort((a, b) => a.containerName.localeCompare(b.containerName))
      case 'image':
        return sorted.sort((a, b) => a.imageRef.localeCompare(b.imageRef))
      case 'state':
        return sorted.sort((a, b) => a.state.localeCompare(b.state))
      case 'stack':
        return sorted.sort((a, b) => (a.projectName || '').localeCompare(b.projectName || ''))
      default:
        return sorted
    }
  }, [updates, sortBy])

  const hasData = updates !== undefined

  if (!hasData && !isLoading) {
    return (
      <Card>
        <CardContent className="flex flex-col items-center justify-center py-12">
          <ArrowUpDown className="h-12 w-12 text-muted-foreground mb-4" />
          <p className="text-lg font-semibold mb-2">Check for Container Updates</p>
          <p className="text-sm text-muted-foreground mb-4">
            Check remote registries for newer image versions and find containers running outdated images
          </p>
          <Button onClick={handleCheck} disabled={isLoading}>
            <Download className="mr-2 h-4 w-4" />
            Check for Updates
          </Button>
        </CardContent>
      </Card>
    )
  }

  if (isLoading) {
    return (
      <div className="space-y-4">
        <Card>
          <CardContent className="flex flex-col items-center justify-center py-12">
            <RefreshCw className="h-8 w-8 text-muted-foreground mb-4 animate-spin" />
            <p className="text-lg font-semibold mb-1">Checking for Updates</p>
            <p className="text-sm text-muted-foreground">
              Checking remote registries for newer image versions...
            </p>
          </CardContent>
        </Card>
        <div className="space-y-2">
          {Array.from({ length: 3 }).map((_, i) => (
            <Skeleton key={i} className="h-12 w-full" />
          ))}
        </div>
      </div>
    )
  }

  if (isError) {
    return (
      <Card>
        <CardContent className="flex flex-col items-center justify-center py-12">
          <p className="text-lg font-semibold mb-2">Failed to Check for Updates</p>
          <p className="text-sm text-muted-foreground mb-4">
            An error occurred while checking for container image updates
          </p>
          <Button onClick={handleCheck}>
            <Download className="mr-2 h-4 w-4" />
            Retry
          </Button>
        </CardContent>
      </Card>
    )
  }

  if (!updates || updates.length === 0) {
    return (
      <Card>
        <CardContent className="flex flex-col items-center justify-center py-12">
          <ArrowUpDown className="h-12 w-12 text-muted-foreground mb-4" />
          <p className="text-lg font-semibold mb-2">All Containers Up to Date</p>
          <p className="text-sm text-muted-foreground mb-4">
            No image updates available for any container
          </p>
          <Button variant="outline" onClick={handleCheck}>
            <Download className="mr-2 h-4 w-4" />
            Check Again
          </Button>
        </CardContent>
      </Card>
    )
  }

  return (
    <div className="space-y-4">
      <SortFilterBar
        sortOptions={[
          { key: 'name', label: 'Name' },
          { key: 'image', label: 'Image' },
          { key: 'state', label: 'State' },
          { key: 'stack', label: 'Stack' },
        ]}
        sortValue={sortBy}
        onSortChange={(key) => setSortBy(key as SortKey)}
        actions={
          <Button variant="outline" size="sm" className="h-7 text-xs" onClick={handleCheck} disabled={isLoading}>
            <Download className="mr-1 h-3 w-3" />
            Check Again
          </Button>
        }
        countDisplay={`${updates!.length} update${updates!.length !== 1 ? 's' : ''} available`}
      />

      <div className="rounded-md border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Container</TableHead>
              <TableHead>Image</TableHead>
              <TableHead>Stack</TableHead>
              <TableHead>State</TableHead>
              <TableHead>Actions</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {sortedUpdates.map((container: ContainerUpdateInfo) => (
              <TableRow key={container.containerId}>
                <TableCell>
                  <div className="flex flex-col">
                    <span className="font-medium text-sm">{container.containerName}</span>
                    {container.isCompose && container.serviceName && (
                      <span className="text-xs text-muted-foreground">service: {container.serviceName}</span>
                    )}
                  </div>
                </TableCell>
                <TableCell>
                  <Badge variant="secondary" className="text-xs font-mono">
                    {container.imageRef}
                  </Badge>
                </TableCell>
                <TableCell>
                  {container.stackId ? (
                    <a
                      href={`/stacks/${container.stackId}`}
                      className="text-sm text-blue-500 hover:underline"
                    >
                      {container.projectName}
                    </a>
                  ) : container.projectName ? (
                    <span className="text-sm text-muted-foreground">{container.projectName}</span>
                  ) : (
                    <span className="text-xs text-muted-foreground italic">standalone</span>
                  )}
                </TableCell>
                <TableCell>
                  <StatusBadge status={container.state === 'running' ? 'running' : 'stopped'} />
                </TableCell>
                <TableCell>
                  <Button
                    variant="default"
                    size="sm"
                    className="h-7 text-xs"
                    onClick={() => handleUpdate(container)}
                    disabled={updatingId === container.containerId || updateMutation.isPending}
                  >
                    {updatingId === container.containerId ? (
                      <>
                        <RefreshCw className="mr-1 h-3 w-3 animate-spin" />
                        Updating...
                      </>
                    ) : (
                      <>
                        <Download className="mr-1 h-3 w-3" />
                        {container.state === 'running' ? 'Update & Restart' : 'Update'}
                      </>
                    )}
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
    </div>
  )
}
