import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { LoginForm, type LoginFormData } from '@/components/auth/LoginForm'
import { ThemeToggle } from '@/components/ThemeToggle'
import { Logo } from '@/components/Logo'
import { toast } from 'sonner'

interface AuthPageProps {
  title: string
  description: string
  submitFn: (username: string, password: string) => Promise<void>
  successMessage: string
  errorPrefix: string
  buttonText?: string
  passwordHint?: string
  enforceComplexity?: boolean
}

function extractErrorMessage(err: unknown, fallback: string): string {
  if (err instanceof Error) return err.message
  if (err && typeof err === 'object') {
    const obj = err as Record<string, unknown>
    if (typeof obj.message === 'string' && obj.message) return obj.message
    if (typeof obj.error === 'string' && obj.error) return obj.error
  }
  return fallback
}

export function AuthPage({ title, description, submitFn, successMessage, errorPrefix, buttonText, passwordHint, enforceComplexity }: AuthPageProps) {
  const navigate = useNavigate()
  const [isLoading, setIsLoading] = useState(false)
  const [error, setError] = useState<string | undefined>()

  const handleSubmit = async (data: LoginFormData) => {
    setIsLoading(true)
    setError(undefined)

    try {
      await submitFn(data.username, data.password)
      toast.success(successMessage)
      navigate('/')
    } catch (err: unknown) {
      // Surface auth/validation failures inline next to the form only; a toast
      // here would duplicate the same message (AU-4).
      const errorMessage = extractErrorMessage(err, `${errorPrefix} failed`)
      setError(errorMessage)
    } finally {
      setIsLoading(false)
    }
  }

  return (
    <div className="relative min-h-dvh flex flex-col items-center justify-center p-4">
      <div className="absolute top-4 right-4">
        <ThemeToggle />
      </div>
      <div className="mb-6 flex flex-col items-center gap-3">
        <Logo className="h-12 w-12" />
        <span className="text-xl font-semibold tracking-tight">Capstan</span>
      </div>
      <Card className="w-full max-w-md">
        <CardHeader className="space-y-1">
          <CardTitle className="text-2xl font-bold">{title}</CardTitle>
          <CardDescription>{description}</CardDescription>
        </CardHeader>
        <CardContent>
          <LoginForm
            onSubmit={handleSubmit}
            isLoading={isLoading}
            error={error}
            buttonText={buttonText}
            passwordHint={passwordHint}
            enforceComplexity={enforceComplexity}
          />
        </CardContent>
      </Card>
    </div>
  )
}
