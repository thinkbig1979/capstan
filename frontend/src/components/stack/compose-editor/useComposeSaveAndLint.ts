import { useCallback, useEffect, useState } from 'react'
import type { RefObject } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { stacksApi } from '@/lib/api'
import { classifyError, presentError, toastInvalid } from '@/lib/error-handler'
import { toast } from 'sonner'
import type { ExternalToast } from 'sonner'
import type { LintResult } from '@/types'
import type { useCodeMirrorEditor } from '@/hooks/useCodeMirrorEditor'
import { queryKeys } from '@/lib/query-keys'

interface UseComposeSaveAndLintArgs {
  stackId: string
  viewRef: ReturnType<typeof useCodeMirrorEditor>['viewRef']
  handleSaveRef: RefObject<() => void>
  setLastSaved: (content: string) => void
  /**
   * Set for a git stack: the save toast then offers "Commit and push", which
   * only calls this (opens the dialog). The save itself makes no git call.
   */
  onOpenCommit?: () => void
}

/**
 * Owns the save + lint mutations and the lint-before-save decision (audit
 * finding-adjacent behavior, not touched here): a normal save first POSTs to
 * /compose/lint; if that lint run reports errors, a dialog telling the user to fix
 * them is shown instead of saving (the server refuses such a file anyway); if
 * the lint request itself fails, the save proceeds directly (no dialog, no
 * error surfaced).
 */
export function useComposeSaveAndLint({
  stackId,
  viewRef,
  handleSaveRef,
  setLastSaved,
  onOpenCommit,
}: UseComposeSaveAndLintArgs) {
  const queryClient = useQueryClient()
  const [lintResults, setLintResults] = useState<LintResult[]>([])
  const [showSaveConfirm, setShowSaveConfirm] = useState(false)
  const [isLintingBeforeSave, setIsLintingBeforeSave] = useState(false)

  const saveMutation = useMutation({
    mutationFn: (content: string) => stacksApi.updateCompose(stackId, content),
    onSuccess: (data, variables) => {
      setLastSaved(variables)
      setLintResults(data.lintResults || [])
      // One toast per save: a second lint toast stacked over this one and
      // covered its "Commit and push" action (agent-os-s35c). The warning
      // note rides in the description instead; the findings themselves are in
      // the editor's lint panel (setLintResults above). A 200 never carries
      // error-level results (the server answers 422 and saves nothing), so
      // those land in onError, not here.
      const options: ExternalToast = {}
      if (data.lintResults?.some((r: LintResult) => r.level === 'warning')) {
        options.description = 'Lint warnings detected'
      }
      if (onOpenCommit) {
        options.action = { label: 'Commit and push', onClick: onOpenCommit }
      }
      if (Object.keys(options).length > 0) {
        toast.success('Compose file saved successfully', options)
      } else {
        toast.success('Compose file saved successfully')
      }
      queryClient.invalidateQueries({ queryKey: queryKeys.stack.detail(stackId) })
    },
    onError: (error: unknown) => {
      const appError = classifyError(error)
      // api.ts's interceptor rejects with the backend's AppError body
      // flattened to the top level (`code`/`message`/`details`/`status`),
      // not an axios `{response:{data}}` wrapper — and lintResults live
      // under `details`, matching the COMPOSE_VALIDATION_ERROR shape sent by
      // compose.go:164-175 (agent-os-m2x).
      const details = (error as { details?: { lintResults?: LintResult[] } } | null | undefined)?.details
      if (details?.lintResults && details.lintResults.length > 0) {
        setLintResults(details.lintResults)
        toastInvalid('Lint errors detected')
      } else {
        toast.error(appError.message)
      }
    },
  })

  const handleSave = useCallback(
    async () => {
      if (!viewRef.current) return
      const currentContent = viewRef.current.state.doc.toString()

      setIsLintingBeforeSave(true)
      try {
        const data = await stacksApi.lintCompose(stackId, currentContent)
        const results = data.lintResults || []
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
    mutationFn: (content: string) => stacksApi.lintCompose(stackId, content),
    onSuccess: (data) => {
      setLintResults(data.lintResults || [])
      if (data.lintResults?.some((r: LintResult) => r.level === 'error')) {
        toastInvalid('Lint errors detected')
      } else if (data.lintResults?.some((r: LintResult) => r.level === 'warning')) {
        toast.warning('Lint warnings detected')
      } else {
        toast.success('No lint issues found')
      }
      queryClient.invalidateQueries({ queryKey: queryKeys.stack.compose(stackId) })
    },
    onError: (err) => {
      presentError(err, { fallback: 'Failed to lint compose file' })
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
