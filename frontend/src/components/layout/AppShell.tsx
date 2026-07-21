import type { ReactNode } from 'react'
import { Sidebar } from './Sidebar'
import { Header } from './Header'
import { useStackEvents } from '@/hooks/useStackEvents'
import { useUpdateScanWatcher } from '@/hooks/useResources'
import { CommandPalette } from '@/components/CommandPalette'

interface AppShellProps {
  children: ReactNode
}

export function AppShell({ children }: AppShellProps) {
  useStackEvents()
  // App-wide watcher: keeps the update scan visible (toast) and self-clearing
  // even after navigating away from the Updates tab.
  useUpdateScanWatcher()

  return (
    <div className="flex min-h-dvh bg-background">
      <Sidebar />
      <div className="flex-1 flex flex-col overflow-hidden min-w-0">
        <Header />
        <main className="flex-1 overflow-y-auto p-4 md:p-6 lg:p-6">{children}</main>
      </div>
      <CommandPalette />
    </div>
  )
}
