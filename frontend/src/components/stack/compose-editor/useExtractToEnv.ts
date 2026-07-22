import { useCallback, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { apiClient, stacksApi } from '@/lib/api'
import { toast } from 'sonner'
import { isActionResult } from '@/lib/action-result'
import type { useCodeMirrorEditor } from '@/hooks/useCodeMirrorEditor'
import { inferVarName } from './inferVarName'

interface UseExtractToEnvArgs {
  stackId: string
  viewRef: ReturnType<typeof useCodeMirrorEditor>['viewRef']
  selectedText: string
  setSelectedText: (text: string) => void
  setContent: (content: string) => void
  setLastSaved: (content: string) => void
}

/**
 * Owns the "Extract to .env" flow: inferring a variable name from the
 * surrounding YAML for the current selection, and writing the compose +
 * .env changes. The selection itself (selectedText) is threaded in from the
 * root, since it's captured by the useCodeMirrorEditor onSelect callback at
 * mount time, alongside the editor mount itself.
 */
export function useExtractToEnv({
  stackId,
  viewRef,
  selectedText,
  setSelectedText,
  setContent,
  setLastSaved,
}: UseExtractToEnvArgs) {
  const queryClient = useQueryClient()

  const [extractVarName, setExtractVarName] = useState('')
  const [showExtractDialog, setShowExtractDialog] = useState(false)
  const [isExtracting, setIsExtracting] = useState(false)

  const handleExtractToEnv = useCallback(() => {
    if (!viewRef.current || !selectedText) return
    const view = viewRef.current
    const sel = view.state.selection.main
    const inferred = inferVarName(view.state.doc.toString(), sel.from)
    setExtractVarName(inferred)
    setShowExtractDialog(true)
  }, [selectedText, viewRef])

  /**
   * Atomic extract-to-env (audit finding #11).
   *
   * Preferred path: PUT /stacks/:id/compose-env writes compose + env in one
   * transaction — no partial-write window where compose references a missing var.
   *
   * Fallback path: if the atomic endpoint returns 404 (backend not yet migrated),
   * we fall back to the env-first sequential write. Writing env first means the
   * compose reference is only persisted after the var exists in .env.
   */
  const confirmExtract = useCallback(async () => {
    if (!viewRef.current || !selectedText || !extractVarName.trim()) return

    const view = viewRef.current
    const sel = view.state.selection.main
    const varName = extractVarName.trim().toUpperCase().replace(/[^A-Z0-9_]/g, '_')

    setIsExtracting(true)
    try {
      const currentCompose = view.state.doc.toString()
      const before = currentCompose.slice(0, sel.from)
      const after = currentCompose.slice(sel.to)
      const updatedCompose = before + `\${${varName}}` + after

      // Build the updated .env content
      let currentEnv = ''
      try {
        const envData = await stacksApi.getEnv(stackId)
        if (envData?.raw) {
          currentEnv = envData.raw
        }
      } catch {
        // No .env file yet — the atomic endpoint will create it.
      }

      const newEnvLine = `${varName}=${selectedText}`
      const updatedEnv = currentEnv ? `${currentEnv.trimEnd()}\n${newEnvLine}` : newEnvLine

      // Attempt atomic write — body: { composeContent, envRaw } per ComposeEnvRequest
      let atomicSuccess = false
      try {
        const result = await stacksApi.updateComposeAndEnv(stackId, updatedCompose, updatedEnv)
        if (isActionResult(result)) {
          if (result.outcome === 'success' || result.outcome === 'no_change') {
            atomicSuccess = true
          } else {
            toast.error(result.reason || 'Failed to extract variable to .env')
            return
          }
        } else {
          // The endpoint doesn't exist yet (pre-B4 backend) — fall through to sequential.
          atomicSuccess = false
        }
      } catch (e: unknown) {
        const err = e as { status?: number; response?: { status?: number } }
        const status = err.status ?? err.response?.status
        if (status === 404) {
          // Backend not yet migrated; use env-first sequential fallback.
          atomicSuccess = false
        } else {
          toast.error('Failed to extract variable to .env')
          return
        }
      }

      if (!atomicSuccess) {
        // Sequential fallback — write env FIRST so the compose reference is
        // never persisted without the variable being available.
        await apiClient.put(`/stacks/${stackId}/env`, { raw: updatedEnv })
        await apiClient.put(`/stacks/${stackId}/compose`, { content: updatedCompose })
      }

      // Update editor state
      view.dispatch({
        changes: { from: sel.from, to: sel.to, insert: `\${${varName}}` },
      })

      setContent(updatedCompose)
      setLastSaved(updatedCompose)
      queryClient.invalidateQueries({ queryKey: ['stack', stackId] })
      toast.success(`Extracted ${varName} to .env`)
      setShowExtractDialog(false)
      setSelectedText('')
    } catch {
      toast.error('Failed to extract variable to .env')
    } finally {
      setIsExtracting(false)
    }
  }, [selectedText, extractVarName, stackId, queryClient, viewRef, setContent, setLastSaved, setSelectedText])

  return {
    extractVarName,
    setExtractVarName,
    showExtractDialog,
    setShowExtractDialog,
    isExtracting,
    handleExtractToEnv,
    confirmExtract,
  }
}
