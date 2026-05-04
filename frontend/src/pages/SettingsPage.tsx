import { useState, useEffect } from 'react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter, DialogDescription } from '@/components/ui/dialog'
import { LoadingSpinner } from '@/components/LoadingSkeleton'
import { useAuth } from '@/hooks/useAuth'
import { useUIStore } from '@/stores/uiStore'
import { toast } from 'sonner'
import { classifyError } from '@/lib/error-handler'
import { formatDateFull } from '@/lib/format'
import { Sun, Moon, Monitor, Shield, Palette, Clock, KeyRound, FolderCog, ScrollText } from 'lucide-react'
import { authApi } from '@/lib/api'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { CollapsibleSection } from '@/components/settings/CollapsibleSection'
import { UpdateScheduleContent } from '@/components/settings/UpdateScheduleContent'
import { GitSettingsContent } from '@/components/settings/GitSettingsContent'
import { DirectoriesSettingsContent } from '@/components/settings/DirectoriesSettingsContent'
import { AuditLogContent } from '@/components/settings/AuditLogContent'

interface SettingsSection {
  id: string
  title: string
  description: string
  icon: React.ReactNode
  defaultExpanded: boolean
}

const SETTINGS_SECTIONS: SettingsSection[] = [
  {
    id: 'account-security',
    title: 'Account Security',
    description: 'Manage your account information and security settings',
    icon: <Shield className="h-5 w-5" />,
    defaultExpanded: true,
  },
  {
    id: 'appearance',
    title: 'Appearance',
    description: 'Customize the look and feel of the application',
    icon: <Palette className="h-5 w-5" />,
    defaultExpanded: false,
  },
  {
    id: 'directories',
    title: 'Directories',
    description: 'Configure monitored stack directories and default location for new stacks',
    icon: <FolderCog className="h-5 w-5" />,
    defaultExpanded: false,
  },
  {
    id: 'git',
    title: 'Git',
    description: 'Configure global git credentials for repository access',
    icon: <KeyRound className="h-5 w-5" />,
    defaultExpanded: false,
  },
  {
    id: 'update-schedule',
    title: 'Updates',
    description: 'Configure image update scanning and auto-update settings',
    icon: <Clock className="h-5 w-5" />,
    defaultExpanded: false,
  },
  {
    id: 'audit-log',
    title: 'Audit Log',
    description: 'View a history of actions performed in the application',
    icon: <ScrollText className="h-5 w-5" />,
    defaultExpanded: false,
  },
]

export function SettingsPage() {
  const { user, logout, authDisabled } = useAuth()
  const { theme, setTheme } = useUIStore()
  const [currentPassword, setCurrentPassword] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')
  const [isChangingPassword, setIsChangingPassword] = useState(false)
  const [showPasswordConfirmDialog, setShowPasswordConfirmDialog] = useState(false)
  const [expandedSections, setExpandedSections] = useState<Record<string, boolean>>(() => {
    const saved = localStorage.getItem('settings-section-states')
    if (saved) {
      try {
        return JSON.parse(saved)
      } catch {
        return {}
      }
    }
    return {}
  })

  useEffect(() => {
    localStorage.setItem('settings-section-states', JSON.stringify(expandedSections))
  }, [expandedSections])

  const themeOptions = [
    { value: 'light' as const, label: 'Light', icon: Sun },
    { value: 'dark' as const, label: 'Dark', icon: Moon },
    { value: 'system' as const, label: 'System', icon: Monitor },
  ]

  const currentThemeLabel = themeOptions.find((t) => t.value === theme)?.label || theme
  const CurrentThemeIcon = themeOptions.find((t) => t.value === theme)?.icon || Monitor

  const toggleSection = (sectionId: string) => {
    setExpandedSections((prev) => ({
      ...prev,
      [sectionId]: !prev[sectionId],
    }))
  }

  const isSectionExpanded = (sectionId: string) => {
    if (expandedSections[sectionId] !== undefined) {
      return expandedSections[sectionId]
    }
    const section = SETTINGS_SECTIONS.find((s) => s.id === sectionId)
    return section?.defaultExpanded ?? false
  }

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

  return (
    <div className="max-w-4xl mx-auto space-y-6">
      <div>
        <h1 className="text-3xl font-bold tracking-tight">Settings</h1>
        <p className="text-muted-foreground">Manage your account and application settings</p>
      </div>

        <CollapsibleSection
          section={SETTINGS_SECTIONS[0]}
          expanded={isSectionExpanded('account-security')}
          onToggle={() => toggleSection('account-security')}
        >
          {authDisabled ? (
            <div className="py-4">
              <div className="flex items-center gap-3 rounded-lg border border-blue-200 bg-blue-50 p-4 dark:border-blue-900 dark:bg-blue-950">
                <Shield className="h-5 w-5 text-blue-600 dark:text-blue-400" />
                <div>
                  <p className="text-sm font-medium text-blue-900 dark:text-blue-100">
                    Authentication is disabled
                  </p>
                  <p className="text-sm text-blue-700 dark:text-blue-300">
                    Account security settings are not available because authentication is disabled. 
                    Enable authentication to manage account settings.
                  </p>
                </div>
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

                <Button
                  type="submit"
                  disabled={isChangingPassword}
                  className="min-h-[44px]"
                >
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
          )}
        </CollapsibleSection>

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
              <Button
                onClick={handleConfirmPasswordChange}
                disabled={isChangingPassword}
              >
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

        <CollapsibleSection
          section={SETTINGS_SECTIONS[1]}
          expanded={isSectionExpanded('appearance')}
          onToggle={() => toggleSection('appearance')}
        >
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
          </div>
        </CollapsibleSection>

        <CollapsibleSection
          section={SETTINGS_SECTIONS[2]}
          expanded={isSectionExpanded('directories')}
          onToggle={() => toggleSection('directories')}
        >
          <DirectoriesSettingsContent />
        </CollapsibleSection>

        <CollapsibleSection
          section={SETTINGS_SECTIONS[3]}
          expanded={isSectionExpanded('git')}
          onToggle={() => toggleSection('git')}
        >
          <GitSettingsContent />
        </CollapsibleSection>

        <CollapsibleSection
          section={SETTINGS_SECTIONS[4]}
          expanded={isSectionExpanded('update-schedule')}
          onToggle={() => toggleSection('update-schedule')}
        >
          <UpdateScheduleContent />
        </CollapsibleSection>

        <CollapsibleSection
          section={SETTINGS_SECTIONS[5]}
          expanded={isSectionExpanded('audit-log')}
          onToggle={() => toggleSection('audit-log')}
        >
          <AuditLogContent />
        </CollapsibleSection>
    </div>
  )
}
