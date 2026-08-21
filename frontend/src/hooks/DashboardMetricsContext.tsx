import { createContext, useContext } from 'react'
import type { useDashboardMetrics } from './useDashboardMetrics'

export type DashboardMetricsValue = ReturnType<typeof useDashboardMetrics>

/**
 * Carries the single app-wide dashboard-metrics WebSocket (opened by
 * DashboardMetricsProvider in the app shell) to the header vitals and the
 * dashboard metrics tab, so they share one connection.
 */
export const DashboardMetricsContext = createContext<DashboardMetricsValue | null>(null)

export function useDashboardMetricsContext(): DashboardMetricsValue {
  const ctx = useContext(DashboardMetricsContext)
  if (!ctx) {
    throw new Error('useDashboardMetricsContext must be used within DashboardMetricsProvider')
  }
  return ctx
}
