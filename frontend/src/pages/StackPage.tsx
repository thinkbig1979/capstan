import { AppShell } from '@/components/layout/AppShell'
import { StackDetail } from '@/components/stack/StackDetail'
import { Skeleton } from '@/components/ui/skeleton'
import { useQuery } from '@tanstack/react-query'
import { stacksApi } from '@/lib/api'
import { useParams, useNavigate } from 'react-router-dom'

export function StackPage() {
  const { id, tab = 'overview' } = useParams<{ id: string; tab?: string }>()
  const navigate = useNavigate()

  const handleTabChange = (newTab: string) => {
    navigate(`/stacks/${id}/${newTab}`)
  }

  const { data: stack, isLoading, error } = useQuery({
    queryKey: ['stack', id],
    queryFn: () => stacksApi.get(id || ''),
    enabled: !!id,
    staleTime: 30000,
  })

  if (isLoading) {
    return (
      <AppShell>
        <div className="space-y-6">
          <div>
            <Skeleton className="h-8 w-64" />
            <Skeleton className="mt-2 h-4 w-96" />
          </div>
          <div className="space-y-4">
            <Skeleton className="h-64 w-full" />
          </div>
        </div>
      </AppShell>
    )
  }

  if (error || !stack) {
    return (
      <AppShell>
        <div className="space-y-6">
          <div>
            <h1 className="text-3xl font-bold tracking-tight">Stack Not Found</h1>
            <p className="text-muted-foreground">The requested stack could not be found.</p>
          </div>
          <button
            onClick={() => navigate('/dashboard')}
            className="rounded-md border px-4 py-2 hover:bg-muted"
          >
            Back to Dashboard
          </button>
        </div>
      </AppShell>
    )
  }

  const handleContainerAction = (containerId: string, actionTab: 'logs' | 'terminal') => {
    navigate(`/stacks/${id}/${actionTab}`, { state: { containerId } })
  }

  return (
    <AppShell>
      <div className="space-y-6">
        <div>
          <h1 className="text-3xl font-bold tracking-tight">{stack.projectName}</h1>
          <p className="text-muted-foreground">{stack.directory}</p>
        </div>

        <StackDetail
          stack={stack}
          activeTab={tab || 'overview'}
          onTabChange={handleTabChange}
          onContainerAction={handleContainerAction}
        />
      </div>
    </AppShell>
  )
}
