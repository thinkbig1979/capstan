import { Link, useLocation } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { Button } from '@/components/ui/button'
import { ScrollArea } from '@/components/ui/scroll-area'
import { useUIStore } from '@/stores/uiStore'
import { stacksApi } from '@/lib/api'
const statusColor: Record<string, string> = {
  running: 'bg-green-500',
  partial: 'bg-yellow-500',
  stopped: 'bg-red-500',
  error: 'bg-red-500',
  unknown: 'bg-gray-400',
}

export function Sidebar() {
  const sidebarOpen = useUIStore((state) => state.sidebarOpen)
  const toggleSidebar = useUIStore((state) => state.toggleSidebar)
  const location = useLocation()

  const { data: stacks = [], isLoading } = useQuery({
    queryKey: ['stacks'],
    queryFn: () => stacksApi.list(),
    staleTime: 30_000,
  })

  if (!sidebarOpen) {
    return (
      <aside className="hidden md:flex flex-col border-r bg-card">
        <Button
          variant="ghost"
          size="icon"
          onClick={toggleSidebar}
          className="fixed left-0 top-1/2 -translate-y-1/2 z-50 min-h-[44px] min-w-[44px]"
          aria-label="Open sidebar"
          title="Open sidebar"
        >
          <svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <polyline points="9 18 15 12 9 6" />
          </svg>
        </Button>
      </aside>
    )
  }

  return (
    <aside className="hidden lg:flex flex-col border-r bg-card w-64 transition-all duration-200">
      <div className="p-4 border-b">
        <h2 className="text-lg font-semibold">Stacks</h2>
      </div>

      <ScrollArea className="flex-1">
        <div className="p-2 space-y-0.5">
          {isLoading ? (
            <div className="px-2 py-4 text-sm text-muted-foreground">Loading...</div>
          ) : stacks.length === 0 ? (
            <div className="px-2 py-4 text-sm text-muted-foreground">No stacks found</div>
          ) : (
            stacks.map((stack) => {
              const isActive = location.pathname.startsWith(`/stacks/${stack.id}`)
              const color = statusColor[stack.status] || statusColor.unknown
              return (
                <Link
                  key={stack.id}
                  to={`/stacks/${stack.id}`}
                  className={`flex items-center gap-2 px-3 py-2 rounded text-sm transition-colors min-h-[40px] ${
                    isActive ? 'bg-accent text-accent-foreground font-medium' : 'hover:bg-accent/50 text-foreground'
                  }`}
                  aria-label={`${stack.projectName} - ${stack.status}`}
                >
                  <span className={`h-2 w-2 rounded-full flex-shrink-0 ${color}`} title={stack.status} />
                  <span className="flex-1 truncate">{stack.projectName}</span>
                  {stack.isGitRepo && stack.gitDirty && (
                    <span className="h-2 w-2 rounded-full bg-orange-400 flex-shrink-0" title="Uncommitted changes" />
                  )}
                </Link>
              )
            })
          )}
        </div>
      </ScrollArea>

      <div className="p-2 border-t">
        <Button variant="ghost" size="sm" className="w-full justify-start min-h-[40px]" asChild aria-label="Go to settings">
          <Link to="/settings">
            <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" className="flex-shrink-0">
              <path d="M12.22 2h-.44a2 2 0 0 0-2 2v.18a2 2 0 0 1-1 1.73l-.43.25a2 2 0 0 1-2 0l-.15-.08a2 2 0 0 0-2.73.73l-.22.38a2 2 0 0 0 .73 2.73l.15.1a2 2 0 0 1 1 1.72v.51a2 2 0 0 1-1 1.74l-.15.09a2 2 0 0 0-.73 2.73l.22.38a2 2 0 0 0 2.73.73l.15-.08a2 2 0 0 1 2 0l.43.25a2 2 0 0 1 1 1.73V20a2 2 0 0 0 2 2h.44a2 2 0 0 0 2-2v-.18a2 2 0 0 1 1-1.73l.43-.25a2 2 0 0 1 2 0l.15.08a2 2 0 0 0 2.73-.73l.22-.38a2 2 0 0 0-.73-2.73l-.15-.1a2 2 0 0 1-1-1.72v-.51a2 2 0 0 1 1-1.74l.15-.09a2 2 0 0 0 .73-2.73l-.22-.38a2 2 0 0 0-2.73-.73l-.15.08a2 2 0 0 1-2 0l-.43-.25a2 2 0 0 1-1-1.73V4a2 2 0 0 0-2-2z" />
              <circle cx="12" cy="12" r="3" />
            </svg>
            <span className="ml-2">Settings</span>
          </Link>
        </Button>
      </div>
    </aside>
  )
}
