import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { AppShell } from '@/components/layout/AppShell'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { useAuth } from '@/hooks/useAuth'
import { toast } from 'sonner'
import { Eye, EyeOff } from 'lucide-react'

function maskValue(value: string): string {
  if (value.length <= 4) {
    return '*'.repeat(value.length)
  }
  const visibleChars = 2
  return value.slice(0, visibleChars) + '*'.repeat(value.length - visibleChars)
}

function GlobalEnvContent({ globalEnv, isLoadingEnv }: { globalEnv?: { vars: { key: string; value: string }[] }; isLoadingEnv: boolean }) {
  const [showValues, setShowValues] = useState<Record<number, boolean>>({})
  const [authPassword, setAuthPassword] = useState('')
  const [showAuthDialog, setShowAuthDialog] = useState(false)
  const [pendingIndex, setPendingIndex] = useState<number | null>(null)

  if (isLoadingEnv) {
    return <div className="text-sm text-muted-foreground">Loading environment variables...</div>
  }

  if (!globalEnv?.vars?.length) {
    return (
      <div className="text-sm text-muted-foreground">
        No global environment variables configured. Create a file at <code className="bg-muted px-1 rounded">/opt/stacks/global.env</code> to add variables.
      </div>
    )
  }

  const handleToggle = (index: number) => {
    if (showValues[index]) {
      setShowValues(prev => ({ ...prev, [index]: false }))
    } else {
      setPendingIndex(index)
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
      if (pendingIndex !== null) {
        setShowValues(prev => ({ ...prev, [pendingIndex]: true }))
      }
      setShowAuthDialog(false)
      setAuthPassword('')
      setPendingIndex(null)
      toast.success('Password verified')
    } catch (error) {
      toast.error('Invalid password')
    }
  }

  return (
    <>
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
  const { user } = useAuth()
  const [currentPassword, setCurrentPassword] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')
  const [isChangingPassword, setIsChangingPassword] = useState(false)

  const { data: globalEnv, isLoading: isLoadingEnv } = useQuery({
    queryKey: ['settings', 'global-env'],
    queryFn: async () => {
      const { authApi } = await import('@/lib/api')
      return authApi.getGlobalEnv()
    },
    staleTime: 30_000,
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

    setIsChangingPassword(true)
    try {
      const { authApi } = await import('@/lib/api')
      await authApi.changePassword(currentPassword, newPassword)
      toast.success('Password changed successfully')
      setCurrentPassword('')
      setNewPassword('')
      setConfirmPassword('')
    } catch (error: any) {
      toast.error(error?.message || 'Failed to change password')
    } finally {
      setIsChangingPassword(false)
    }
  }

  return (
    <AppShell>
      <div className="max-w-4xl mx-auto space-y-6">
        <div>
          <h1 className="text-3xl font-bold tracking-tight">Settings</h1>
          <p className="text-muted-foreground">Manage your account and application settings</p>
        </div>

        <Card>
          <CardHeader>
            <CardTitle>Account</CardTitle>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="username">Username</Label>
              <Input id="username" value={user?.username || ''} disabled className="bg-muted" />
            </div>

            <div className="space-y-2">
              <Label htmlFor="created">Created</Label>
              <Input
                id="created"
                value={user?.created_at ? new Date(user.created_at).toLocaleDateString() : 'N/A'}
                disabled
                className="bg-muted"
              />
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Change Password</CardTitle>
          </CardHeader>
          <CardContent>
            <form onSubmit={handlePasswordChange} className="space-y-4">
              <div className="space-y-2">
                <Label htmlFor="current-password">Current Password</Label>
                <Input
                  id="current-password"
                  type="password"
                  value={currentPassword}
                  onChange={(e) => setCurrentPassword(e.target.value)}
                  required
                  className="min-h-[44px]"
                />
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
                  className="min-h-[44px]"
                />
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
                  className="min-h-[44px]"
                />
              </div>

              <Button type="submit" disabled={isChangingPassword} className="min-h-[44px]">
                {isChangingPassword ? 'Changing...' : 'Change Password'}
              </Button>
            </form>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Appearance</CardTitle>
          </CardHeader>
          <CardContent>
            <p className="text-sm text-muted-foreground">
              Theme selection is available in the header using the theme toggle button.
            </p>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Global Environment Variables</CardTitle>
          </CardHeader>
          <CardContent>
            <GlobalEnvContent globalEnv={globalEnv} isLoadingEnv={isLoadingEnv} />
          </CardContent>
        </Card>
      </div>
    </AppShell>
  )
}
