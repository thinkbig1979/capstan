import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { RefreshCw, Download, ArrowUpDown, Settings } from 'lucide-react'

/** Shown while a scan is actively running (isRefreshing). */
export function CheckingUpdatesCard() {
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

/** Shown while the initial query is loading (no scan in flight). */
export function LoadingSkeletons() {
  return (
    <div className="space-y-2">
      {Array.from({ length: 3 }).map((_, i) => (
        <Skeleton key={i} className="h-12 w-full" />
      ))}
    </div>
  )
}

interface RetryCardProps {
  onCheck: () => void
}

/** Shown when the check-updates query errored and there is no cached data to fall back to. */
export function UpdateCheckErrorCard({ onCheck }: RetryCardProps) {
  return (
    <Card>
      <CardContent className="flex flex-col items-center justify-center py-12">
        <p className="text-lg font-semibold mb-2">Failed to Check for Updates</p>
        <p className="text-sm text-muted-foreground mb-4">
          An error occurred while checking for container image updates
        </p>
        <Button onClick={onCheck}>
          <Download className="mr-2 h-4 w-4" />
          Retry
        </Button>
      </CardContent>
    </Card>
  )
}

/** Shown when no scan has ever run (no cached data, not loading/erroring/scanning). */
export function NeverScannedCard({ onCheck }: RetryCardProps) {
  return (
    <Card>
      <CardContent className="flex flex-col items-center justify-center py-12">
        <ArrowUpDown className="h-12 w-12 text-muted-foreground mb-4" />
        <p className="text-lg font-semibold mb-2">No Scan Data Available</p>
        <p className="text-sm text-muted-foreground mb-4">
          Enable scheduled scanning in Settings or check manually
        </p>
        <div className="flex gap-2">
          <Button onClick={onCheck}>
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

interface NoUpdatesCardProps {
  onCheck: () => void
  isRefreshing: boolean
}

/** Shown when a scan has completed and found nothing to update. */
export function NoUpdatesCard({ onCheck, isRefreshing }: NoUpdatesCardProps) {
  return (
    <Card>
      <CardContent className="flex flex-col items-center justify-center py-12">
        <ArrowUpDown className="h-12 w-12 text-muted-foreground mb-4" />
        <p className="text-lg font-semibold mb-2">All Containers Up to Date</p>
        <p className="text-sm text-muted-foreground mb-4">
          No image updates available for any container
        </p>
        <Button variant="outline" onClick={onCheck} disabled={isRefreshing}>
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
