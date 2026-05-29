import { useAuthStore } from '@/stores/authStore'
import { AuthPage } from '@/components/auth/AuthPage'

export function SetupPage() {
  const setup = useAuthStore((state) => state.setup)

  return (
    <AuthPage
      title="Setup Capstan"
      description="Create your admin account to get started"
      submitFn={setup}
      successMessage="Admin account created successfully"
      errorPrefix="Setup"
      buttonText="Create Account"
      passwordHint="At least 8 characters, including uppercase, lowercase, number, and special character"
    />
  )
}
