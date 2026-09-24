import { useCallback, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { stacksApi } from '@/lib/api'
import { toast } from 'sonner'
import { presentError, toastInvalid } from '@/lib/error-handler'
import type { useCodeMirrorEditor } from '@/hooks/useCodeMirrorEditor'
import { inferVarName } from './inferVarName'
import { queryKeys } from '@/lib/query-keys'

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
   * Atomic extract-to-env (audit finding #11): PUT /stacks/:id/compose-env
   * writes compose + env in one transaction, so there is no partial-write
   * window where compose references a missing var.
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

      // Build the updated .env content.
      //
      // A REJECTION here is deliberately NOT caught (agent-os-erfc). It used to
      // be, by an unbound `catch {}` that distinguished nothing, so a 500 on an
      // existing .env left currentEnv '' and the next statement built the new
      // file content out of that — replacing a file we could not read with a
      // single line, while reporting the extraction as successful. There is
      // nothing safe to write when the current contents are unknown, so the
      // rejection travels to the outer catch: that aborts before the write and
      // presents the cause.
      //
      // The 200 answers still proceed, and only they do. `hasEnvFile: false` is
      // the sole no-file answer (env.go:105, agent-os-bt5y) and the write below
      // creates the file. Both rejections this newly aborts on are genuine
      // faults by the backend's own reading: a 404 "Env file not found on disk"
      // is the DB and the filesystem disagreeing, which env.go:98-100 states
      // must NOT be fused with the no-file state, and a 400 rejects an env path
      // that failed validation (env.go:111-118).
      const envData = await stacksApi.getEnv(stackId)
      const currentEnv = envData.hasEnvFile && envData.raw ? envData.raw : ''

      const newEnvLine = `${varName}=${selectedText}`
      const updatedEnv = currentEnv ? `${currentEnv.trimEnd()}\n${newEnvLine}` : newEnvLine

      // Atomic write — body: { composeContent, envRaw } per ComposeEnvRequest
      try {
        const result = await stacksApi.updateComposeAndEnv(stackId, updatedCompose, updatedEnv)
        if (result.outcome !== 'success' && result.outcome !== 'no_change') {
          // Behaviour unchanged (agent-os-5g8a): this site already rendered
          // the reason and keeps it as the TITLE, the same call
          // toastForResult's `failed` arm makes for an ActionResult.
          toastInvalid(result.reason || 'Failed to extract variable to .env')
          return
        }
      } catch (e: unknown) {
        // agent-os-yre8. Not in the bead's own SPEC, which quotes only the
        // outer catch, but this is the site a rejected atomic write lands
        // on: PutComposeAndEnv answers truth.Failed/truth.Partial at 5xx,
        // so axios rejects and the ActionResult reason -- which names WHICH
        // write failed and whether a rollback happened -- arrives HERE.
        presentError(e, { fallback: 'Failed to extract variable to .env' })
        return
      }

      // Update editor state
      view.dispatch({
        changes: { from: sel.from, to: sel.to, insert: `\${${varName}}` },
      })

      setContent(updatedCompose)
      setLastSaved(updatedCompose)
      queryClient.invalidateQueries({ queryKey: queryKeys.stack.detail(stackId) })
      toast.success(`Extracted ${varName} to .env`)
      setShowExtractDialog(false)
      setSelectedText('')
    } catch (err) {
      // agent-os-yre8. This catch used to take no binding, so the cause was
      // not merely discarded -- it was never observed.
      //
      // The .env READ above (agent-os-erfc) lands here. Reaching this catch
      // from there IS the abort -- no write call has been made at that point --
      // which is why a file we could not read is no longer overwritten with
      // one line.
      presentError(err, { fallback: 'Failed to extract variable to .env' })
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
