import { useState, useEffect } from 'react'
import { StackDetail } from '@/components/stack/StackDetail'
import { Skeleton } from '@/components/ui/skeleton'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { AlertCircle, RefreshCw, Home, Trash2 } from 'lucide-react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { stacksApi } from '@/lib/api'
import { classifyError } from '@/lib/error-handler'
import { useParams, useNavigate, useLocation } from 'react-router-dom'
import { toast } from 'sonner'
import { useConfirm } from '@/components/ConfirmDialog'
import { useStackStore } from '@/stores/stackStore'

export function StackPage() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const location = useLocation()
  const queryClient = useQueryClient()
  const { confirm, ConfirmComponent } = useConfirm()
  const [isDeleting, setIsDeleting] = useState(false)
  const { setSelectedStack } = useStackStore()

  const tabFromPath = location.pathname.split('/').slice(3).join('/') || 'overview'
  const [activeTab, setActiveTab] = useState(tabFromPath)

  useEffect(() => {
    setActiveTab(tabFromPath)
  }, [tabFromPath])

  useEffect(() => {
    setSelectedStack(id ?? null)
  }, [id, setSelectedStack])

  const handleTabChange = (newTab: string) => {
    setActiveTab(newTab)
    navigate(`/stacks/${id}/${newTab}`, { replace: true })
  }

  const { data: stack, isLoading, error, refetch } = useQuery({
    queryKey: ['stack', id],
    queryFn: () => stacksApi.get(id || ''),
    enabled: !!id,
    staleTime: 30000,
    retry: 1,
  })

  const deleteMutation = useMutation({
    mutationFn: (stackId: string) => stacksApi.delete(stackId),
    onSuccess: () => {
      toast.success('Stack deleted successfully')
      queryClient.invalidateQueries({ queryKey: ['stacks'] })
      navigate('/')
    },
    onError: () => {
      toast.error('Failed to delete stack')
      setIsDeleting(false)
    },
  })

  if (isLoading) {
    return (
      <div className="space-y-6">
        <div>
          <Skeleton className="h-8 w-64" />
          <Skeleton className="mt-2 h-4 w-96" />
        </div>
        <div className="space-y-4">
          <Skeleton className="h-64 w-full" />
        </div>
      </div>
    )
  }

  if (error || !stack) {
    const appError = error ? classifyError(error) : null
    
    return (
      <div className="space-y-6">
        <div>
          <h1 className="text-3xl font-bold tracking-tight">Stack Not Found</h1>
          <p className="text-muted-foreground">
            {appError?.message || 'The requested stack could not be found.'}
          </p>
        </div>
        
        {appError && (
          <Card className="border-destructive">
            <CardContent className="pt-6">
              <div className="flex items-start gap-4">
                <AlertCircle className="h-5 w-5 text-destructive mt-0.5" />
                <div className="flex-1 space-y-2">
                  <h3 className="font-semibold">Failed to load stack</h3>
                  <p className="text-sm text-muted-foreground">{appError.message}</p>
                  <div className="flex gap-2">
                    <Button 
                      onClick={() => refetch()} 
                      disabled={!appError.retryable}
                      variant="outline"
                      size="sm"
                    >
                      <RefreshCw className="mr-2 h-4 w-4" />
                      Retry
                    </Button>
                    <Button 
                      onClick={() => navigate('/')}
                      variant="outline"
                      size="sm"
                    >
                      <Home className="mr-2 h-4 w-4" />
                      Dashboard
                    </Button>
                    {appError.type === 'auth' && (
                      <Button 
                        onClick={() => (window.location.href = '/login')}
                        variant="outline"
                        size="sm"
                      >
                        Login
                      </Button>
                    )}
                  </div>
                </div>
              </div>
            </CardContent>
          </Card>
        )}
        
        {!appError && (
          <Button onClick={() => navigate('/')} variant="outline">
            <Home className="mr-2 h-4 w-4" />
            Back to Dashboard
          </Button>
        )}
      </div>
    )
  }

  const handleContainerAction = (containerId: string, actionTab: 'logs' | 'terminal') => {
    navigate(`/stacks/${id}/${actionTab}`, { state: { containerId } })
  }

  const handleDelete = async () => {
    if (!stack) return
    const confirmed = await confirm(
      `Delete Stack "${stack.projectName}"?`,
      'This action cannot be undone. The stack and all its data will be permanently removed.',
      { confirmText: 'Delete', isDangerous: true }
    )
    if (confirmed) {
      setIsDeleting(true)
      deleteMutation.mutate(stack.id)
    }
  }

  return (
    <>
      <div className="space-y-6">
        <div className="flex items-start justify-between">
          <div>
            <h1 className="text-3xl font-bold tracking-tight">{stack.projectName}</h1>
            <p className="text-muted-foreground">{stack.directory}</p>
          </div>
          <Button
            variant="outline"
            size="sm"
            onClick={handleDelete}
            disabled={isDeleting || deleteMutation.isPending}
            className="text-destructive hover:text-destructive"
          >
            <Trash2 className="mr-2 h-4 w-4" />
            Delete Stack
          </Button>
        </div>

        <StackDetail
          key={stack.id}
          stack={stack}
          activeTab={activeTab}
          onTabChange={handleTabChange}
          onContainerAction={handleContainerAction}
        />
      </div>
      <ConfirmComponent />
    </>
  )
}
