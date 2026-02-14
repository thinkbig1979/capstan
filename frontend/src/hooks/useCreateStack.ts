import { useMutation, useQueryClient } from '@tanstack/react-query'
import { apiClient } from '@/lib/api'
import type { LintResult } from '@/types'

interface CreateStackInput {
  name: string
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
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['directories'] })
      queryClient.invalidateQueries({ queryKey: ['stacks'] })
    },
  })
}
