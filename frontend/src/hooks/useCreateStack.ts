import { useMutation, useQueryClient } from '@tanstack/react-query'
import { stacksApi } from '@/lib/api'
import { toast } from 'sonner'
import { isActionResult } from '@/lib/action-result'
import type { LintResult, Stack } from '@/types'

interface CreateStackInput {
  name: string
  directory?: string
  composeContent: string
  envContent?: string
  deploy: boolean
}

/**
 * Extracts `details.stack` from a CreateStackResult.
 *
 * Post-migration, the backend wraps everything inside `details`:
 *   { outcome, reason, details: { stack, lintResults, deployed, ... } }
 *
 * Returns undefined if no stack is present (genuine failure paths).
 */
function extractStack(data: unknown): Stack | undefined {
  if (typeof data !== 'object' || data === null) return undefined
  const d = data as Record<string, unknown>

  // The backend always wraps the stack inside details (ActionResult contract).
  if (isActionResult(d)) {
    const details = d.details as { stack?: Stack } | undefined
    return details?.stack
  }
  return undefined
}

/**
 * Extracts `details.lintResults` from a CreateStackResult.
 */
function extractLintResults(data: unknown): LintResult[] | undefined {
  if (typeof data !== 'object' || data === null) return undefined
  const d = data as Record<string, unknown>
  if (isActionResult(d)) {
    const details = d.details as { lintResults?: LintResult[] } | undefined
    return details?.lintResults
  }
  return undefined
}

/**
 * Determines if a create response (either from onSuccess or onError) represents
 * a stack that was CREATED but failed to deploy. This is the `partial` outcome
 * case — the backend returns HTTP 207 with both `outcome:'partial'` and a stack
 * in details.
 *
 * A created-but-not-deployed stack must NEVER appear as "Create failed" — it
 * exists on disk and in the database; it just hasn't started.
 */
function isCreatedButNotDeployed(
  data: unknown,
): data is { outcome: 'partial'; reason: string; details: { stack: Stack; lintResults?: LintResult[] } } {
  if (typeof data !== 'object' || data === null) return false
  const d = data as Record<string, unknown>
  if (d.outcome !== 'partial') return false
  const details = d.details as { stack?: unknown } | undefined
  return typeof details === 'object' && details !== null && details.stack != null
}

export function useCreateStack() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: async (input: CreateStackInput) => {
      return stacksApi.create(input)
    },
    onSuccess: (data) => {
      // Invalidate regardless of outcome whenever we have a result from the create endpoint.
      queryClient.invalidateQueries({ queryKey: ['directories'] })
      queryClient.invalidateQueries({ queryKey: ['stacks'] })

      if (isActionResult(data)) {
        if (data.outcome === 'partial') {
          // Stack was created but deploy failed. Show a warning, not an error.
          // The stack exists in the DB — the UI must show it.
          toast.warning(data.reason || 'Stack created but not deployed')
        } else if (data.outcome === 'success') {
          // Preserve lint-result toast differentiation.
          const lintResults = extractLintResults(data)
          if (lintResults?.some((r) => r.level === 'error')) {
            toast.error('Stack created but has lint errors')
          } else if (lintResults?.some((r) => r.level === 'warning')) {
            toast.warning('Stack created but has lint warnings')
          } else {
            toast.success('Stack created successfully')
          }
        } else {
          // no_change/failed are unexpected from create (a real failure rejects
          // and lands in onError), but never leave a 2xx body silent.
          toast.error(data.reason || 'Stack create failed')
        }
      }
    },
    onError: (error: unknown) => {
      // CRITICAL: a created-but-not-deployed response may arrive as onError if
      // the axios interceptor rejects 207 (Multi-Status). Detect it by
      // `outcome:'partial'` + `details.stack` and treat it as a warning, NOT
      // a failure. Always invalidate so the stack appears in the list.
      if (isCreatedButNotDeployed(error)) {
        queryClient.invalidateQueries({ queryKey: ['directories'] })
        queryClient.invalidateQueries({ queryKey: ['stacks'] })
        const lintResults = error.details.lintResults
        if (lintResults?.some((r) => r.level === 'error')) {
          toast.warning(`Stack created but not deployed — lint errors present: ${error.reason}`)
        } else {
          toast.warning(error.reason || 'Stack created but not deployed')
        }
        return
      }

      // Genuine create failure (validation, mkdir, db — HTTP 4xx/5xx without a stack).
      const err = error as { error?: string; code?: string; lintResults?: LintResult[] }

      if (err.lintResults && err.lintResults.length > 0) {
        toast.error('Lint errors detected')
      } else if (err.error?.includes('already exists')) {
        toast.error('A stack with this name already exists')
      } else {
        toast.error(err.error || 'Failed to create stack')
      }
    },
  })
}

export { extractStack, extractLintResults }
