import { useQuery } from '@tanstack/react-query'
import { Card, CardContent } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { NoDirectories } from '@/components/EmptyState'
import { StackCardSkeleton } from '@/components/LoadingSkeleton'

export function DirectoryList() {
  const { data: directories = [], isLoading, refetch } = useQuery({
    queryKey: ['directories'],
    queryFn: async () => {
      const { directoriesApi } = await import('@/lib/api')
      return directoriesApi.list()
    },
    staleTime: 30_000,
  })

  const directoriesList = Array.isArray(directories) ? directories : []

  if (isLoading) {
    return (
      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
        {Array.from({ length: 6 }).map((_, i) => (
          <StackCardSkeleton key={i} />
        ))}
      </div>
    )
  }

  if (directoriesList.length === 0) {
    return <NoDirectories onScan={() => refetch()} />
  }

  return (
    <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 xl:grid-cols-3 gap-4">
      {directoriesList.map((dir) => (
        <Card key={dir.path} className="hover:shadow-md transition-shadow">
          <CardContent className="p-4">
            <div className="flex items-start justify-between mb-3">
              <div className="flex-1 min-w-0">
                <h3 className="font-semibold truncate" title={dir.name}>{dir.name}</h3>
                <p className="text-sm text-muted-foreground truncate" title={dir.path}>{dir.path}</p>
              </div>
              <span className="text-xs bg-muted px-2 py-0.5 rounded ml-2 shrink-0">
                {dir.stackCount} stacks
              </span>
            </div>

            {dir.isGitRepo && (
              <div className="flex items-center gap-1 text-xs text-muted-foreground mb-3">
                <svg
                  xmlns="http://www.w3.org/2000/svg"
                  width="12"
                  height="12"
                  viewBox="0 0 24 24"
                  fill="none"
                  stroke="currentColor"
                  strokeWidth="2"
                  strokeLinecap="round"
                  strokeLinejoin="round"
                >
                  <path d="M15 22v-4a4.8 4.8 0 0 0-1-3.5c3 0 6-2 6-5.5.08-1.25-.27-2.48-1-3.5.28-1.15.28-2.35 0-3.5 0 0-1 0-3 1.5-2.64-.5-5.36.5-8 0C6 2 5 2 5 2c-.3 1.15-.3 2.35 0 3.5A5.403 5.403 0 0 0 4 9c0 3.5 3 5.5 6 5.5-.39.49-.68 1.05-.85 1.65-.17.6-.22 1.23-.15 1.85v4" />
                  <path d="M9 18c-4.51 2-5-2-7-2" />
                </svg>
                <span className="truncate">{dir.gitBranch || 'main'}</span>
              </div>
            )}

            <div className="flex gap-2">
              <Button variant="outline" size="sm" className="flex-1 min-h-[36px]" asChild>
                <a href={`/?dir=${encodeURIComponent(dir.path)}`}>
                  View Stacks
                </a>
              </Button>
              {dir.isGitRepo && (
                <Button variant="outline" size="sm" className="min-h-[36px]">
                  <svg
                    xmlns="http://www.w3.org/2000/svg"
                    width="14"
                    height="14"
                    viewBox="0 0 24 24"
                    fill="none"
                    stroke="currentColor"
                    strokeWidth="2"
                    strokeLinecap="round"
                    strokeLinejoin="round"
                  >
                    <path d="M15 22v-4a4.8 4.8 0 0 0-1-3.5c3 0 6-2 6-5.5.08-1.25-.27-2.48-1-3.5.28-1.15.28-2.35 0-3.5 0 0-1 0-3 1.5-2.64-.5-5.36.5-8 0C6 2 5 2 5 2c-.3 1.15-.3 2.35 0 3.5A5.403 5.403 0 0 0 4 9c0 3.5 3 5.5 6 5.5-.39.49-.68 1.05-.85 1.65-.17.6-.22 1.23-.15 1.85v4" />
                    <path d="M9 18c-4.51 2-5-2-7-2" />
                  </svg>
                </Button>
              )}
            </div>
          </CardContent>
        </Card>
      ))}
    </div>
  )
}
