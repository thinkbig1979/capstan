import { useAuthStore } from '@/stores/authStore'

export function useAuth() {
  const isAuthenticated = useAuthStore((state) => state.isAuthenticated)
  const authDisabled = useAuthStore((state) => state.authDisabled)
  const needsSetup = useAuthStore((state) => state.needsSetup)
  const user = useAuthStore((state) => state.user)
  const login = useAuthStore((state) => state.login)
  const setup = useAuthStore((state) => state.setup)
  const logout = useAuthStore((state) => state.logout)
  const checkAuth = useAuthStore((state) => state.checkAuth)
  const checkStatus = useAuthStore((state) => state.checkStatus)

  const canAccess = authDisabled || isAuthenticated

  return {
    isAuthenticated,
    authDisabled,
    needsSetup,
    user,
    canAccess,
    login,
    setup,
    logout,
    checkAuth,
    checkStatus,
  }
}
