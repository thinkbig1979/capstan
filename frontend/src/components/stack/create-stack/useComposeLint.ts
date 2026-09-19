import { useCallback } from 'react'
import type { Dispatch, SetStateAction } from 'react'
import { toast } from 'sonner'
import { presentError, toastInvalid } from '@/lib/error-handler'
import { stacksApi } from '@/lib/api'
import type { LintResult } from '@/types'

interface UseComposeLintArgs {
  composeContent: string
  lintResults: LintResult[]
  setLintResults: Dispatch<SetStateAction<LintResult[]>>
}

export function useComposeLint({ composeContent, lintResults, setLintResults }: UseComposeLintArgs) {
  const handleLint = useCallback(async () => {
    try {
      const data = await stacksApi.lint(composeContent)
      setLintResults(data.lintResults || [])

      if (data.lintResults?.some((r: LintResult) => r.level === 'error')) {
        toastInvalid('Lint errors detected')
      } else if (data.lintResults?.some((r: LintResult) => r.level === 'warning')) {
        toast.warning('Lint warnings detected')
      } else {
        toast.success('No lint issues found')
      }
    } catch (err) {
      presentError(err, { fallback: 'Failed to lint compose file' })
    }
  }, [composeContent, setLintResults])

  const hasLintErrors = lintResults.some((r) => r.level === 'error')

  return { handleLint, hasLintErrors }
}
