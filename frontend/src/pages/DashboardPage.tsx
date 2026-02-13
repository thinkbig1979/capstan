import { AppShell } from '@/components/layout/AppShell'

export function DashboardPage() {
  return (
    <AppShell>
      <div className="space-y-6">
        <div>
          <h1 className="text-3xl font-bold tracking-tight">Dashboard</h1>
          <p className="text-muted-foreground">Welcome to Docker Manager</p>
        </div>

        <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-4">
          <div className="rounded-lg border bg-card text-card-foreground shadow-sm p-6">
            <div className="flex flex-row items-center justify-between space-y-0 pb-2">
              <h3 className="tracking-tight text-sm font-medium">Total Stacks</h3>
            </div>
            <div className="text-2xl font-bold">0</div>
          </div>
          <div className="rounded-lg border bg-card text-card-foreground shadow-sm p-6">
            <div className="flex flex-row items-center justify-between space-y-0 pb-2">
              <h3 className="tracking-tight text-sm font-medium">Running</h3>
            </div>
            <div className="text-2xl font-bold">0</div>
          </div>
          <div className="rounded-lg border bg-card text-card-foreground shadow-sm p-6">
            <div className="flex flex-row items-center justify-between space-y-0 pb-2">
              <h3 className="tracking-tight text-sm font-medium">Stopped</h3>
            </div>
            <div className="text-2xl font-bold">0</div>
          </div>
          <div className="rounded-lg border bg-card text-card-foreground shadow-sm p-6">
            <div className="flex flex-row items-center justify-between space-y-0 pb-2">
              <h3 className="tracking-tight text-sm font-medium">Containers</h3>
            </div>
            <div className="text-2xl font-bold">0</div>
          </div>
        </div>

        <div className="rounded-lg border bg-card text-card-foreground shadow-sm">
          <div className="flex flex-col space-y-1.5 p-6">
            <h3 className="text-2xl font-semibold leading-none tracking-tight">Quick Start</h3>
            <p className="text-sm text-muted-foreground">Get started by configuring your Docker stacks directory</p>
          </div>
          <div className="p-6 pt-0">
            <p className="text-sm text-muted-foreground">
              No stacks configured yet. Add your Docker Compose files to get started.
            </p>
          </div>
        </div>
      </div>
    </AppShell>
  )
}
