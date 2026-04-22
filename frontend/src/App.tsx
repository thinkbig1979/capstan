import { useEffect, useState, Suspense, lazy } from 'react'
import { Routes, Route, Navigate, useLocation } from 'react-router-dom'
import { QueryClientProvider } from '@tanstack/react-query'
import { Toaster } from 'sonner'
import { queryClient } from '@/lib/query-client'
import { setAuthCallbacks } from '@/lib/api'
import { useAuth } from '@/hooks/useAuth'
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

function AuthGuard({ children }: { children: React.ReactNode }) {
  const { canAccess, checkAuth, checkStatus } = useAuth()
  const location = useLocation()

  useEffect(() => {
    const init = async () => {
      await checkStatus()
      await checkAuth()
    }
    init()
  }, [checkAuth, checkStatus])

  if (!canAccess) {
    return <Navigate to="/login" state={{ from: location }} replace />
  }

  return <>{children}</>
}

function App() {
  const { authDisabled, needsSetup, isAuthenticated, checkStatus } = useAuth()
  const [statusChecked, setStatusChecked] = useState(false)

  useEffect(() => {
    setAuthCallbacks(
      () => null,
      () => {
        window.location.href = '/login'
      },
    )
  }, [])

  useEffect(() => {
    const init = async () => {
      await checkStatus()
      setStatusChecked(true)
    }
    init()
  }, [])

  if (!statusChecked) {
    return (
      <div className="flex items-center justify-center min-h-screen">
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
            <Suspense fallback={<div className="flex items-center justify-center min-h-screen"><LoadingSpinner size="large" /></div>}>
              <Routes>
                <Route path="/" element={<DashboardPage />} />
                <Route path="/stacks/:id" element={<StackPage />} />
                <Route path="/stacks/:id/:tab" element={<StackPage />} />
                <Route path="/settings" element={<SettingsPage />} />
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
          <AppShell>
            <Routes>
              <Route path="/setup" element={<SetupPage />} />
              <Route path="*" element={<Navigate to="/setup" replace />} />
            </Routes>
          </AppShell>
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
          <Route
            path="/"
            element={
              <AppShell>
                <AuthGuard>
                  <Suspense fallback={<div className="flex items-center justify-center min-h-screen"><LoadingSpinner size="large" /></div>}>
                    <DashboardPage />
                  </Suspense>
                </AuthGuard>
              </AppShell>
            }
          />
          <Route
            path="/stacks/:id"
            element={
              <AppShell>
                <AuthGuard>
                  <Suspense fallback={<div className="flex items-center justify-center min-h-screen"><LoadingSpinner size="large" /></div>}>
                    <StackPage />
                  </Suspense>
                </AuthGuard>
              </AppShell>
            }
          />
          <Route
            path="/stacks/:id/:tab"
            element={
              <AppShell>
                <AuthGuard>
                  <Suspense fallback={<div className="flex items-center justify-center min-h-screen"><LoadingSpinner size="large" /></div>}>
                    <StackPage />
                  </Suspense>
                </AuthGuard>
              </AppShell>
            }
          />
          <Route
            path="/settings"
            element={
              <AppShell>
                <AuthGuard>
                  <Suspense fallback={<div className="flex items-center justify-center min-h-screen"><LoadingSpinner size="large" /></div>}>
                    <SettingsPage />
                  </Suspense>
                </AuthGuard>
              </AppShell>
            }
          />
          <Route path="*" element={<Navigate to="/login" replace />} />
        </Routes>
      </ErrorBoundary>
      <Toaster />
    </QueryClientProvider>
  )
}

export default App
