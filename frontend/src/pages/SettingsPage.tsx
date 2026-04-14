import { useState, useCallback, useEffect } from 'react'
import { useQuery } from '@tanstack/react-query'
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
import { Eye, EyeOff, AlertCircle, RefreshCw, Sun, Moon, Monitor, ChevronDown, ChevronUp, Shield, Palette, Cpu } from 'lucide-react'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'

interface SettingsSection {
  id: 'account-security' | 'appearance' | 'system'
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
    id: 'system',
    title: 'System',
    description: 'Configure system-wide environment variables',
    icon: <Cpu className="h-5 w-5" />,
    defaultExpanded: false,
  },
]

interface CollapsibleSectionProps {
  section: SettingsSection
  expanded: boolean
  onToggle: () => void
  children: React.ReactNode
}

function CollapsibleSection({ section, expanded, onToggle, children }: CollapsibleSectionProps) {
  return (
    <Card>
      <CardHeader
        className="cursor-pointer hover:bg-muted/50 transition-colors select-none"
        onClick={onToggle}
        onKeyDown={(e) => {
          if (e.key === 'Enter' || e.key === ' ') {
            e.preventDefault()
            onToggle()
          }
        }}
        role="button"
        tabIndex={0}
        aria-expanded={expanded}
        aria-controls={`${section.id}-content`}
      >
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-3">
            <div className="p-2 rounded-lg bg-primary/10 text-primary">
              {section.icon}
            </div>
            <div>
              <CardTitle className="text-base">{section.title}</CardTitle>
              <p className="text-sm text-muted-foreground">{section.description}</p>
            </div>
          </div>
          <Button variant="ghost" size="icon" aria-label={expanded ? `Collapse ${section.title}` : `Expand ${section.title}`}>
            {expanded ? <ChevronUp className="h-4 w-4" /> : <ChevronDown className="h-4 w-4" />}
          </Button>
        </div>
      </CardHeader>
      {expanded && (
        <CardContent id={`${section.id}-content`} className="pt-0">
          {children}
        </CardContent>
      )}
    </Card>
  )
}

function maskValue(value: string): string {
  if (value.length <= 4) {
    return '*'.repeat(value.length)
  }
  const visibleChars = 2
  return value.slice(0, visibleChars) + '*'.repeat(value.length - visibleChars)
}

