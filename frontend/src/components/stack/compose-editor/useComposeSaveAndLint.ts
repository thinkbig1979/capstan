import { useCallback, useEffect, useState } from 'react'
import type { RefObject } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { apiClient } from '@/lib/api'
import { classifyError } from '@/lib/error-handler'
import { toast } from 'sonner'
import type { LintResult } from '@/types'
import type { useCodeMirrorEditor } from '@/hooks/useCodeMirrorEditor'

interface UseComposeSaveAndLintArgs {
  stackId: string
  viewRef: ReturnType<typeof useCodeMirrorEditor>['viewRef']
  handleSaveRef: RefObject<(forceSave?: boolean) => void>
  setLastSaved: (content: string) => void
}

/**
 * Owns the save + lint mutations and the lint-before-save decision (audit
 * finding-adjacent behavior, not touched here): a normal save first POSTs to
 * /compose/lint; if that lint run reports errors, a confirm dialog is shown
 * instead of saving; if the lint request itself fails, the save proceeds
 * directly (no confirm dialog, no error surfaced).
 */
export function useComposeSaveAndLint({
  stackId,
  viewRef,
  handleSaveRef,
  setLastSaved,
}: UseComposeSaveAndLintArgs) {
  const queryClient = useQueryClient()
  const [lintResults, setLintResults] = useState<LintResult[]>([])
  const [showSaveConfirm, setShowSaveConfirm] = useState(false)
  const [isLintingBeforeSave, setIsLintingBeforeSave] = useState(false)

  const saveMutation = useMutation({
    mutationFn: async (content: string) => {
      const response = await apiClient.put(`/stacks/${stackId}/compose`, { content })
      return response.data
    },
    onSuccess: (data, variables) => {
      setLastSaved(variables)
      setLintResults(data.lintResults || [])
      toast.success('Compose file saved successfully')
      if (data.lintResults?.some((r: LintResult) => r.level === 'error')) {
        toast.error('Lint errors detected')
      } else if (data.lintResults?.some((r: LintResult) => r.level === 'warning')) {
        toast.warning('Lint warnings detected')
      }
      queryClient.invalidateQueries({ queryKey: ['stack', stackId] })
    },
    onError: (error: { response?: { data?: { lintResults?: LintResult[] } } }) => {
      const appError = classifyError(error)
      if (error.response?.data?.lintResults) {
        setLintResults(error.response.data.lintResults)
        toast.error('Lint errors detected')
      } else {
        toast.error(appError.message)
      }
    },
  })

  const handleSave = useCallback(
    async (forceSave = false) => {
      if (!viewRef.current) return
      const currentContent = viewRef.current.state.doc.toString()

      if (forceSave) {
        saveMutation.mutate(currentContent)
        setShowSaveConfirm(false)
        return
      }

      setIsLintingBeforeSave(true)
      try {
        const response = await apiClient.post(`/stacks/${stackId}/compose/lint`, { content: currentContent })
        const results = response.data.lintResults || []
        setLintResults(results)

        if (results.some((r: LintResult) => r.level === 'error')) {
          setShowSaveConfirm(true)
        } else {
          saveMutation.mutate(currentContent)
        }
      } catch {
        saveMutation.mutate(currentContent)
      } finally {
        setIsLintingBeforeSave(false)
      }
    },
    [saveMutation, stackId, viewRef],
  )
  useEffect(() => {
    handleSaveRef.current = handleSave
  }, [handleSave, handleSaveRef])

  const lintMutation = useMutation({
    mutationFn: async (content: string) => {
      const response = await apiClient.post(`/stacks/${stackId}/compose/lint`, { content })
      return response.data
    },
    onSuccess: (data) => {
      setLintResults(data.lintResults || [])
      if (data.lintResults?.some((r: LintResult) => r.level === 'error')) {
        toast.error('Lint errors detected')
      } else if (data.lintResults?.some((r: LintResult) => r.level === 'warning')) {
        toast.warning('Lint warnings detected')
      } else {
        toast.success('No lint issues found')
      }
      queryClient.invalidateQueries({ queryKey: ['stack', stackId, 'compose'] })
    },
    onError: () => {
      toast.error('Failed to lint compose file')
    },
  })

  const handleLint = useCallback(() => {
    if (!viewRef.current) return
    const currentContent = viewRef.current.state.doc.toString()
    lintMutation.mutate(currentContent)
  }, [lintMutation, viewRef])

  return {
    lintResults,
    showSaveConfirm,
    setShowSaveConfirm,
    isLintingBeforeSave,
    saveMutation,
    lintMutation,
    handleSave,
    handleLint,
  }
}
