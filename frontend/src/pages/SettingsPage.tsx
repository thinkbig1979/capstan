import { AppShell } from '@/components/layout/AppShell'

export function SettingsPage() {
  return (
    <AppShell>
      <div className="space-y-6">
        <div>
          <h1 className="text-3xl font-bold tracking-tight">Settings</h1>
          <p className="text-muted-foreground">Configure your Docker Manager preferences</p>
        </div>

        <div className="rounded-lg border bg-card text-card-foreground shadow-sm p-6">
          <p className="text-sm text-muted-foreground">Settings coming soon...</p>
        </div>
      </div>
    </AppShell>
  )
}
