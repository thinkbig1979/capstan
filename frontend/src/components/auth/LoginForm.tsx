import { useState, type ChangeEvent, type FormEvent } from 'react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Loader2, Eye, EyeOff } from 'lucide-react'

export interface LoginFormData {
  username: string
  password: string
}

interface FormValues extends LoginFormData {
  confirmPassword: string
}

type FieldErrors = Partial<Record<keyof FormValues, string>>

// bcrypt's input limit, in UTF-8 bytes. The server (ValidatePassword) refuses
// anything longer, so Setup says so before the request (agent-os-orxo).
const MAX_PASSWORD_BYTES = 72
const utf8 = new TextEncoder()

function usernameError(username: string): string | undefined {
  if (username.length < 3) return 'Username must be at least 3 characters'
  if (username.length > 50) return 'Username must not exceed 50 characters'
  return undefined
}

function validateLogin(values: FormValues): FieldErrors {
  return {
    username: usernameError(values.username),
    password: values.password.length < 8 ? 'Password must be at least 8 characters' : undefined,
  }
}

// Mirrors the server rule (backend ValidatePassword), in the same order, so
// Setup gives immediate feedback instead of a late server rejection. The
// server additionally rejects common passwords; that blocklist stays
// server-side.
function setupPasswordError(password: string): string | undefined {
  if (password.length < 8) return 'Password must be at least 8 characters long'
  if (utf8.encode(password).length > MAX_PASSWORD_BYTES) {
    return 'Password must not exceed 72 bytes (accented and non-Latin characters count as 2-4 bytes each)'
  }
  if (!/\p{Lu}/u.test(password)) return 'Password must contain at least one uppercase letter'
  if (!/\p{Ll}/u.test(password)) return 'Password must contain at least one lowercase letter'
  if (!/\p{N}/u.test(password)) return 'Password must contain at least one number'
  if (!/[\p{P}\p{S}]/u.test(password)) return 'Password must contain at least one special character'
  return undefined
}

function validateSetup(values: FormValues): FieldErrors {
  let confirmPassword: string | undefined
  if (values.confirmPassword === '') confirmPassword = 'Please confirm your password'
  else if (values.password !== values.confirmPassword) confirmPassword = 'Passwords do not match'
  return {
    username: usernameError(values.username),
    password: setupPasswordError(values.password),
    confirmPassword,
  }
}

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
  const [values, setValues] = useState<FormValues>({ username: '', password: '', confirmPassword: '' })
  // Errors appear on the first submit and then follow every keystroke.
  const [submitted, setSubmitted] = useState(false)
  const [showPassword, setShowPassword] = useState(false)

  const validate = enforceComplexity ? validateSetup : validateLogin
  const errors: FieldErrors = submitted ? validate(values) : {}
  // Login reuses an existing credential; Setup creates a new one.
  const passwordAutoComplete = enforceComplexity ? 'new-password' : 'current-password'

  const setField = (field: keyof FormValues) => (e: ChangeEvent<HTMLInputElement>) =>
    setValues((v) => ({ ...v, [field]: e.target.value }))

  const handleSubmit = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault()
    setSubmitted(true)
    if (Object.values(validate(values)).some(Boolean)) return
    void onSubmit({ username: values.username, password: values.password })
  }

  return (
    <form onSubmit={handleSubmit} className="space-y-4">
      <div className="space-y-2">
        <Label htmlFor="username">Username</Label>
        <Input
          id="username"
          name="username"
          type="text"
          autoComplete="username"
          autoFocus
          value={values.username}
          onChange={setField('username')}
        />
        {errors.username && <p className="text-sm text-destructive">{errors.username}</p>}
      </div>

      <div className="space-y-2">
        <Label htmlFor="password">Password</Label>
        <div className="flex items-center gap-2">
          <Input
            id="password"
            name="password"
            type={showPassword ? 'text' : 'password'}
            autoComplete={passwordAutoComplete}
            className="flex-1"
            value={values.password}
            onChange={setField('password')}
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
        {errors.password && <p className="text-sm text-destructive">{errors.password}</p>}
      </div>

      {enforceComplexity && (
        <div className="space-y-2">
          <Label htmlFor="confirmPassword">Confirm Password</Label>
          <Input
            id="confirmPassword"
            name="confirmPassword"
            type={showPassword ? 'text' : 'password'}
            autoComplete="new-password"
            value={values.confirmPassword}
            onChange={setField('confirmPassword')}
          />
          {errors.confirmPassword && <p className="text-sm text-destructive">{errors.confirmPassword}</p>}
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
