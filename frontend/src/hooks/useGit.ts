import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { apiClient } from '@/lib/api'
import type { GitStatus, GitCommit, CommandResult } from '@/types'

export function useGitStatus(stackId: string) {
  return useQuery({
    queryKey: ['git', stackId],
    queryFn: async () => {
      const response = await apiClient.get(`/git?stackId=${encodeURIComponent(stackId)}`)
      return response.data as GitStatus
    },
    staleTime: 60000,
  })
}

export function useGitLog(stackId: string, limit = 50, offset = 0, file?: string) {
  return useQuery({
    queryKey: ['git', stackId, 'log', limit, offset, file],
    queryFn: async () => {
      const params = new URLSearchParams({ stackId, limit: limit.toString(), offset: offset.toString() })
      if (file) params.append('file', file)
      const response = await apiClient.get(`/git/log?${params}`)
      return response.data as { commits: GitCommit[]; hasMore: boolean }
    },
    staleTime: 60000,
  })
}

export function useGitDiff(stackId: string, hash: string) {
  return useQuery({
    queryKey: ['git', stackId, 'diff', hash],
    queryFn: async () => {
      const response = await apiClient.get(`/git/diff/${hash}?stackId=${encodeURIComponent(stackId)}`)
      return response.data as { diff: string }
    },
    enabled: !!hash,
    staleTime: 60000,
  })
}

export function useGitPull() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: async ({ stackId, redeploy = false }: { stackId: string; redeploy?: boolean }) => {
      const params = new URLSearchParams({ stackId })
      if (redeploy) params.append('redeploy', 'true')
      const response = await apiClient.post(`/git/pull?${params}`)
      return response.data as CommandResult & { redeployedStacks?: string[] }
    },
    onSuccess: (_, { stackId }) => {
      queryClient.invalidateQueries({ queryKey: ['git', stackId] })
      queryClient.invalidateQueries({ queryKey: ['stacks'] })
    },
  })
}
