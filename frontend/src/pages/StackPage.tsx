import { AppShell } from '@/components/layout/AppShell'

export function StackPage() {
  return (
    <AppShell>
      <div className="space-y-6">
        <div>
          <h1 className="text-3xl font-bold tracking-tight">Stack Details</h1>
          <p className="text-muted-foreground">View and manage your Docker Compose stack</p>
        </div>

        <div className="rounded-lg border bg-card text-card-foreground shadow-sm p-6">
          <p className="text-sm text-muted-foreground">Stack management coming soon...</p>
        </div>
      </div>
    </AppShell>
  )
}
