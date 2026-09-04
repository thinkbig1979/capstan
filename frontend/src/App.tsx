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

// AuthGuard wraps AppShell, not the other way round: the shell mounts Sidebar
// (4 queries via useSidebarData) and Header, whose HeaderVitals issues a 5th
// (dashboardApi.stats -> /api/v1/dashboard/stats). Each fires on mount, so with
// the shell outside the guard all five ran against a dead session before the
// redirect to /login could unmount them, measured 2026-09-02 as 5 wasted 401s
// per logged-out load of `/`. The shell's two WebSockets were never part of it:
// useWebSocket.ts:92 returns early when unauthenticated, so they self-guarded
// and the damage was HTTP-only. Gating the mount fixes every query at once and
// cannot be defeated by a sixth one added to the shell later.
function AuthenticatedLayout() {
  return (
    <AuthGuard>
      <AppShell>
        <Suspense fallback={suspendedFallback}>
          <Outlet />
        </Suspense>
      </AppShell>
    </AuthGuard>
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
    // Nothing in here may leave statusChecked false: the !statusChecked branch
    // below is a full-page spinner with no retry and no timeout, so a rejection
    // that skipped the flag stranded the app there until the user reloaded by
    // hand. Both probes swallow their own failures, and the catch keeps init()
    // from rejecting unhandled if some future await in here does not -- a
    // `finally` alone would set the flag but still reject.
    //
    // This reaches a renderable state; it does not recover. Nothing re-probes
    // (this effect is the only call site, and its deps are stable Zustand
    // actions), so a failed boot leaves the user on the login page until they
    // reload by hand. Deliberately minimal; a retry path is agent-os-a4eh.
    //
    // DEV ONLY (agent-os-lqsa): StrictMode's dev double-invoke (main.tsx)
    // can have this effect's setup run twice in close succession (mount,
    // cleanup, remount) before either probe's network call settles. That
    // used to mean two independent checkStatus/checkAuth calls racing to
    // write the store, where a superseded invocation's late result could
    // clobber the surviving one's. Fixed at the store level instead of here
    // (authStore.ts's checkStatus/checkAuth now de-duplicate concurrent
    // calls into a single shared in-flight request), so both invocations of
    // this effect end up awaiting the SAME promise and applying the SAME
    // result -- nothing here needs to change to benefit from that.
    const init = async () => {
      try {
        await checkStatus()
        await checkAuth()
      } catch (error) {
        const isDev = import.meta.env.DEV
        if (isDev) {
          console.error('Auth initialisation failed:', error)
        }
      } finally {
        if (!ignore) setStatusChecked(true)
      }
    }
    void init()
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
