import { useQuery } from '@tanstack/react-query'
import { Button } from '@/components/ui/button'
import { NoContainers } from '@/components/EmptyState'
import { LoadingSpinner } from '@/components/LoadingSkeleton'

interface ContainerTableProps {
  stackId: string
}

export function ContainerTable({ stackId }: ContainerTableProps) {
  const { data: stack, isLoading } = useQuery({
    queryKey: ['stack', stackId],
    queryFn: async () => {
      const { stacksApi } = await import('@/lib/api')
      return stacksApi.get(stackId)
    },
    staleTime: 10_000,
  })

  if (isLoading) {
    return (
      <div className="flex items-center justify-center min-h-[200px]">
        <LoadingSpinner size="large" />
      </div>
    )
  }

  const containers = stack?.containers || []

  if (containers.length === 0) {
    return <NoContainers />
  }

  return (
    <div className="rounded-lg border bg-card">
      <div className="grid grid-cols-4 gap-4 p-4 border-b font-medium text-sm">
        <div>Name</div>
        <div>Status</div>
        <div>Ports</div>
        <div className="text-right">Actions</div>
      </div>
      {containers.map((container: any) => (
        <div key={container.id} className="grid grid-cols-4 gap-4 p-4 border-b last:border-0 items-center">
          <div>
            <div className="font-medium">{container.name}</div>
            <div className="text-sm text-muted-foreground truncate">{container.image}</div>
          </div>
          <div>
            <div
              className={`inline-flex items-center px-2 py-1 rounded text-xs font-medium ${
                container.state === 'running'
                  ? 'bg-green-100 text-green-800 dark:bg-green-900 dark:text-green-100'
                  : 'bg-gray-100 text-gray-800 dark:bg-gray-800 dark:text-gray-100'
              }`}
            >
              {container.state}
            </div>
          </div>
          <div className="text-sm text-muted-foreground">
            {container.ports && container.ports.length > 0
              ? container.ports.map((p: any) => p.publicPort).filter(Boolean).join(', ')
              : '-'}
          </div>
          <div className="flex justify-end gap-2">
            <Button variant="outline" size="sm">
              Logs
            </Button>
            <Button variant="outline" size="sm">
              Terminal
            </Button>
            <Button
              variant={container.state === 'running' ? 'destructive' : 'default'}
              size="sm"
            >
              {container.state === 'running' ? 'Stop' : 'Start'}
            </Button>
          </div>
        </div>
      ))}
    </div>
  )
}