function GlobalEnvContent({
  globalEnv,
  isLoadingEnv,
  error: envError,
  refetch
}: {
  globalEnv?: { vars: { key: string; value: string }[] };
  isLoadingEnv: boolean;
  error?: unknown;
  refetch: () => void;
}) {
  const [isAuthenticated, setIsAuthenticated] = useState(false)
  const [authTimeout, setAuthTimeout] = useState<ReturnType<typeof setTimeout> | null>(null)
  const [showValues, setShowValues] = useState<Record<number, boolean>>({})
  const [authPassword, setAuthPassword] = useState('')
  const [showAuthDialog, setShowAuthDialog] = useState(false)

  useEffect(() => {
    return () => {
      if (authTimeout) {
        clearTimeout(authTimeout)
      }
    }
  }, [authTimeout])

  const resetAuth = useCallback(() => {
    setIsAuthenticated(false)
    setShowValues({})
    if (authTimeout) {
      clearTimeout(authTimeout)
      setAuthTimeout(null)
    }
  }, [authTimeout])

  const startAuthTimeout = useCallback(() => {
    if (authTimeout) {
      clearTimeout(authTimeout)
    }
    const timeout = setTimeout(() => {
      setIsAuthenticated(false)
      setShowValues({})
      toast.info('Authentication session expired')
    }, 5 * 60 * 1000) // 5 minutes
    setAuthTimeout(timeout)
  }, [authTimeout])

  const handleAuthSuccess = useCallback(() => {
    setIsAuthenticated(true)
    setShowValues(
      Object.fromEntries(globalEnv!.vars.map((_, i) => [i, true])),
    )
    setShowAuthDialog(false)
    setAuthPassword('')
    toast.success('Password verified')
    startAuthTimeout()
  }, [globalEnv, startAuthTimeout])

  if (isLoadingEnv) {
    return (
      <div className="flex items-center gap-2 text-sm text-muted-foreground">
        <LoadingSpinner size="small" />
        Loading environment variables...
      </div>
    )
  }

  if (envError) {
    const appError = classifyError(envError)
    return (
      <div className="flex items-start gap-2 text-sm text-destructive">
        <AlertCircle className="h-4 w-4 mt-0.5 flex-shrink-0" />
        <div className="flex-1 space-y-2">
          <p>{appError.message}</p>
          {appError.retryable && (
            <Button 
              variant="outline" 
              size="sm" 
              onClick={refetch}
              className="h-7"
            >
              <RefreshCw className="mr-1 h-3 w-3" />
              Retry
            </Button>
          )}
        </div>
      </div>
    )
  }

  if (!globalEnv?.vars?.length) {
    return (
      <div className="text-sm text-muted-foreground">
        No global environment variables configured. Create a file at <code className="bg-muted px-1 rounded">/opt/stacks/global.env</code> to add variables.
      </div>
    )
  }

  const handleToggle = (index: number) => {
    if (isAuthenticated) {
      setShowValues(prev => ({ ...prev, [index]: !prev[index] }))
      startAuthTimeout()
    } else {
      setShowAuthDialog(true)
    }
  }

  const handleAuthConfirm = async () => {
    if (!authPassword) {
      toast.error('Password required')
      return
    }

    try {
      const { authApi } = await import('@/lib/api')
      await authApi.login(globalEnv!.vars[0].key, authPassword)
      handleAuthSuccess()
    } catch {
      toast.error('Invalid password')
    }
  }

  return (
    <>
      <div className="flex items-center justify-between mb-3">
        <div className="flex items-center gap-2 text-sm text-muted-foreground">
          {isAuthenticated ? (
            <>
              <EyeOff className="h-4 w-4" />
              <span>Unlocked - all values visible</span>
            </>
          ) : (
            <>
              <Eye className="h-4 w-4" />
              <span>Locked - enter password to view</span>
            </>
          )}
        </div>
        {isAuthenticated && (
          <Button
            variant="outline"
            size="sm"
            onClick={resetAuth}
            className="h-8"
          >
            Hide all
          </Button>
        )}
      </div>
      
      <div className="space-y-2">
        {globalEnv.vars.map((envVar: { key: string; value: string }, index: number) => {
          const displayValue = showValues[index] ? envVar.value : maskValue(envVar.value)
          return (
            <div key={index} className="flex items-center gap-2">
              <div className="flex-1 font-mono text-sm bg-muted p-2 rounded">
                <span className="font-semibold">{envVar.key}</span>=
                <span className="opacity-70">{displayValue}</span>
              </div>
              <Button
                type="button"
                variant="ghost"
                size="sm"
                onClick={() => handleToggle(index)}
                className="h-8 w-8 p-0"
                aria-label={showValues[index] ? 'Hide value' : 'Show value'}
              >
                {showValues[index] ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
              </Button>
            </div>
          )
        })}
      </div>

      {showAuthDialog && (
        <div className="fixed inset-0 z-50 flex items-center justify-center">
          <div className="fixed inset-0 bg-black/80" onClick={() => setShowAuthDialog(false)} aria-hidden="true" />
          <div className="relative z-50 w-full max-w-md rounded-lg border bg-background p-6 shadow-lg">
            <h3 className="text-lg font-semibold mb-4">Verify Password</h3>
            <div className="space-y-4">
              <div>
                <Label htmlFor="auth-password">Password</Label>
                <Input
                  id="auth-password"
                  type="password"
                  value={authPassword}
                  onChange={(e) => setAuthPassword(e.target.value)}
                  onKeyDown={(e) => e.key === 'Enter' && handleAuthConfirm()}
                  className="mt-1.5"
                />
              </div>
              <div className="flex gap-2">
                <Button variant="outline" onClick={() => setShowAuthDialog(false)} className="flex-1">
                  Cancel
                </Button>
                <Button onClick={handleAuthConfirm} className="flex-1">
                  Verify
                </Button>
              </div>
            </div>
          </div>
        </div>
      )}
    </>
  )
}

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

  const { 
    data: globalEnv, 
    isLoading: isLoadingEnv,
    error: envError,
    refetch: refetchGlobalEnv 
  } = useQuery({
    queryKey: ['settings', 'global-env'],
    queryFn: async () => {
      const { authApi } = await import('@/lib/api')
      return authApi.getGlobalEnv()
    },
    staleTime: 30_000,
    retry: 1,
  })

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
      const { authApi } = await import('@/lib/api')
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
                  value={user?.createdAt ? new Date(user.createdAt).toLocaleDateString() : 'N/A'}
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
          expanded={isSectionExpanded('system')}
          onToggle={() => toggleSection('system')}
        >
          <GlobalEnvContent
            globalEnv={globalEnv}
            isLoadingEnv={isLoadingEnv}
            error={envError}
            refetch={refetchGlobalEnv}
          />
        </CollapsibleSection>
    </div>
  )
}
