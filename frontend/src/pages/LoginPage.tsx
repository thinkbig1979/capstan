import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { LoginForm, type LoginFormData } from '@/components/auth/LoginForm'
import { useAuthStore } from '@/stores/authStore'
import { toast } from 'sonner'

export function LoginPage() {
  const navigate = useNavigate()
  const login = useAuthStore((state) => state.login)
  const [isLoading, setIsLoading] = useState(false)
  const [error, setError] = useState<string | undefined>()

  const handleSubmit = async (data: LoginFormData) => {
    setIsLoading(true)
    setError(undefined)

    try {
      await login(data.username, data.password)
      toast.success('Logged in successfully')
      navigate('/')
    } catch (err: unknown) {
      const errorMessage = err instanceof Error ? err.message : 'Login failed'
      setError(errorMessage)
      toast.error(errorMessage)
    } finally {
      setIsLoading(false)
    }
  }

  return (
    <div className="min-h-screen flex items-center justify-center p-4">
      <Card className="w-full max-w-md">
        <CardHeader className="space-y-1">
          <CardTitle className="text-2xl font-bold">Login</CardTitle>
          <CardDescription>Enter your credentials to access Docker Manager</CardDescription>
        </CardHeader>
        <CardContent>
          <LoginForm onSubmit={handleSubmit} isLoading={isLoading} error={error} />
        </CardContent>
      </Card>
    </div>
  )
}
