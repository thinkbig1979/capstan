import { useState, useEffect } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter, DialogDescription } from '@/components/ui/dialog'
import { Switch } from '@/components/ui/switch'
import { LoadingSpinner } from '@/components/LoadingSkeleton'
import { useAuth } from '@/hooks/useAuth'
import { useUIStore } from '@/stores/uiStore'
import { useUpdateSettings, useUpdateUpdateSettings, useGitSettings, useUpdateGitSettings } from '@/hooks/useResources'
import { toast } from 'sonner'
import { classifyError } from '@/lib/error-handler'
import { AlertCircle, Sun, Moon, Monitor, ChevronDown, ChevronUp, Shield, Palette, Clock, KeyRound, FolderCog } from 'lucide-react'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'

interface SettingsSection {
  id: 'account-security' | 'appearance' | 'directories' | 'git' | 'update-schedule'
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

function UpdateScheduleContent() {
  const { data: settings, isLoading } = useUpdateSettings()
  const updateSettingsMutation = useUpdateUpdateSettings()

  const [initialized, setInitialized] = useState(false)
  const [scanPreset, setScanPreset] = useState<string>('0')
  const [customMinutes, setCustomMinutes] = useState<number>(60)
  const [globalAutoUpdate, setGlobalAutoUpdate] = useState(false)

  const presets = ['0', '60', '360', '720', '1440']

  useEffect(() => {
    if (settings && !initialized) {
      if (presets.includes(String(settings.scanIntervalMinutes))) {
        setScanPreset(String(settings.scanIntervalMinutes))
      } else {
        setScanPreset('custom')
        setCustomMinutes(settings.scanIntervalMinutes)
      }
      setGlobalAutoUpdate(settings.globalAutoUpdate)
      setInitialized(true)
    }
  }, [settings, initialized])

  const scanInterval = settings?.scanIntervalMinutes ?? 0
  const effectivePreset = initialized ? scanPreset : (presets.includes(String(scanInterval)) ? String(scanInterval) : 'custom')
  const effectiveCustom = initialized ? customMinutes : scanInterval
  const effectiveAutoUpdate = initialized ? globalAutoUpdate : (settings?.globalAutoUpdate ?? false)

  if (isLoading) {
    return (
      <div className="flex items-center gap-2 text-sm text-muted-foreground">
        <LoadingSpinner size="small" />
        Loading update settings...
      </div>
    )
  }

  const save = (updates: { scanIntervalMinutes?: number; globalAutoUpdate?: boolean }) => {
    const minutes = updates.scanIntervalMinutes ?? (effectivePreset === 'custom' ? effectiveCustom : parseInt(effectivePreset, 10))
    const autoUpdate = updates.globalAutoUpdate ?? effectiveAutoUpdate
    if (minutes > 0 && minutes < 15) {
      toast.error('Custom interval must be at least 15 minutes')
      return
    }
    updateSettingsMutation.mutate(
      { scanIntervalMinutes: minutes, globalAutoUpdate: autoUpdate },
      {
        onSuccess: () => toast.success('Settings saved'),
        onError: () => toast.error('Failed to save settings'),
      },
    )
  }

  const handlePresetChange = (value: string) => {
    setScanPreset(value)
    if (value !== 'custom') {
      save({ scanIntervalMinutes: parseInt(value, 10) })
    }
  }

  const handleCustomBlur = () => {
    if (scanPreset === 'custom') {
      save({ scanIntervalMinutes: effectiveCustom })
    }
  }

  const handleAutoUpdateChange = (checked: boolean) => {
    setGlobalAutoUpdate(checked)
    save({ globalAutoUpdate: checked })
  }

  const stats = settings?.autoUpdateStats

  return (
    <div className="space-y-6">
      <div className="space-y-4">
        <h3 className="text-lg font-medium">Scan for Image Updates</h3>
        <div className="space-y-2">
          <Label htmlFor="scan-interval">Scan Interval</Label>
          <Select value={effectivePreset} onValueChange={handlePresetChange}>
            <SelectTrigger id="scan-interval" className="w-full max-w-xs">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="0">Disabled</SelectItem>
              <SelectItem value="60">Every hour</SelectItem>
              <SelectItem value="360">Every 6 hours</SelectItem>
              <SelectItem value="720">Every 12 hours</SelectItem>
              <SelectItem value="1440">Every 24 hours</SelectItem>
              <SelectItem value="custom">Custom</SelectItem>
            </SelectContent>
          </Select>
        </div>

        {effectivePreset === 'custom' && (
          <div className="space-y-2">
            <Label htmlFor="custom-minutes">Custom interval (minutes)</Label>
            <Input
              id="custom-minutes"
              type="number"
              min={15}
              max={10080}
              value={effectiveCustom}
              onChange={(e) => setCustomMinutes(parseInt(e.target.value, 10) || 0)}
              onBlur={handleCustomBlur}
              className="max-w-xs"
            />
            <p className="text-xs text-muted-foreground">Minimum 15 minutes</p>
          </div>
        )}

        {settings?.lastScanAt && (
          <p className="text-sm text-muted-foreground">
            Last scanned: {new Date(settings.lastScanAt).toLocaleString()}
          </p>
        )}
        {!settings?.lastScanAt && (
          <p className="text-sm text-muted-foreground">Last scanned: Never</p>
        )}
        {settings?.lastScanError && (
          <p className="text-sm text-destructive">
            Last scan error: {settings.lastScanError}
          </p>
        )}
      </div>

      <div className="space-y-4 pt-4 border-t">
        <h3 className="text-lg font-medium">Auto-Update</h3>
        <div className="flex items-center gap-3">
          <Switch
            id="global-auto-update"
            checked={effectiveAutoUpdate}
            onCheckedChange={handleAutoUpdateChange}
          />
          <div>
            <Label htmlFor="global-auto-update">Enable Auto-Update</Label>
            <p className="text-xs text-muted-foreground">
              Master switch for automatic container updates. When on, you can opt in individual containers or stacks.
            </p>
          </div>
        </div>
        {effectiveAutoUpdate && (
          <div className="flex items-start gap-2 rounded-lg border border-yellow-200 bg-yellow-50 p-3 dark:border-yellow-900 dark:bg-yellow-950">
            <AlertCircle className="h-4 w-4 mt-0.5 text-yellow-600 dark:text-yellow-400 shrink-0" />
            <p className="text-sm text-yellow-800 dark:text-yellow-200">
              Only containers and stacks with auto-update turned on will be updated. Updates happen when new images are detected during scans and may cause brief service interruption.
            </p>
          </div>
        )}
        {!effectiveAutoUpdate && (
          <div className="flex items-start gap-2 rounded-lg border bg-muted/50 p-3">
            <AlertCircle className="h-4 w-4 mt-0.5 text-muted-foreground shrink-0" />
            <p className="text-sm text-muted-foreground">
              Auto-update is off. Per-container and per-stack auto-update toggles are locked until this is enabled.
            </p>
          </div>
        )}
      </div>

      {stats && (
        <div className="space-y-2 pt-4 border-t">
          <h3 className="text-sm font-medium text-muted-foreground">Statistics</h3>
          <p className="text-sm">
            {stats.enabledContainers} container{stats.enabledContainers !== 1 ? 's' : ''} with auto-update enabled
          </p>
          <p className="text-sm text-muted-foreground">
            {stats.updatesLast7Days} update{stats.updatesLast7Days !== 1 ? 's' : ''} in the last 7 days,{' '}
            {stats.updatesLast30Days} in the last 30 days
          </p>
        </div>
      )}
    </div>
  )
}

function GitSettingsContent() {
  const { data: gitSettings, isLoading } = useGitSettings()
  const updateGitSettings = useUpdateGitSettings()

  const [sshKey, setSshKey] = useState<string | undefined>(undefined)
  const [httpsUser, setHttpsUser] = useState<string | undefined>(undefined)
  const [httpsToken, setHttpsToken] = useState('')

  const effectiveSshKey = sshKey !== undefined ? sshKey : (gitSettings?.sshKey || '')
  const effectiveHttpsUser = httpsUser !== undefined ? httpsUser : (gitSettings?.httpsUser || '')

  if (isLoading) {
    return <div className="py-4"><LoadingSpinner /></div>
  }

  const handleSave = () => {
    const data: { sshKey?: string; httpsUser?: string; httpsToken?: string } = {}
    if (effectiveSshKey) data.sshKey = effectiveSshKey
    if (effectiveHttpsUser) data.httpsUser = effectiveHttpsUser
    if (httpsToken) data.httpsToken = httpsToken
    updateGitSettings.mutate(data, {
      onSuccess: () => {
        toast.success('Git settings saved')
        setHttpsToken('')
      },
      onError: () => toast.error('Failed to save git settings'),
    })
  }

  return (
    <form onSubmit={(e) => { e.preventDefault(); handleSave() }} className="space-y-6">
      <div className="space-y-4">
        <h3 className="text-lg font-medium">SSH</h3>
        <div className="space-y-2">
          <Label htmlFor="git-ssh-key">SSH Private Key Path</Label>
          <Input
            id="git-ssh-key"
            type="text"
            placeholder="/path/to/id_rsa (inside container)"
            value={effectiveSshKey}
            onChange={(e) => setSshKey(e.target.value)}
            className="max-w-md"
          />
          <p className="text-xs text-muted-foreground">
            Path to the default SSH private key used for git operations. Must be accessible inside the container.
          </p>
        </div>
      </div>

      <div className="space-y-4 pt-4 border-t">
        <h3 className="text-lg font-medium">HTTPS</h3>
        <div className="space-y-2">
          <Label htmlFor="git-https-user">Username</Label>
          <Input
            id="git-https-user"
            type="text"
            placeholder="git"
            value={effectiveHttpsUser}
            onChange={(e) => setHttpsUser(e.target.value)}
            className="max-w-md"
          />
        </div>
        <div className="space-y-2">
          <Label htmlFor="git-https-token">
            Personal Access Token
            {gitSettings?.hasHttpsToken && (
              <span className="ml-2 text-xs text-muted-foreground font-normal">(currently set)</span>
            )}
          </Label>
          <Input
            id="git-https-token"
            type="password"
            placeholder={gitSettings?.hasHttpsToken ? 'Leave blank to keep current token' : 'ghp_xxxx or glpat-xxxx'}
            value={httpsToken}
            onChange={(e) => setHttpsToken(e.target.value)}
            className="max-w-md"
          />
          <p className="text-xs text-muted-foreground">
            Used as the default token for HTTPS git remotes. Individual stack credentials override these.
          </p>
        </div>
      </div>

      <Button type="submit" disabled={updateGitSettings.isPending}>
        {updateGitSettings.isPending ? (
          <>
            <span className="mr-2"><LoadingSpinner size="small" /></span>
            Saving...
          </>
        ) : (
          'Save Git Settings'
        )}
      </Button>
    </form>
  )
}

function DirectoriesSettingsContent() {
  const { data: config, isLoading } = useQuery({
    queryKey: ['config'],
    queryFn: async () => {
      const { authApi } = await import('@/lib/api')
      return authApi.getConfig()
    },
  })
  const queryClient = useQueryClient()
  const [defaultDir, setDefaultDir] = useState('')
  const [initialized, setInitialized] = useState(false)

  useEffect(() => {
    if (config && !initialized) {
      setDefaultDir(config.stacksDir || '')
      setInitialized(true)
    }
  }, [config, initialized])

  if (isLoading) {
    return <div className="py-4"><LoadingSpinner /></div>
  }

  const allDirs = config?.stacksDirectories || []
  const effectiveDefault = initialized ? defaultDir : (config?.stacksDir || '')

  const handleSaveDefault = () => {
    import('@/lib/api').then(({ directoryConfigApi }) => {
      directoryConfigApi.update({ defaultDir: effectiveDefault }).then(() => {
        toast.success('Default directory updated')
        queryClient.invalidateQueries({ queryKey: ['config'] })
      }).catch(() => {
        toast.error('Failed to update default directory')
      })
    })
  }

  return (
    <div className="space-y-6">
      <div className="space-y-4">
        <h3 className="text-lg font-medium">Monitored Directories</h3>
        {allDirs.length > 0 ? (
          <div className="space-y-2">
            {allDirs.map((dir: string, index: number) => (
              <div key={dir} className="flex items-center gap-3 p-3 rounded-md border bg-muted/30">
                <div className="flex-1 min-w-0">
                  <div className="flex items-center gap-2">
                    <span className="text-sm font-medium truncate">{dir.split('/').filter(Boolean).pop() || dir}</span>
                    {index === 0 && (
                      <Badge variant="secondary" className="text-[10px] px-1.5 py-0">Default</Badge>
                    )}
                  </div>
                  <p className="text-xs text-muted-foreground truncate">{dir}</p>
                </div>
              </div>
            ))}
          </div>
        ) : (
          <p className="text-sm text-muted-foreground">No directories configured</p>
        )}
        <p className="text-xs text-muted-foreground">
          Additional directories can be added via the EXTRA_STACKS_DIRS environment variable (comma-separated paths).
        </p>
      </div>

      {allDirs.length > 1 && (
        <div className="space-y-4 pt-4 border-t">
          <h3 className="text-lg font-medium">Default Stack Directory</h3>
          <div className="space-y-2">
            <Label htmlFor="default-dir">Default Directory for New Stacks</Label>
            <Select value={effectiveDefault} onValueChange={setDefaultDir}>
              <SelectTrigger id="default-dir" className="w-full max-w-md">
                <SelectValue placeholder="Select default directory" />
              </SelectTrigger>
              <SelectContent>
                {allDirs.map((dir: string) => (
                  <SelectItem key={dir} value={dir}>
                    {dir.split('/').filter(Boolean).pop() || dir} ({dir})
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <p className="text-xs text-muted-foreground">
              New stacks will be created in this directory by default unless changed in the creation dialog.
            </p>
          </div>
          <Button onClick={handleSaveDefault} disabled={effectiveDefault === config?.stacksDir}>
            Save Default Directory
          </Button>
        </div>
      )}
    </div>
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
    </div>
  )
}
