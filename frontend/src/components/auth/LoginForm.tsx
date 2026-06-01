import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Loader2, Eye, EyeOff } from 'lucide-react'

const usernameSchema = z.string().min(3, 'Username must be at least 3 characters').max(50)

const loginSchema = z.object({
  username: usernameSchema,
  password: z.string().min(8, 'Password must be at least 8 characters'),
})

// Mirrors the server rule (backend ValidatePassword) so Setup gives immediate
// feedback instead of a late server rejection. The server additionally rejects
// common passwords; that blocklist stays server-side.
const setupSchema = z
  .object({
    username: usernameSchema,
    password: z
      .string()
      .min(8, 'Password must be at least 8 characters long')
      .max(128, 'Password must not exceed 128 characters')
      .regex(/\p{Lu}/u, 'Password must contain at least one uppercase letter')
      .regex(/\p{Ll}/u, 'Password must contain at least one lowercase letter')
      .regex(/\p{N}/u, 'Password must contain at least one number')
      .regex(/[\p{P}\p{S}]/u, 'Password must contain at least one special character'),
    confirmPassword: z.string().min(1, 'Please confirm your password'),
  })
  .refine((d) => d.password === d.confirmPassword, {
    message: 'Passwords do not match',
    path: ['confirmPassword'],
  })

export type LoginFormData = z.infer<typeof loginSchema>
type FormValues = LoginFormData & { confirmPassword?: string }

interface LoginFormProps {
  onSubmit: (data: LoginFormData) => Promise<void>
  isLoading?: boolean
  error?: string
  buttonText?: string
  passwordHint?: string
  /** Enforce the full server password-complexity rule (Setup variant). */
  enforceComplexity?: boolean
}

export function LoginForm({
  onSubmit,
  isLoading = false,
  error,
  buttonText = 'Login',
  passwordHint = 'Minimum 8 characters',
  enforceComplexity = false,
}: LoginFormProps) {
  const {
    register,
    handleSubmit,
    formState: { errors },
  } = useForm<FormValues>({
    resolver: zodResolver(enforceComplexity ? setupSchema : loginSchema),
  })

  const [showPassword, setShowPassword] = useState(false)
  // Login reuses an existing credential; Setup creates a new one.
  const passwordAutoComplete = enforceComplexity ? 'new-password' : 'current-password'

  return (
    <form onSubmit={handleSubmit((data) => onSubmit(data))} className="space-y-4">
      <div className="space-y-2">
        <Label htmlFor="username">Username</Label>
        <Input
          id="username"
          type="text"
          autoComplete="username"
          autoFocus
          {...register('username')}
        />
        {errors.username && <p className="text-sm text-destructive">{errors.username.message}</p>}
      </div>

      <div className="space-y-2">
        <Label htmlFor="password">Password</Label>
        <div className="flex items-center gap-2">
          <Input
            id="password"
            type={showPassword ? 'text' : 'password'}
            autoComplete={passwordAutoComplete}
            className="flex-1"
            {...register('password')}
          />
          <Button
            type="button"
            variant="ghost"
            size="icon"
            onClick={() => setShowPassword((v) => !v)}
            title={showPassword ? 'Hide password' : 'Reveal password'}
            aria-label={showPassword ? 'Hide password' : 'Reveal password'}
          >
            {showPassword ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
          </Button>
        </div>
        <p className="text-sm text-muted-foreground">{passwordHint}</p>
        {errors.password && <p className="text-sm text-destructive">{errors.password.message}</p>}
      </div>

      {enforceComplexity && (
        <div className="space-y-2">
          <Label htmlFor="confirmPassword">Confirm Password</Label>
          <Input
            id="confirmPassword"
            type={showPassword ? 'text' : 'password'}
            autoComplete="new-password"
            {...register('confirmPassword')}
          />
          {errors.confirmPassword && (
            <p className="text-sm text-destructive">{errors.confirmPassword.message}</p>
          )}
        </div>
      )}

      {error && <p className="text-sm text-destructive">{error}</p>}

      <Button type="submit" className="w-full" disabled={isLoading}>
        {isLoading ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : null}
        {buttonText}
      </Button>
    </form>
  )
}
