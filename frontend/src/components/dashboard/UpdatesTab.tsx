import { useState, useMemo } from 'react'
import { useCheckUpdates, useCheckUpdatesRefresh, useUpdateContainer, useAutoUpdatePolicies, useUpdateJobs } from '@/hooks/useResources'
import { useUpdateScanStore } from '@/stores/updateScanStore'
import { useUpdateJobStore } from '@/stores/updateJobStore'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import {
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
} from '@/components/ui/table'
import { RefreshCw, Download, ArrowUpDown, Settings } from 'lucide-react'
import { toast } from 'sonner'
import { classifyError } from '@/lib/error-handler'
import { SortFilterBar } from '@/components/dashboard/SortFilterBar'
import { StatusBadge } from '@/components/dashboard/StatusBadge'
import { AutoUpdateToggle } from '@/components/dashboard/AutoUpdateToggle'
import { BackupToggle } from '@/components/dashboard/BackupToggle'
import { UpdateLogTab } from '@/components/dashboard/UpdateLogTab'
import { UpdateJobStatusCell } from '@/components/dashboard/UpdateJobStatusCell'
import type { ContainerUpdateInfo, CachedUpdate, AutoUpdatePolicy } from '@/types'
import { formatRelativeTime } from '@/lib/format'

type SortKey = 'name' | 'image' | 'state' | 'stack'

type UpdateItem = ContainerUpdateInfo | CachedUpdate

function isCachedUpdate(item: UpdateItem): item is CachedUpdate {
  return 'localDigest' in item && 'remoteDigest' in item
}

export function UpdatesTab() {
  const { data: updateData, isLoading, isError } = useCheckUpdates()
  const refreshMutation = useCheckUpdatesRefresh()
  const updateMutation = useUpdateContainer()
  const { data: policiesData } = useAutoUpdatePolicies()
  const { isScanning } = useUpdateScanStore()
  const [sortBy, setSortBy] = useState<SortKey>('name')
  // Expanded log state: set of containerIds with expanded log panels
  const [expandedIds, setExpandedIds] = useState<Set<string>>(new Set())

  // Hydrate store on mount so returning to the tab reflects in-flight/recent jobs
  useUpdateJobs()

  const jobForContainer = useUpdateJobStore((s) => s.jobForContainer)

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
    updateMutation.mutate(container.containerId, {
      onSuccess: () => {
        // The store drives UI; the toast just confirms the action was accepted
        const action = container.state === 'running' ? 'queued for update and restart' : 'queued for update'
        toast.success(`${container.containerName} ${action}`)
      },
      onError: (err) => {
        toast.error(classifyError(err).message || `Failed to update ${container.containerName}`)
      },
    })
  }

  const toggleExpand = (containerId: string) => {
    setExpandedIds((prev) => {
      const next = new Set(prev)
      if (next.has(containerId)) {
        next.delete(containerId)
      } else {
        next.add(containerId)
      }
      return next
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
  const isRefreshing = isScanning
  const neverScanned = !updateData && !isLoading && !isError && !isScanning

  const renderAvailableContent = () => {
    if (isRefreshing) {
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

    if (isLoading) {
      return (
        <div className="space-y-2">
          {Array.from({ length: 3 }).map((_, i) => (
            <Skeleton key={i} className="h-12 w-full" />
          ))}
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
              {isRefreshing ? (
                <RefreshCw className="mr-2 h-4 w-4 animate-spin" />
              ) : (
                <Download className="mr-2 h-4 w-4" />
              )}
              {isRefreshing ? 'Checking…' : 'Check Again'}
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
                {isRefreshing ? (
                  <RefreshCw className="mr-1 h-3 w-3 animate-spin" />
                ) : (
                  <Download className="mr-1 h-3 w-3" />
                )}
                {isRefreshing ? 'Checking…' : 'Check for Updates'}
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
                const job = jobForContainer(container.containerId)
                const expanded = expandedIds.has(container.containerId)

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
                          className="text-sm text-info hover:underline"
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
                      <div className="flex items-center gap-2">
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
                        {!containerPolicy && container.stackId && (
                          <BackupToggle stackId={container.stackId} />
                        )}
                      </div>
                    </TableCell>
                    <TableCell>
                      <UpdateJobStatusCell
                        job={job}
                        expanded={expanded}
                        onToggleExpand={() => toggleExpand(container.containerId)}
                        onUpdate={() => handleUpdate(container)}
                        isRunning={container.state === 'running'}
                        updatePending={updateMutation.isPending}
                      />
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
        <TabsTrigger value="log">
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
