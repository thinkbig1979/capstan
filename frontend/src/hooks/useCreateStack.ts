import { useMutation, useQueryClient } from '@tanstack/react-query'
import { apiClient } from '@/lib/api'
import { toast } from 'sonner'
import type { LintResult } from '@/types'

interface CreateStackInput {
  name: string
  directory?: string
  composeContent: string
  envContent?: string
  deploy: boolean
}

interface CreateStackResponse {
  stack: {
    id: string
    directory: string
    projectName: string
  }
  deployed?: boolean
  lintResults?: LintResult[]
}

export function useCreateStack() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: async (input: CreateStackInput) => {
      const response = await apiClient.post('/stacks', input)
      return response.data as CreateStackResponse
    },
    onSuccess: (data) => {
      queryClient.invalidateQueries({ queryKey: ['directories'] })
      queryClient.invalidateQueries({ queryKey: ['stacks'] })
      
      if (data.lintResults?.some((r) => r.level === 'error')) {
        toast.error('Stack created but has lint errors')
      } else if (data.lintResults?.some((r) => r.level === 'warning')) {
        toast.warning('Stack created but has lint warnings')
      } else {
        toast.success('Stack created successfully')
      }
    },
    onError: (error: unknown) => {
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
