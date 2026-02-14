import { Link } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { Button } from '@/components/ui/button'
import { ScrollArea } from '@/components/ui/scroll-area'
import { useUIStore } from '@/stores/uiStore'

export function Sidebar() {
  const sidebarOpen = useUIStore((state) => state.sidebarOpen)
  const toggleSidebar = useUIStore((state) => state.toggleSidebar)

  const { data: directories = [], isLoading } = useQuery({
    queryKey: ['directories'],
    queryFn: async () => {
      const { directoriesApi } = await import('@/lib/api')
      return directoriesApi.list()
    },
    staleTime: 30_000,
  })

  const directoriesList = Array.isArray(directories) ? directories : []

  if (!sidebarOpen) {
    return (
      <aside className="hidden md:flex flex-col border-r bg-card">
        <Button
          variant="ghost"
          size="icon"
          onClick={toggleSidebar}
          className="fixed left-0 top-1/2 -translate-y-1/2 z-50 min-h-[44px] min-w-[44px]"
        >
          <svg
            xmlns="http://www.w3.org/2000/svg"
            width="24"
            height="24"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="2"
            strokeLinecap="round"
            strokeLinejoin="round"
          >
            <polyline points="9 18 15 12 9 6" />
          </svg>
        </Button>
      </aside>
    )
  }

  return (
    <aside className="hidden lg:flex flex-col border-r bg-card w-64 xl:w-64 lg:w-16 transition-all duration-200">
      <div className="p-4 border-b lg:px-2">
        <h2 className="text-lg font-semibold lg:hidden">Docker Manager</h2>
        <svg
          xmlns="http://www.w3.org/2000/svg"
          width="24"
          height="24"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
          strokeLinecap="round"
          strokeLinejoin="round"
          className="mx-auto lg:block hidden"
        >
          <rect width="24" height="24" rx="2" />
          <path d="M7 7h10" />
          <path d="M7 12h10" />
          <path d="M7 17h10" />
        </svg>
      </div>

      <ScrollArea className="flex-1">
        <div className="p-4 space-y-2 lg:px-2">
          {isLoading ? (
            <div className="text-sm text-muted-foreground lg:hidden">Loading directories...</div>
          ) : directoriesList.length === 0 ? (
            <div className="text-sm text-muted-foreground lg:hidden">No directories found</div>
          ) : (
            directoriesList.map((dir) => (
              <Link
                key={dir.path}
                to="/"
                className="flex items-center p-2 rounded hover:bg-accent text-left transition-colors min-h-[44px]"
              >
                <svg
                  xmlns="http://www.w3.org/2000/svg"
                  width="16"
                  height="16"
                  viewBox="0 0 24 24"
                  fill="none"
                  stroke="currentColor"
                  strokeWidth="2"
                  strokeLinecap="round"
                  strokeLinejoin="round"
                  className="flex-shrink-0"
                >
                  <path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z" />
                </svg>
                <span className="flex-1 truncate ml-2 lg:hidden">{dir.name}</span>
                <span className="text-xs bg-muted px-2 py-0.5 rounded lg:hidden">{dir.stackCount}</span>
                {dir.isGitRepo && (
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
                    className="ml-2 flex-shrink-0 lg:hidden"
                  >
                    <path d="M15 22v-4a4.8 4.8 0 0 0-1-3.5c3 0 6-2 6-5.5.08-1.25-.27-2.48-1-3.5.28-1.15.28-2.35 0-3.5 0 0-1 0-3 1.5-2.64-.5-5.36.5-8 0C6 2 5 2 5 2c-.3 1.15-.3 2.35 0 3.5A5.403 5.403 0 0 0 4 9c0 3.5 3 5.5 6 5.5-.39.49-.68 1.05-.85 1.65-.17.6-.22 1.23-.15 1.85v4" />
                    <path d="M9 18c-4.51 2-5-2-7-2" />
                  </svg>
                )}
              </Link>
            ))
          )}
        </div>
      </ScrollArea>

      <div className="p-4 border-t lg:px-2">
        <Button variant="ghost" size="sm" className="w-full justify-start lg:justify-center lg:px-2 min-h-[44px]" asChild>
          <Link to="/settings">
            <svg
              xmlns="http://www.w3.org/2000/svg"
              width="16"
              height="16"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="2"
              strokeLinecap="round"
              strokeLinejoin="round"
              className="flex-shrink-0"
            >
              <path d="M12.22 2h-.44a2 2 0 0 0-2 2v.18a2 2 0 0 1-1 1.73l-.43.25a2 2 0 0 1-2 0l-.15-.08a2 2 0 0 0-2.73.73l-.22.38a2 2 0 0 0 .73 2.73l.15.1a2 2 0 0 1 1 1.72v.51a2 2 0 0 1-1 1.74l-.15.09a2 2 0 0 0-.73 2.73l.22.38a2 2 0 0 0 2.73.73l.15-.08a2 2 0 0 1 2 0l.43.25a2 2 0 0 1 1 1.73V20a2 2 0 0 0 2 2h.44a2 2 0 0 0 2-2v-.18a2 2 0 0 1 1-1.73l.43-.25a2 2 0 0 1 2 0l.15.08a2 2 0 0 0 2.73-.73l.22-.38a2 2 0 0 0-.73-2.73l-.15-.1a2 2 0 0 1-1-1.72v-.51a2 2 0 0 1 1-1.74l.15-.09a2 2 0 0 0 .73-2.73l-.22-.38a2 2 0 0 0-2.73-.73l-.15.08a2 2 0 0 1-2 0l-.43-.25a2 2 0 0 1-1-1.73V4a2 2 0 0 0-2-2z" />
              <circle cx="12" cy="12" r="3" />
            </svg>
            <span className="ml-2 lg:hidden">Settings</span>
          </Link>
        </Button>
      </div>
    </aside>
  )
}
