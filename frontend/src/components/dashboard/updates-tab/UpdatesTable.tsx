import { Fragment } from 'react'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import {
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
} from '@/components/ui/table'
import { RefreshCw, Download } from 'lucide-react'
import { SortFilterBar } from '@/components/dashboard/SortFilterBar'
import { HelpHint } from '@/components/ui/help-hint'
import { StatusBadge } from '@/components/dashboard/StatusBadge'
import { AutoUpdateToggle } from '@/components/dashboard/AutoUpdateToggle'
import { BackupToggle } from '@/components/dashboard/BackupToggle'
import { UpdateJobStatusCell } from '@/components/dashboard/UpdateJobStatusCell'
import { UpdateJobLog } from '@/components/updates/UpdateJobLog'
import type { AutoUpdatePolicy } from '@/types'
import type { UpdateJob } from '@/stores/updateJobStore'
import { formatRelativeTime } from '@/lib/format'
import { isCachedUpdate, type SortKey, type UpdateItem } from './types'

interface UpdatesTableProps {
  sortedUpdates: UpdateItem[]
  totalCount: number
  sortBy: SortKey
  onSortChange: (key: SortKey) => void
  query: string
  onQueryChange: (value: string) => void
  scannedAt: string | undefined
  isRefreshing: boolean
  onCheck: () => void
  policies: Map<string, AutoUpdatePolicy>
  jobForContainer: (containerId: string) => UpdateJob | undefined
  expandedIds: Set<string>
  onToggleExpand: (containerId: string) => void
  onUpdate: (container: UpdateItem) => void
  updatePending: boolean
}

export function UpdatesTable({
  sortedUpdates,
  totalCount,
  sortBy,
  onSortChange,
  query,
  onQueryChange,
  scannedAt,
  isRefreshing,
  onCheck,
  policies,
  jobForContainer,
  expandedIds,
  onToggleExpand,
  onUpdate,
  updatePending,
}: UpdatesTableProps) {
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
        onSortChange={(key) => onSortChange(key as SortKey)}
        help={
          <HelpHint label="Updates" title="Updates" side="bottom" align="start">
            <p>
              Each container&apos;s current image is compared against its registry. A row here
              means a newer image was published for that tag.
            </p>
            <p>
              Updating pulls it and recreates the container. Auto-update does the same on a
              schedule for the containers and stacks you opt in.
            </p>
          </HelpHint>
        }
        searchValue={query}
        onSearchChange={onQueryChange}
        searchPlaceholder="Filter updates…"
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
              onClick={onCheck}
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
        countDisplay={
          query
            ? `${sortedUpdates.length} of ${totalCount} update${totalCount !== 1 ? 's' : ''} available`
            : `${totalCount} update${totalCount !== 1 ? 's' : ''} available`
        }
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
                <Fragment key={container.containerId}>
                <TableRow>
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
                      onToggleExpand={() => onToggleExpand(container.containerId)}
                      onUpdate={() => onUpdate(container)}
                      isRunning={container.state === 'running'}
                      updatePending={updatePending}
                    />
                  </TableCell>
                </TableRow>
                {expanded && job && (
                  <TableRow>
                    <TableCell colSpan={6} className="bg-muted/30 p-3">
                      <UpdateJobLog job={job} enabled={expanded} />
                    </TableCell>
                  </TableRow>
                )}
                </Fragment>
              )
            })}
          </TableBody>
        </Table>
      </div>
    </div>
  )
}
