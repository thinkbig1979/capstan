import { useState, useEffect } from 'react'
import { useLocation, Link } from 'react-router-dom'
import { Logo } from '@/components/Logo'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { ScrollArea } from '@/components/ui/scroll-area'
import { useUIStore } from '@/stores/uiStore'
import { useAuth } from '@/hooks/useAuth'
import { toast } from 'sonner'
import { HelpCircle, Settings, Sun, Moon, Laptop, ChevronLeft } from 'lucide-react'

export function Header() {
  const location = useLocation()
  const { theme, setTheme, toggleSidebar } = useUIStore()
  const { user, logout } = useAuth()
  const [showShortcuts, setShowShortcuts] = useState(false)

  const shortcuts = [
    {
      category: 'General',
      shortcuts: [
        { key: 'Ctrl+K', description: 'Open search' },
        { key: 'Ctrl+/', description: 'Keyboard shortcuts help' },
      ],
    },
    {
      category: 'Stack Management',
      shortcuts: [
        { key: 'Ctrl+S', description: 'Save changes (Compose/Env)' },
        { key: 'Ctrl+L', description: 'Lint compose file' },
      ],
    },
    {
      category: 'Editor',
      shortcuts: [
        { key: 'Ctrl+Z', description: 'Undo (Env editor)' },
        { key: 'Ctrl+Y', description: 'Redo (Env editor)' },
        { key: 'Ctrl+F', description: 'Find in editor' },
      ],
    },
    {
      category: 'Navigation',
      shortcuts: [
        { key: 'Escape', description: 'Close dialogs/modals' },
      ],
    },
  ]

  const getCurrentCategory = () => {
    const path = location.pathname
    if (path.startsWith('/stacks/')) return 'Stack Management'
    if (path.startsWith('/settings')) return 'General'
    return 'General'
  }

  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key === '/') {
        e.preventDefault()
        setShowShortcuts(true)
      }
      if (e.key === 'Escape') {
        setShowShortcuts(false)
      }
    }
    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [])

  const handleLogout = async () => {
    try {
      await logout()
      toast.success('Logged out successfully')
    } catch (error) {
      console.error('Logout error:', error)
    }
  }

  const getBreadcrumbs = () => {
    const path = location.pathname
    if (path === '/') {
      return [{ label: 'Dashboard', path: '/' }]
    }
    if (path.startsWith('/stacks/')) {
      const searchParams = new URLSearchParams(location.search)
      const tab = searchParams.get('tab') || 'overview'
      const tabLabel = tab.charAt(0).toUpperCase() + tab.slice(1)
      return [
        { label: 'Dashboard', path: '/' },
        { label: 'Stack Details', path: location.pathname },
        { label: tabLabel, path: `${location.pathname}?tab=${tab}` },
      ]
    }
    if (path === '/settings') {
      return [
        { label: 'Dashboard', path: '/' },
        { label: 'Settings', path: '/settings' },
      ]
    }
    return [{ label: 'Dashboard', path: '/' }]
  }

  const breadcrumbs = getBreadcrumbs()

  return (
    <header className="sticky top-0 z-10 flex h-16 shrink-0 items-center gap-2 border-b bg-background/90 backdrop-blur-sm px-4">
      <div className="flex items-center gap-2">
        <Button variant="ghost" size="icon" onClick={toggleSidebar} aria-label="Toggle sidebar" title="Toggle sidebar">
          <svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <line x1="4" x2="20" y1="12" y2="12" />
            <line x1="4" x2="20" y1="6" y2="6" />
            <line x1="4" x2="20" y1="18" y2="18" />
          </svg>
        </Button>
        <Link to="/" className="flex items-center gap-2 font-semibold tracking-tight" aria-label="Capstan home">
          <Logo className="h-7 w-7" />
          <span className="hidden sm:inline text-base">Capstan</span>
        </Link>
        {location.pathname !== '/' && (
          <Button variant="ghost" size="sm" className="md:hidden gap-1" asChild>
            <Link to="/">
              <ChevronLeft className="h-4 w-4" />
              Dashboard
            </Link>
          </Button>
        )}

        <nav className="hidden md:flex items-center gap-1 text-sm text-muted-foreground" aria-label="Breadcrumb">
          {breadcrumbs.map((crumb, index) => (
            <div key={crumb.path} className="flex items-center">
              {index > 0 && (
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
                  className="mx-1"
                  aria-hidden="true"
                >
                  <path d="m9 18 6-6-6-6" />
                </svg>
              )}
              {index === breadcrumbs.length - 1 ? (
                <span className="text-foreground font-medium" aria-current="page">
                  {crumb.label}
                </span>
              ) : (
                <Link to={crumb.path} className="hover:text-foreground transition-colors">
                  {crumb.label}
                </Link>
              )}
            </div>
          ))}
        </nav>
      </div>

      <div className="ml-auto flex items-center gap-2">
        <Button
          variant="ghost"
          size="icon"
          onClick={() => setShowShortcuts(true)}
          title="Keyboard shortcuts (Ctrl+/)"
          aria-label="Show keyboard shortcuts"
        >
          <HelpCircle className="h-5 w-5" />
        </Button>

        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button variant="ghost" size="icon" aria-label={`Current theme: ${theme}. Click to change theme`}>
              {theme === 'light' && <Sun className="h-5 w-5" />}
              {theme === 'dark' && <Moon className="h-5 w-5" />}
              {theme === 'system' && <Laptop className="h-5 w-5" />}
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuItem onClick={() => setTheme('light')}>Light</DropdownMenuItem>
            <DropdownMenuItem onClick={() => setTheme('dark')}>Dark</DropdownMenuItem>
            <DropdownMenuItem onClick={() => setTheme('system')}>System</DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem asChild>
              <Link to="/settings#appearance">Appearance Settings...</Link>
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>

        <Button variant="ghost" size="icon" asChild aria-label="Settings" title="Settings">
          <Link to="/settings">
            <Settings className="h-5 w-5" />
          </Link>
        </Button>

        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button variant="ghost" size="icon" aria-label="User menu">
              <svg
                xmlns="http://www.w3.org/2000/svg"
                width="20"
                height="20"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="2"
                strokeLinecap="round"
                strokeLinejoin="round"
              >
                <path d="M19 21v-2a4 4 0 0 0-4-4H9a4 4 0 0 0-4 4v2" />
                <circle cx="12" cy="7" r="4" />
              </svg>
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="w-56">
            <DropdownMenuLabel>
              <div className="flex flex-col space-y-1">
                <p className="text-sm font-medium">{user?.username || 'User'}</p>
              </div>
            </DropdownMenuLabel>
            <DropdownMenuSeparator />
            <DropdownMenuItem asChild>
              <button onClick={handleLogout} aria-label="Log out of account">
                Logout
              </button>
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>

      <Dialog open={showShortcuts} onOpenChange={setShowShortcuts}>
        <DialogContent className="sm:max-w-[500px]">
          <DialogHeader>
            <DialogTitle>Keyboard Shortcuts</DialogTitle>
            <DialogDescription>
              Current page: <span className="font-medium">{getCurrentCategory()}</span>
            </DialogDescription>
          </DialogHeader>
          <ScrollArea className="max-h-[400px] pr-4">
            <div className="space-y-4">
              {shortcuts.map((group) => (
                <div key={group.category}>
                  <h4 className="text-sm font-semibold mb-2">{group.category}</h4>
                  <div className="space-y-2">
                    {group.shortcuts.map((shortcut) => (
                      <div key={shortcut.key} className="flex items-center justify-between text-sm">
                        <span className="text-muted-foreground">{shortcut.description}</span>
                        <kbd className="px-2 py-1 text-xs font-semibold bg-muted rounded border">
                          {shortcut.key}
                        </kbd>
                      </div>
                    ))}
                  </div>
                </div>
              ))}
            </div>
          </ScrollArea>
        </DialogContent>
      </Dialog>
    </header>
  )
}
