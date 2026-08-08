import { useEffect, useState, Suspense, lazy } from 'react'
import { Routes, Route, Navigate, useLocation, Outlet } from 'react-router'
import { QueryClientProvider } from '@tanstack/react-query'
import { Toaster } from 'sonner'
import { queryClient } from '@/lib/query-client'
import { setAuthCallbacks } from '@/lib/api'
import { useAuth } from '@/hooks/useAuth'
import { useEnvUnlockCacheSync } from '@/hooks/useEnvUnlockCacheSync'
import { AppShell } from '@/components/layout/AppShell'
import { ErrorBoundary } from '@/components/ErrorBoundary'
import { LoadingSpinner } from '@/components/LoadingSkeleton'
import { LoginPage } from '@/pages/LoginPage'
import { SetupPage } from '@/pages/SetupPage'

const DashboardPage = lazy(() =>
  import('@/pages/DashboardPage').then((m) => ({ default: m.DashboardPage }))
)
const StackPage = lazy(() =>
  import('@/pages/StackPage').then((m) => ({ default: m.StackPage }))
)
const SettingsPage = lazy(() =>
  import('@/pages/SettingsPage').then((m) => ({ default: m.SettingsPage }))
)

const suspendedFallback = (
  <div className="flex items-center justify-center min-h-dvh">
    <LoadingSpinner size="large" />
  </div>
)

function AuthGuard({ children }: { children: React.ReactNode }) {
  const { canAccess } = useAuth()
  const location = useLocation()

  if (!canAccess) {
    return <Navigate to="/login" state={{ from: location }} replace />
  }

  return <>{children}</>
}

function AuthenticatedLayout() {
  return (
    <AppShell>
      <AuthGuard>
        <Suspense fallback={suspendedFallback}>
          <Outlet />
        </Suspense>
      </AuthGuard>
    </AppShell>
  )
}

function App() {
  const { authDisabled, needsSetup, isAuthenticated, checkStatus, checkAuth } = useAuth()
  const [statusChecked, setStatusChecked] = useState(false)

  // Purges cached plaintext secrets when the env-unlock window closes.
  useEnvUnlockCacheSync()

  useEffect(() => {
    setAuthCallbacks(
      () => null,
      () => {
        window.location.href = '/login'
      },
    )
  }, [])

  useEffect(() => {
    let ignore = false
    const init = async () => {
      await checkStatus()
      await checkAuth()
      if (!ignore) setStatusChecked(true)
    }
    init()
    return () => {
      ignore = true
    }
  }, [checkStatus, checkAuth])

  if (!statusChecked) {
    return (
      <div className="flex items-center justify-center min-h-dvh">
        <div className="flex flex-col items-center gap-4">
          <LoadingSpinner size="large" />
          <div className="text-lg">Loading...</div>
        </div>
      </div>
    )
  }

  if (authDisabled) {
    return (
      <QueryClientProvider client={queryClient}>
        <ErrorBoundary>
          <AppShell>
            <Suspense fallback={suspendedFallback}>
              <Routes>
                <Route path="/" element={<DashboardPage />} />
                <Route path="/stacks/:id" element={<StackPage />} />
                <Route path="/stacks/:id/:tab" element={<StackPage />} />
                <Route path="/settings" element={<SettingsPage />} />
                <Route path="/settings/:section" element={<SettingsPage />} />
                <Route path="*" element={<Navigate to="/" replace />} />
              </Routes>
            </Suspense>
          </AppShell>
        </ErrorBoundary>
        <Toaster />
      </QueryClientProvider>
    )
  }

  if (needsSetup && !isAuthenticated) {
    return (
      <QueryClientProvider client={queryClient}>
        <ErrorBoundary>
          <Routes>
            <Route path="/setup" element={<SetupPage />} />
            <Route path="*" element={<Navigate to="/setup" replace />} />
          </Routes>
        </ErrorBoundary>
        <Toaster />
      </QueryClientProvider>
    )
  }

  return (
    <QueryClientProvider client={queryClient}>
      <ErrorBoundary>
        <Routes>
          <Route path="/login" element={<LoginPage />} />
          <Route element={<AuthenticatedLayout />}>
            <Route path="/" element={<DashboardPage />} />
            <Route path="/stacks/:id" element={<StackPage />} />
            <Route path="/stacks/:id/:tab" element={<StackPage />} />
            <Route path="/settings" element={<SettingsPage />} />
            <Route path="/settings/:section" element={<SettingsPage />} />
          </Route>
          <Route path="*" element={<Navigate to="/login" replace />} />
        </Routes>
      </ErrorBoundary>
      <Toaster />
    </QueryClientProvider>
  )
}

export { App }
