import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { directoriesApi, stacksApi } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Plus, Folder, GitBranch, GitPullRequest } from 'lucide-react'
import { CreateStackDialog } from '@/components/stack/CreateStackDialog'
import { useStackStatusAnimation } from '@/hooks/useStackStatusAnimation'

export function DashboardPage() {
  const [createDialogOpen, setCreateDialogOpen] = useState(false)
  const { isAnimating } = useStackStatusAnimation()

  const { data: directories } = useQuery({
    queryKey: ['directories'],
    queryFn: directoriesApi.list,
  })

  const { data: stacks } = useQuery({
    queryKey: ['stacks'],
    queryFn: () => stacksApi.list(),
  })

  const runningCount = stacks?.filter((s) => s.status === 'running').length || 0
  const stoppedCount = stacks?.filter((s) => s.status === 'stopped').length || 0
  const containerCount = stacks?.reduce((sum, s) => sum + (s.containerCount || 0), 0) || 0

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-3xl font-bold tracking-tight">Dashboard</h1>
          <p className="text-muted-foreground">Welcome to Docker Manager</p>
        </div>
        <Button onClick={() => setCreateDialogOpen(true)}>
          <Plus className="mr-2 h-4 w-4" />
          New Stack
        </Button>
      </div>

      <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-4">
        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium">Total Stacks</CardTitle>
            <Folder className="h-4 w-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold">{stacks?.length || 0}</div>
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium">Running</CardTitle>
            <div className="h-2 w-2 rounded-full bg-green-500" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold">{runningCount}</div>
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium">Stopped</CardTitle>
            <div className="h-2 w-2 rounded-full bg-red-500" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold">{stoppedCount}</div>
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium">Containers</CardTitle>
            <div className="h-2 w-2 rounded-full bg-blue-500" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold">{containerCount}</div>
          </CardContent>
        </Card>
      </div>

      <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3">
        {directories?.map((dir) => (
          <Card key={dir.path} className="hover:shadow-md transition-shadow cursor-pointer">
            <CardHeader>
              <CardTitle className="flex items-center gap-2">
                <Folder className="h-5 w-5" />
                {dir.name}
              </CardTitle>
              <CardDescription>{dir.path}</CardDescription>
            </CardHeader>
            <CardContent>
              <div className="flex items-center gap-2 mb-2">
                <Badge variant="secondary">{dir.stackCount} stacks</Badge>
                {dir.isGitRepo && (
                  <>
                    <Badge variant="outline" className="flex items-center gap-1">
                      <GitBranch className="h-3 w-3" />
                      {dir.gitBranch || 'main'}
                    </Badge>
                    {dir.gitBehind > 0 && (
                      <Badge variant="secondary" className="flex items-center gap-1 text-yellow-600">
                        <GitPullRequest className="h-3 w-3" />
                        {dir.gitBehind}
                      </Badge>
                    )}
                  </>
                )}
              </div>
            </CardContent>
          </Card>
        ))}

        {stacks?.map((stack) => (
          <Card
            key={stack.id}
            className="hover:shadow-md transition-shadow cursor-pointer"
            onClick={() => (window.location.href = `/stacks/${stack.id}`)}
          >
            <CardHeader>
              <CardTitle>{stack.projectName}</CardTitle>
              <CardDescription>{stack.directory}</CardDescription>
            </CardHeader>
            <CardContent>
              <div className="flex items-center gap-2">
                <Badge
                  variant={
                    stack.status === 'running'
                      ? 'default'
                      : stack.status === 'stopped'
                      ? 'secondary'
                      : 'outline'
                  }
                  className={isAnimating(stack.id) ? 'animate-pulse' : ''}
                >
                  {stack.status}
                </Badge>
                {stack.containerCount !== undefined && (
                  <Badge variant="outline">{stack.containerCount} containers</Badge>
                )}
              </div>
            </CardContent>
          </Card>
        ))}
      </div>

      {stacks?.length === 0 && directories?.length === 0 && (
        <Card>
          <CardHeader>
            <CardTitle>Quick Start</CardTitle>
            <CardDescription>Get started by creating your first stack</CardDescription>
          </CardHeader>
          <CardContent>
            <p className="text-sm text-muted-foreground mb-4">
              No stacks configured yet. Click "New Stack" to create your first Docker Compose stack.
            </p>
            <Button onClick={() => setCreateDialogOpen(true)}>
              <Plus className="mr-2 h-4 w-4" />
              Create Your First Stack
            </Button>
          </CardContent>
        </Card>
      )}

      <CreateStackDialog open={createDialogOpen} onOpenChange={setCreateDialogOpen} />
    </div>
  )
}
