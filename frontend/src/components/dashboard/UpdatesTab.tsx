import { useState, useMemo } from 'react'
import { useCheckUpdates, useCheckUpdatesRefresh, useUpdateContainer, useAutoUpdatePolicies } from '@/hooks/useResources'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import {
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
} from '@/components/ui/table'
import { RefreshCw, Download, ArrowUpDown, History, Settings } from 'lucide-react'
import { toast } from 'sonner'
import { SortFilterBar } from '@/components/dashboard/SortFilterBar'
import { StatusBadge } from '@/components/dashboard/StatusBadge'
import { AutoUpdateToggle } from '@/components/dashboard/AutoUpdateToggle'
import { UpdateLogTab } from '@/components/dashboard/UpdateLogTab'
import type { ContainerUpdateInfo, CachedUpdate, AutoUpdatePolicy } from '@/types'

type SortKey = 'name' | 'image' | 'state' | 'stack'

type UpdateItem = ContainerUpdateInfo | CachedUpdate

function isCachedUpdate(item: UpdateItem): item is CachedUpdate {
  return 'localDigest' in item && 'remoteDigest' in item
}

function formatRelativeTime(dateStr?: string): string {
  if (!dateStr) return 'Never'
  try {
    const date = new Date(dateStr)
    const now = new Date()
    const diffMs = now.getTime() - date.getTime()
    const diffMin = Math.floor(diffMs / 60000)
    if (diffMin < 1) return 'just now'
    if (diffMin < 60) return `${diffMin}m ago`
    const diffHr = Math.floor(diffMin / 60)
    if (diffHr < 24) return `${diffHr}h ago`
    const diffDay = Math.floor(diffHr / 24)
    return `${diffDay}d ago`
  } catch {
    return dateStr
  }
}

export function UpdatesTab() {
  const { data: updateData, isLoading, isError } = useCheckUpdates()
  const refreshMutation = useCheckUpdatesRefresh()
  const updateMutation = useUpdateContainer()
  const { data: policiesData } = useAutoUpdatePolicies()
  const [sortBy, setSortBy] = useState<SortKey>('name')
  const [updatingId, setUpdatingId] = useState<string | null>(null)

  const updates = useMemo(() => updateData?.updates ?? [], [updateData?.updates])
  const fromCache = updateData?.fromCache ?? false
  const scannedAt = updateData?.scannedAt

  const policies = useMemo(() => {
    const map = new Map<string, AutoUpdatePolicy>()
    if (policiesData?.policies) {
      for (const p of policiesData.policies) {
        map.set(`${p.targetType}:${p.targetId}`, p)
      }
    }
    return map
  }, [policiesData])

  const handleCheck = () => {
    refreshMutation.mutate()
  }

  const handleUpdate = (container: UpdateItem) => {
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
    if (!updates.length) return updates
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

  const hasData = updates.length > 0
  const isRefreshing = refreshMutation.isPending
  const neverScanned = !updateData && !isLoading && !isError

  const renderAvailableContent = () => {
    if (isLoading || isRefreshing) {
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

    if (isError && !fromCache) {
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

    if (neverScanned) {
      return (
        <Card>
          <CardContent className="flex flex-col items-center justify-center py-12">
            <ArrowUpDown className="h-12 w-12 text-muted-foreground mb-4" />
            <p className="text-lg font-semibold mb-2">No Scan Data Available</p>
            <p className="text-sm text-muted-foreground mb-4">
              Enable scheduled scanning in Settings or check manually
            </p>
            <div className="flex gap-2">
              <Button onClick={handleCheck}>
                <Download className="mr-2 h-4 w-4" />
                Check for Updates
              </Button>
              <Button variant="outline" asChild>
                <a href="/settings">
                  <Settings className="mr-2 h-4 w-4" />
                  Settings
                </a>
              </Button>
            </div>
          </CardContent>
        </Card>
      )
    }

    if (!hasData) {
      return (
        <Card>
          <CardContent className="flex flex-col items-center justify-center py-12">
            <ArrowUpDown className="h-12 w-12 text-muted-foreground mb-4" />
            <p className="text-lg font-semibold mb-2">All Containers Up to Date</p>
            <p className="text-sm text-muted-foreground mb-4">
              No image updates available for any container
            </p>
            <Button variant="outline" onClick={handleCheck} disabled={isRefreshing}>
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
            <div className="flex items-center gap-2">
              {scannedAt && (
                <span className="text-xs text-muted-foreground">
                  Last scanned: {formatRelativeTime(scannedAt)}
                </span>
              )}
              <Button
                variant="outline"
                size="sm"
                className="h-7 text-xs"
                onClick={handleCheck}
                disabled={isRefreshing}
              >
                <Download className="mr-1 h-3 w-3" />
                Check for Updates
              </Button>
            </div>
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
                <TableHead>Auto-Update</TableHead>
                <TableHead>Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {sortedUpdates.map((container: UpdateItem) => {
                const containerPolicy = policies.get(`container:${container.containerId}`)
                const stackPolicy = container.stackId
                  ? policies.get(`stack:${container.stackId}`)
                  : undefined
                const activePolicy = containerPolicy || stackPolicy

                return (
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
                      {isCachedUpdate(container) && (
                        <div className="text-xs text-muted-foreground mt-1 font-mono">
                          {container.localDigest.substring(0, 12)} → {container.remoteDigest.substring(0, 12)}
                        </div>
                      )}
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
                      {activePolicy ? (
                        <AutoUpdateToggle
                          targetType={containerPolicy ? 'container' : 'stack'}
                          targetId={containerPolicy ? container.containerId : (container.stackId || '')}
                          enabled={activePolicy.enabled}
                          paused={activePolicy.paused}
                          consecutiveFailures={activePolicy.consecutiveFailures}
                        />
                      ) : (
                        <AutoUpdateToggle
                          targetType="container"
                          targetId={container.containerId}
                          enabled={false}
                          paused={false}
                          consecutiveFailures={0}
                        />
                      )}
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
                )
              })}
            </TableBody>
          </Table>
        </div>
      </div>
    )
  }

  return (
    <Tabs defaultValue="available">
      <TabsList>
        <TabsTrigger value="available" className="gap-1.5">
          Available Updates
          {hasData && (
            <Badge variant="destructive" className="ml-1 h-5 min-w-[20px] text-[10px] px-1">
              {updates.length}
            </Badge>
          )}
        </TabsTrigger>
        <TabsTrigger value="log" className="gap-1.5">
          <History className="h-3.5 w-3.5" />
          Update Log
        </TabsTrigger>
      </TabsList>

      <TabsContent value="available" className="mt-4">
        {renderAvailableContent()}
      </TabsContent>

      <TabsContent value="log" className="mt-4">
        <UpdateLogTab />
      </TabsContent>
    </Tabs>
  )
}
