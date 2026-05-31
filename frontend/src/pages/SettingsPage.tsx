import { useState, useMemo } from 'react'
import { useParams, useNavigate, Link } from 'react-router-dom'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter, DialogDescription } from '@/components/ui/dialog'
import { LoadingSpinner } from '@/components/LoadingSkeleton'
import { useAuth } from '@/hooks/useAuth'
import { useUIStore } from '@/stores/uiStore'
import { toast } from 'sonner'
import { classifyError } from '@/lib/error-handler'
import { formatDateFull } from '@/lib/format'
import { cn } from '@/lib/utils'
import {
  Sun, Moon, Monitor, Shield, Palette, Clock, KeyRound, FolderCog,
  ScrollText, Globe, HardDrive, Search, type LucideIcon,
} from 'lucide-react'
import { BackupSettingsContent } from '@/components/settings/BackupSettingsContent'
import { authApi } from '@/lib/api'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { UpdateScheduleContent } from '@/components/settings/UpdateScheduleContent'
import { GitSettingsContent } from '@/components/settings/GitSettingsContent'
import { DirectoriesSettingsContent } from '@/components/settings/DirectoriesSettingsContent'
import { AuditLogContent } from '@/components/settings/AuditLogContent'
import { GlobalEnvSettingsContent } from '@/components/settings/GlobalEnvSettingsContent'

interface SettingsSection {
  id: string
  title: string
  description: string
  Icon: LucideIcon
}

// Flat catalog of every settings section. Order within a group is preserved
// from the GROUPS definition below.
const ALL_SECTIONS: SettingsSection[] = [
  {
    id: 'account-security',
    title: 'Account Security',
    description: 'Manage your account information and security settings',
    Icon: Shield,
  },
  {
    id: 'appearance',
    title: 'Appearance',
    description: 'Customize the look and feel of the application',
    Icon: Palette,
  },
  {
    id: 'directories',
    title: 'Directories',
    description: 'Configure monitored stack directories and default location for new stacks',
    Icon: FolderCog,
  },
  {
    id: 'global-env',
    title: 'Global Environment Variables',
    description: 'Variables applied to every stack before its own .env file',
    Icon: Globe,
  },
  {
    id: 'git',
    title: 'Git',
    description: 'Configure global git credentials for repository access',
    Icon: KeyRound,
  },
  {
    id: 'update-schedule',
    title: 'Updates',
    description: 'Configure image update scanning and auto-update settings',
    Icon: Clock,
  },
  {
    id: 'backup',
    title: 'Backup',
    description: 'Configure restic repository, retention policy, schedule, and rclone cloud sync',
    Icon: HardDrive,
  },
  {
    id: 'audit-log',
    title: 'Audit Log',
    description: 'View a history of actions performed in the application',
    Icon: ScrollText,
  },
]

// Sidebar grouping. Each group lists section ids in display order.
const GROUPS: { label: string; ids: string[] }[] = [
  { label: 'Account', ids: ['account-security', 'appearance'] },
  { label: 'Stacks', ids: ['directories', 'global-env', 'git'] },
  { label: 'Automation', ids: ['update-schedule', 'backup'] },
  { label: 'System', ids: ['audit-log'] },
]

const DEFAULT_SECTION = 'account-security'

function sectionById(id: string | undefined): SettingsSection {
  return ALL_SECTIONS.find((s) => s.id === id) ?? ALL_SECTIONS[0]
}

