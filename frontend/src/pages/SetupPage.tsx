import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { LoginForm, type LoginFormData } from '@/components/auth/LoginForm'
import { useAuthStore } from '@/stores/authStore'
import { ThemeToggle } from '@/components/ThemeToggle'
import { toast } from 'sonner'

export function SetupPage() {
  const navigate = useNavigate()
  const setup = useAuthStore((state) => state.setup)
  const [isLoading, setIsLoading] = useState(false)
  const [error, setError] = useState<string | undefined>()

  const handleSubmit = async (data: LoginFormData) => {
    setIsLoading(true)
    setError(undefined)

    try {
      await setup(data.username, data.password)
      toast.success('Admin account created successfully')
      navigate('/')
    } catch (err: unknown) {
      const errorMessage = err instanceof Error ? err.message : 'Setup failed'
      setError(errorMessage)
      toast.error(errorMessage)
    } finally {
      setIsLoading(false)
    }
  }

  return (
    <div className="relative min-h-screen flex items-center justify-center p-4">
      <div className="absolute top-4 right-4">
        <ThemeToggle />
      </div>
      <Card className="w-full max-w-md">
        <CardHeader className="space-y-1">
          <CardTitle className="text-2xl font-bold">Setup Docker Manager</CardTitle>
          <CardDescription>Create your admin account to get started</CardDescription>
        </CardHeader>
        <CardContent>
          <LoginForm onSubmit={handleSubmit} isLoading={isLoading} error={error} buttonText="Create Account" />
        </CardContent>
      </Card>
    </div>
  )
}
