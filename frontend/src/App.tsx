import { useEffect } from 'react'
import { Routes, Route, Navigate, useLocation } from 'react-router-dom'
import { QueryClientProvider } from '@tanstack/react-query'
import { Toaster } from 'sonner'
import { queryClient } from '@/lib/query-client'
import { setAuthCallbacks } from '@/lib/api'
import { useAuth } from '@/hooks/useAuth'
import { LoginPage } from '@/pages/LoginPage'
import { SetupPage } from '@/pages/SetupPage'
import { DashboardPage } from '@/pages/DashboardPage'
import { StackPage } from '@/pages/StackPage'
import { SettingsPage } from '@/pages/SettingsPage'

function AuthGuard({ children }: { children: React.ReactNode }) {
  const { canAccess, checkAuth, checkStatus } = useAuth()
  const location = useLocation()

  useEffect(() => {
    const init = async () => {
      await checkStatus()
      if (sessionStorage.getItem('token')) {
        await checkAuth()
      }
    }
    init()
  }, [checkAuth, checkStatus])

  if (!canAccess) {
    return <Navigate to="/login" state={{ from: location }} replace />
  }

  return <>{children}</>
}

function App() {
  const { authDisabled, needsSetup, isAuthenticated } = useAuth()

  useEffect(() => {
    setAuthCallbacks(
      () => sessionStorage.getItem('token'),
      () => {
        sessionStorage.removeItem('token')
        window.location.href = '/login'
      },
    )
  }, [])

  if (authDisabled) {
    return (
      <QueryClientProvider client={queryClient}>
        <Routes>
          <Route path="/" element={<DashboardPage />} />
          <Route path="/stacks/:id" element={<StackPage />} />
          <Route path="/stacks/:id/:tab" element={<StackPage />} />
          <Route path="/settings" element={<SettingsPage />} />
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
        <Toaster />
      </QueryClientProvider>
    )
  }

  if (needsSetup && !isAuthenticated) {
    return (
      <QueryClientProvider client={queryClient}>
        <Routes>
          <Route path="/setup" element={<SetupPage />} />
          <Route path="*" element={<Navigate to="/setup" replace />} />
        </Routes>
        <Toaster />
      </QueryClientProvider>
    )
  }

  return (
    <QueryClientProvider client={queryClient}>
      <Routes>
        <Route path="/login" element={<LoginPage />} />
        <Route
          path="/"
          element={
            <AuthGuard>
              <DashboardPage />
            </AuthGuard>
          }
        />
        <Route
          path="/stacks/:id"
          element={
            <AuthGuard>
              <StackPage />
            </AuthGuard>
          }
        />
        <Route
          path="/stacks/:id/:tab"
          element={
            <AuthGuard>
              <StackPage />
            </AuthGuard>
          }
        />
        <Route
          path="/settings"
          element={
            <AuthGuard>
              <SettingsPage />
            </AuthGuard>
          }
        />
        <Route path="*" element={<Navigate to="/login" replace />} />
      </Routes>
      <Toaster />
    </QueryClientProvider>
  )
}

export default App