export function SettingsPage() {
  const { section } = useParams<{ section?: string }>()
  const navigate = useNavigate()
  const { user, logout, authDisabled } = useAuth()
  const { theme, setTheme } = useUIStore()
  const [currentPassword, setCurrentPassword] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')
  const [isChangingPassword, setIsChangingPassword] = useState(false)
  const [showPasswordConfirmDialog, setShowPasswordConfirmDialog] = useState(false)
  const [query, setQuery] = useState('')

  // The active section is driven by the URL (/settings/:section) so individual
  // sections are deep-linkable and the browser back button works as expected.
  const activeId = ALL_SECTIONS.some((s) => s.id === section) ? section! : DEFAULT_SECTION
  const active = sectionById(activeId)

  const filteredGroups = useMemo(() => {
    const q = query.trim().toLowerCase()
    return GROUPS.map((group) => ({
      label: group.label,
      sections: group.ids
        .map((id) => sectionById(id))
        .filter(
          (s) =>
            q === '' ||
            s.title.toLowerCase().includes(q) ||
            s.description.toLowerCase().includes(q),
        ),
    })).filter((group) => group.sections.length > 0)
  }, [query])

  const themeOptions = [
    { value: 'light' as const, label: 'Light', icon: Sun },
    { value: 'dark' as const, label: 'Dark', icon: Moon },
    { value: 'system' as const, label: 'System', icon: Monitor },
  ]

  const currentThemeLabel = themeOptions.find((t) => t.value === theme)?.label || theme
  const CurrentThemeIcon = themeOptions.find((t) => t.value === theme)?.icon || Monitor

  const handlePasswordChange = async (e: React.FormEvent) => {
    e.preventDefault()

    if (newPassword.length < 8) {
      toast.error('Password must be at least 8 characters')
      return
    }

    if (newPassword !== confirmPassword) {
      toast.error('Passwords do not match')
      return
    }

    setShowPasswordConfirmDialog(true)
  }

  const handleConfirmPasswordChange = async () => {
    setIsChangingPassword(true)
    try {
      await authApi.changePassword(currentPassword, newPassword)
      toast.success('Password changed successfully. Please log in again.')
      setShowPasswordConfirmDialog(false)
      setCurrentPassword('')
      setNewPassword('')
      setConfirmPassword('')
      logout()
    } catch (error: unknown) {
      const appError = classifyError(error)
      toast.error(appError.message)
      setShowPasswordConfirmDialog(false)
    } finally {
      setIsChangingPassword(false)
    }
  }

  // Renders the body for the active section. Account & appearance stay inline
  // because they read this page's state/handlers; the rest are self-contained.
  const renderActiveContent = () => {
    switch (activeId) {
      case 'account-security':
        return authDisabled ? (
          <div className="flex items-center gap-3 rounded-lg border border-info/30 bg-info/10 p-4">
            <Shield className="h-5 w-5 text-info" />
            <div>
              <p className="text-sm font-medium text-info">Authentication is disabled</p>
              <p className="text-sm text-info/80">
                Account security settings are not available because authentication is disabled.
                Enable authentication to manage account settings.
              </p>
            </div>
          </div>
        ) : (
          <div className="space-y-6">
            <div className="space-y-4">
              <h3 className="text-lg font-medium">Account Information</h3>
              <div className="space-y-2">
                <Label htmlFor="username">Username</Label>
                <Input id="username" value={user?.username || ''} disabled className="bg-muted" />
              </div>

              <div className="space-y-2">
                <Label htmlFor="created">Created</Label>
                <Input
                  id="created"
                  value={user?.createdAt ? formatDateFull(user.createdAt) : 'N/A'}
                  disabled
                  className="bg-muted"
                />
              </div>
            </div>

            <div className="space-y-4 pt-4 border-t">
              <h3 className="text-lg font-medium">Change Password</h3>
              <form onSubmit={handlePasswordChange} className="space-y-4">
                <div className="space-y-2">
                  <Label htmlFor="current-password">Current Password</Label>
                  <Input
                    id="current-password"
                    type="password"
                    value={currentPassword}
                    onChange={(e) => setCurrentPassword(e.target.value)}
                    required
                    disabled={isChangingPassword}
                    className="min-h-[44px]"
                    aria-describedby="current-password-hint"
                  />
                  <p id="current-password-hint" className="text-sm text-muted-foreground">
                    Enter your current password to change it
                  </p>
                </div>

                <div className="space-y-2">
                  <Label htmlFor="new-password">New Password</Label>
                  <Input
                    id="new-password"
                    type="password"
                    value={newPassword}
                    onChange={(e) => setNewPassword(e.target.value)}
                    required
                    minLength={8}
                    disabled={isChangingPassword}
                    className="min-h-[44px]"
                    aria-describedby="new-password-hint"
                  />
                  <p id="new-password-hint" className="text-sm text-muted-foreground">
                    Password must be at least 8 characters
                  </p>
                </div>

                <div className="space-y-2">
                  <Label htmlFor="confirm-password">Confirm Password</Label>
                  <Input
                    id="confirm-password"
                    type="password"
                    value={confirmPassword}
                    onChange={(e) => setConfirmPassword(e.target.value)}
                    required
                    minLength={8}
                    disabled={isChangingPassword}
                    className="min-h-[44px]"
                  />
                </div>

                <Button type="submit" disabled={isChangingPassword} className="min-h-[44px]">
                  {isChangingPassword ? (
                    <>
                      <span className="mr-2"><LoadingSpinner size="small" /></span>
                      Changing...
                    </>
                  ) : (
                    'Change Password'
                  )}
                </Button>
              </form>
            </div>
          </div>
        )

      case 'appearance':
        return (
          <div className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="theme-select">Theme</Label>
              <div className="flex items-center gap-3 mb-3">
                <div className="flex items-center gap-2 text-sm text-muted-foreground">
                  <CurrentThemeIcon className="h-4 w-4" />
                  <span>Current: {currentThemeLabel}</span>
                </div>
              </div>
              <Select value={theme} onValueChange={(value) => setTheme(value as 'light' | 'dark' | 'system')}>
                <SelectTrigger id="theme-select" className="w-full max-w-xs">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {themeOptions.map((option) => {
                    const Icon = option.icon
                    return (
                      <SelectItem key={option.value} value={option.value}>
                        <div className="flex items-center gap-2">
                          <Icon className="h-4 w-4" />
                          <span>{option.label}</span>
                        </div>
                      </SelectItem>
                    )
                  })}
                </SelectContent>
              </Select>
              <p className="text-sm text-muted-foreground">
                Theme selection is also available in header using the theme toggle button.
              </p>
            </div>

            <div className="space-y-2 pt-4 border-t">
              <Label>Layout Preferences</Label>
              <p className="text-sm text-muted-foreground">
                Reset sidebar width, sidebar filters, dashboard sort/filter, and section expansion to defaults. Your theme, account, and stack data are not affected.
              </p>
              <Button
                variant="outline"
                size="sm"
                onClick={() => {
                  if (window.confirm('Reset all layout preferences to defaults? Theme and account settings will not be affected.')) {
                    useUIStore.getState().resetLayout()
                    toast.success('Layout preferences reset. Reloading to apply…')
                    setTimeout(() => window.location.reload(), 600)
                  }
                }}
              >
                Reset layout preferences
              </Button>
            </div>
          </div>
        )

      case 'directories':
        return <DirectoriesSettingsContent />
      case 'git':
        return <GitSettingsContent />
      case 'global-env':
        return <GlobalEnvSettingsContent />
      case 'update-schedule':
        return <UpdateScheduleContent />
      case 'backup':
        return <BackupSettingsContent />
      case 'audit-log':
        return <AuditLogContent />
      default:
        return null
    }
  }

  const ActiveIcon = active.Icon

  return (
    <div className="max-w-6xl mx-auto space-y-6">
      <div>
        <h1 className="text-3xl font-bold tracking-tight">Settings</h1>
        <p className="text-muted-foreground">Manage your account and application settings</p>
      </div>

      {/* Mobile: section picker (the sidebar is hidden below md). */}
      <div className="md:hidden">
        <Select value={activeId} onValueChange={(value) => navigate(`/settings/${value}`)}>
          <SelectTrigger aria-label="Select settings section">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {ALL_SECTIONS.map((s) => (
              <SelectItem key={s.id} value={s.id}>
                <div className="flex items-center gap-2">
                  <s.Icon className="h-4 w-4" />
                  <span>{s.title}</span>
                </div>
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>

      <div className="flex gap-6">
        {/* Sidebar navigation (desktop). */}
        <aside className="hidden md:block w-60 shrink-0">
          <div className="sticky top-6 space-y-4">
            <div className="relative">
              <Search className="pointer-events-none absolute left-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
              <Input
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                placeholder="Search settings"
                aria-label="Search settings"
                className="pl-8"
              />
            </div>

            <nav className="space-y-4" aria-label="Settings sections">
              {filteredGroups.map((group) => (
                <div key={group.label} className="space-y-1">
                  <p className="px-2 text-xs font-medium uppercase tracking-wider text-muted-foreground">
                    {group.label}
                  </p>
                  {group.sections.map((s) => {
                    const isActive = s.id === activeId
                    return (
                      <Link
                        key={s.id}
                        to={`/settings/${s.id}`}
                        aria-current={isActive ? 'page' : undefined}
                        className={cn(
                          'flex items-center gap-2.5 rounded-md px-2 py-1.5 text-sm transition-colors',
                          isActive
                            ? 'bg-primary/10 font-medium text-primary'
                            : 'text-foreground hover:bg-muted',
                        )}
                      >
                        <s.Icon className="h-4 w-4 shrink-0" />
                        <span className="truncate">{s.title}</span>
                      </Link>
                    )
                  })}
                </div>
              ))}
              {filteredGroups.length === 0 && (
                <p className="px-2 text-sm text-muted-foreground">
                  No settings match “{query}”.
                </p>
              )}
            </nav>
          </div>
        </aside>

        {/* Detail pane for the active section. */}
        <div className="min-w-0 flex-1">
          <Card>
            <CardHeader>
              <div className="flex items-center gap-3">
                <div className="rounded-lg bg-primary/10 p-2 text-primary">
                  <ActiveIcon className="h-5 w-5" />
                </div>
                <div>
                  <CardTitle className="text-base">{active.title}</CardTitle>
                  <p className="text-sm text-muted-foreground">{active.description}</p>
                </div>
              </div>
            </CardHeader>
            <CardContent>{renderActiveContent()}</CardContent>
          </Card>
        </div>
      </div>

      <Dialog open={showPasswordConfirmDialog} onOpenChange={setShowPasswordConfirmDialog}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Confirm Password Change</DialogTitle>
            <DialogDescription className="space-y-2 pt-2">
              <p>New password: {'•'.repeat(8)}</p>
              <p className="text-destructive font-medium">This will log you out of all devices</p>
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button
              variant="outline"
              onClick={() => setShowPasswordConfirmDialog(false)}
              disabled={isChangingPassword}
            >
              Cancel
            </Button>
            <Button onClick={handleConfirmPasswordChange} disabled={isChangingPassword}>
              {isChangingPassword ? (
                <>
                  <span className="mr-2"><LoadingSpinner size="small" /></span>
                  Changing...
                </>
              ) : (
                'Confirm'
              )}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
