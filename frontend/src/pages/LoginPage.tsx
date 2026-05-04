import { useAuthStore } from '@/stores/authStore'
import { AuthPage } from '@/components/auth/AuthPage'

export function LoginPage() {
  const login = useAuthStore((state) => state.login)

  return (
    <AuthPage
      title="Login"
      description="Enter your credentials to access Docker Manager"
      submitFn={login}
      successMessage="Logged in successfully"
      errorPrefix="Login"
    />
  )
}
