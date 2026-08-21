import type { ReactNode } from 'react'
import { DashboardMetricsContext } from '@/hooks/DashboardMetricsContext'
import { useDashboardMetrics } from '@/hooks/useDashboardMetrics'

/** Opens the one shared dashboard-metrics WebSocket for the app shell. */
export function DashboardMetricsProvider({ children }: { children: ReactNode }) {
  const value = useDashboardMetrics()
  return (
    <DashboardMetricsContext.Provider value={value}>
      {children}
    </DashboardMetricsContext.Provider>
  )
}
