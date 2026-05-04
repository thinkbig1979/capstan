import { useAuthStore } from '@/stores/authStore'
import { AuthPage } from '@/components/auth/AuthPage'

export function SetupPage() {
  const setup = useAuthStore((state) => state.setup)

  return (
    <AuthPage
      title="Setup Docker Manager"
      description="Create your admin account to get started"
      submitFn={setup}
      successMessage="Admin account created successfully"
      errorPrefix="Setup"
      buttonText="Create Account"
    />
  )
}
