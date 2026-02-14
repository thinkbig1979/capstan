import { useQuery } from '@tanstack/react-query'
import { Card, CardContent } from '@/components/ui/card'
import { NoContainers } from '@/components/EmptyState'
import { LoadingSpinner } from '@/components/LoadingSkeleton'

interface ContainerCardListProps {
  stackId: string
}

export function ContainerCardList({ stackId }: ContainerCardListProps) {
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
    <div className="space-y-4">
      {containers.map((container: any) => (
        <Card key={container.id}>
          <CardContent className="p-4">
            <div className="flex items-start justify-between mb-2">
              <div className="flex-1 min-w-0">
                <h3 className="font-semibold truncate">{container.name}</h3>
                <p className="text-sm text-muted-foreground truncate">{container.image}</p>
              </div>
              <div
                className={`px-2 py-1 rounded text-xs font-medium ${
                  container.state === 'running'
                    ? 'bg-green-100 text-green-800 dark:bg-green-900 dark:text-green-100'
                    : 'bg-gray-100 text-gray-800 dark:bg-gray-800 dark:text-gray-100'
                }`}
              >
                {container.state}
              </div>
            </div>

            {container.ports && container.ports.length > 0 && (
              <div className="text-sm text-muted-foreground mb-3">
                <span className="font-medium">Ports:</span>{' '}
                {container.ports.map((p: any) => p.publicPort).filter(Boolean).join(', ')}
              </div>
            )}

            <div className="flex gap-2">
              <button className="flex-1 min-h-[36px] px-3 py-2 text-sm font-medium rounded-md border hover:bg-accent">
                Logs
              </button>
              <button className="flex-1 min-h-[36px] px-3 py-2 text-sm font-medium rounded-md border hover:bg-accent">
                Terminal
              </button>
              <button
                className={`min-h-[36px] px-3 py-2 text-sm font-medium rounded-md border ${
                  container.state === 'running'
                    ? 'hover:bg-destructive hover:text-destructive-foreground'
                    : ''
                }`}
              >
                {container.state === 'running' ? 'Stop' : 'Start'}
              </button>
            </div>
          </CardContent>
        </Card>
      ))}
    </div>
  )
}
