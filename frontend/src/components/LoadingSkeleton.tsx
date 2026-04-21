import { cn } from '@/lib/utils'

const SpinnerIcon = ({ className }: { className?: string }) => (
  <svg
    className={className}
    xmlns="http://www.w3.org/2000/svg"
    fill="none"
    viewBox="0 0 24 24"
  >
    <circle
      className="opacity-25"
      cx="12"
      cy="12"
      r="10"
      stroke="currentColor"
      strokeWidth="4"
    />
    <path
      className="opacity-75"
      fill="currentColor"
      d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"
    />
  </svg>
)

export function StackCardSkeleton() {
  return (
    <div className="rounded-lg border bg-card p-4">
      <div className="flex items-start justify-between mb-3">
        <div className="flex-1">
          <div className="h-5 w-32 bg-muted animate-pulse rounded mb-2" />
          <div className="h-4 w-48 bg-muted animate-pulse rounded" />
        </div>
        <div className="h-6 w-16 bg-muted animate-pulse rounded-full" />
      </div>
      <div className="flex gap-2 mb-3">
        <div className="h-8 w-24 bg-muted animate-pulse rounded" />
        <div className="h-8 w-24 bg-muted animate-pulse rounded" />
      </div>
      <div className="space-y-2">
        <div className="h-4 w-full bg-muted animate-pulse rounded" />
        <div className="h-4 w-3/4 bg-muted animate-pulse rounded" />
      </div>
    </div>
  )
}

export function ContainerTableSkeleton() {
  return (
    <div className="rounded-lg border bg-card">
      <div className="grid grid-cols-4 gap-4 p-4 border-b font-medium text-sm">
        <div>Name</div>
        <div>Status</div>
        <div>Ports</div>
        <div className="text-right">Actions</div>
      </div>
      {Array.from({ length: 3 }).map((_, i) => (
        <div key={i} className="grid grid-cols-4 gap-4 p-4 border-b last:border-0">
          <div className="h-5 w-40 bg-muted animate-pulse rounded" />
          <div className="h-6 w-20 bg-muted animate-pulse rounded-full" />
          <div className="h-5 w-32 bg-muted animate-pulse rounded" />
          <div className="flex justify-end gap-2">
            <div className="h-8 w-20 bg-muted animate-pulse rounded" />
            <div className="h-8 w-16 bg-muted animate-pulse rounded" />
          </div>
        </div>
      ))}
    </div>
  )
}

export function EditorSkeleton() {
  return (
    <div className="rounded-lg border bg-card">
      <div className="flex items-center justify-between p-4 border-b">
        <div className="h-6 w-48 bg-muted animate-pulse rounded" />
        <div className="flex gap-2">
          <div className="h-9 w-24 bg-muted animate-pulse rounded" />
          <div className="h-9 w-20 bg-muted animate-pulse rounded" />
        </div>
      </div>
      <div className="p-4">
        <div className="space-y-2 font-mono text-sm">
          {Array.from({ length: 12 }).map((_, i) => (
            <div key={i} className="h-5 w-full bg-muted animate-pulse rounded" />
          ))}
        </div>
      </div>
    </div>
  )
}

export function GitHistorySkeleton() {
  return (
    <div className="rounded-lg border bg-card">
      {Array.from({ length: 5 }).map((_, i) => (
        <div key={i} className="flex items-start gap-4 p-4 border-b last:border-0">
          <div className="h-8 w-8 bg-muted animate-pulse rounded-full shrink-0" />
          <div className="flex-1 min-w-0">
            <div className="h-5 w-64 bg-muted animate-pulse rounded mb-2" />
            <div className="h-4 w-96 bg-muted animate-pulse rounded mb-1" />
            <div className="h-4 w-32 bg-muted animate-pulse rounded" />
          </div>
          <div className="flex gap-2 shrink-0">
            <div className="h-8 w-16 bg-muted animate-pulse rounded" />
            <div className="h-8 w-16 bg-muted animate-pulse rounded" />
          </div>
        </div>
      ))}
    </div>
  )
}

export function MetricsSkeleton() {
  return (
    <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
      {Array.from({ length: 4 }).map((_, i) => (
        <div key={i} className="rounded-lg border bg-card p-4">
          <div className="h-4 w-24 bg-muted animate-pulse rounded mb-2" />
          <div className="h-8 w-16 bg-muted animate-pulse rounded" />
          <div className="h-4 w-20 bg-muted animate-pulse rounded mt-2" />
        </div>
      ))}
    </div>
  )
}

export function LoadingSpinner({ size = 'default' }: { size?: 'small' | 'default' | 'large' }) {
  const sizeClasses = {
    small: 'w-4 h-4',
    default: 'w-6 h-6',
    large: 'w-8 h-8',
  }

  return <SpinnerIcon className={cn('animate-spin text-muted-foreground', sizeClasses[size])} />
}
