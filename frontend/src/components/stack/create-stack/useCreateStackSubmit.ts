import { useCallback } from 'react'
import type { Dispatch, SetStateAction } from 'react'
import { useNavigate } from 'react-router-dom'
import { toast } from 'sonner'
import { useCreateStack, extractStack } from '@/hooks/useCreateStack'
import type { LintResult } from '@/types'
import { validateName } from './nameValidation'
import { DEFAULT_COMPOSE } from './constants'

interface UseCreateStackSubmitArgs {
  name: string
  setName: Dispatch<SetStateAction<string>>
  setNameError: Dispatch<SetStateAction<string>>
  selectedDir: string
  setSelectedDir: Dispatch<SetStateAction<string>>
  composeContent: string
  setComposeContent: Dispatch<SetStateAction<string>>
  envContent: string
  setEnvContent: Dispatch<SetStateAction<string>>
  showEnv: boolean
  setShowEnv: Dispatch<SetStateAction<boolean>>
  deploy: boolean
  nameError: string
  hasLintErrors: boolean
  lintResults: LintResult[]
  setLintResults: Dispatch<SetStateAction<LintResult[]>>
  setDockerRunInput: Dispatch<SetStateAction<string>>
  setConversionError: Dispatch<SetStateAction<string>>
  setPendingCompose: Dispatch<SetStateAction<string | null>>
  setComposeTab: Dispatch<SetStateAction<'editor' | 'docker-run'>>
  onOpenChange: (open: boolean) => void
}

export function useCreateStackSubmit({
  name,
  setName,
  setNameError,
  selectedDir,
  setSelectedDir,
  composeContent,
  setComposeContent,
  envContent,
  setEnvContent,
  showEnv,
  setShowEnv,
  deploy,
  nameError,
  hasLintErrors,
  lintResults,
  setLintResults,
  setDockerRunInput,
  setConversionError,
  setPendingCompose,
  setComposeTab,
  onOpenChange,
}: UseCreateStackSubmitArgs) {
  const navigate = useNavigate()
  const createMutation = useCreateStack()

  const handleNameChange = useCallback(
    (value: string) => {
      setName(value)
      setNameError(validateName(value))
    },
    [setName, setNameError],
  )

  // NOTE: intentionally does not clear nameError or deploy — matches the
  // original component's resetForm exactly (deploy carries over across a
  // create, since the dialog stays mounted between opens; nameError is
  // already '' by the time a create can succeed, since Create is disabled
  // while it isn't).
  const resetForm = useCallback(() => {
    setName('')
    setSelectedDir('')
    setComposeContent(DEFAULT_COMPOSE)
    setEnvContent('')
    setShowEnv(false)
    setLintResults([])
    setDockerRunInput('')
    setConversionError('')
    setPendingCompose(null)
    setComposeTab('editor')
  }, [
    setName,
    setSelectedDir,
    setComposeContent,
    setEnvContent,
    setShowEnv,
    setLintResults,
    setDockerRunInput,
    setConversionError,
    setPendingCompose,
    setComposeTab,
  ])

  const handleCreate = useCallback(() => {
    const error = validateName(name)
    if (error) {
      setNameError(error)
      toast.error(error)
      return
    }

    if (!composeContent.trim()) {
      toast.error('Compose content cannot be empty')
      return
    }

    createMutation.mutate(
      { name, directory: selectedDir || undefined, composeContent, envContent: showEnv ? envContent : undefined, deploy },
      {
        onSuccess: (data) => {
          // The hook's onSuccess has already fired the appropriate toast
          // (success/warning based on outcome). We handle UI navigation here.
          //
          // For partial outcome (created-not-deployed): close the dialog and
          // navigate to the stack so the user can see/fix it. The warning toast
          // from the hook is already visible.
          const stackObj = extractStack(data)
          if (stackObj?.id) {
            onOpenChange(false)
            navigate(`/stacks/${stackObj.id}`)
            resetForm()
          }
        },
        onError: (error: unknown) => {
          // If the hook detected a created-but-not-deployed partial outcome, it
          // already showed the warning toast and invalidated queries. Navigate
          // to the stack so the user can see it.
          const partial = error as { outcome?: string; details?: { stack?: { id: string } } }
          if (partial?.outcome === 'partial' && partial?.details?.stack?.id) {
            onOpenChange(false)
            navigate(`/stacks/${partial.details.stack.id}`)
            resetForm()
            return
          }

          // Genuine create failure: show inline lint errors if present.
          // The hook's onError already fired the appropriate error toast.
          const err = error as { error?: string; lintResults?: LintResult[] }
          if (err.lintResults && err.lintResults.length > 0) {
            setLintResults(err.lintResults)
          }
        },
      },
    )
  }, [name, selectedDir, composeContent, envContent, showEnv, deploy, createMutation, onOpenChange, navigate, resetForm, setNameError, setLintResults])

  const validationErrors: { field: string; message: string }[] = []
  if (nameError) {
    validationErrors.push({ field: 'name', message: nameError })
  }
  if (hasLintErrors) {
    validationErrors.push({
      field: 'compose',
      message: `${lintResults.filter((r) => r.level === 'error').length} lint error(s)`,
    })
  }

  const isCreateDisabled = createMutation.isPending || validationErrors.length > 0

  return {
    handleNameChange,
    resetForm,
    handleCreate,
    validationErrors,
    isCreateDisabled,
    isPending: createMutation.isPending,
  }
}
