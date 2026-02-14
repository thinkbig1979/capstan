import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { apiClient } from '@/lib/api'
import type { GitStatus, GitCommit, CommandResult } from '@/types'

export function useGitStatus(path: string) {
  return useQuery({
    queryKey: ['git', path],
    queryFn: async () => {
      const response = await apiClient.get(`/directories/${encodeURIComponent(path)}/git`)
      return response.data as GitStatus
    },
    staleTime: 60000,
  })
}

export function useGitLog(path: string, limit = 50, offset = 0, file?: string) {
  return useQuery({
    queryKey: ['git', path, 'log', limit, offset, file],
    queryFn: async () => {
      const params = new URLSearchParams({ limit: limit.toString(), offset: offset.toString() })
      if (file) params.append('file', file)
      const response = await apiClient.get(`/directories/${encodeURIComponent(path)}/git/log?${params}`)
      return response.data as { commits: GitCommit[]; hasMore: boolean }
    },
    staleTime: 60000,
  })
}

export function useGitDiff(path: string, hash: string) {
  return useQuery({
    queryKey: ['git', path, 'diff', hash],
    queryFn: async () => {
      const response = await apiClient.get(`/directories/${encodeURIComponent(path)}/git/diff/${hash}`)
      return response.data as { diff: string }
    },
    enabled: !!hash,
    staleTime: 60000,
  })
}

export function useGitPull() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: async ({ path, redeploy = false }: { path: string; redeploy?: boolean }) => {
      const params = redeploy ? '?redeploy=true' : ''
      const response = await apiClient.post(`/directories/${encodeURIComponent(path)}/git/pull${params}`)
      return response.data as CommandResult & { redeployedStacks?: string[] }
    },
    onSuccess: (_, { path }) => {
      queryClient.invalidateQueries({ queryKey: ['git', path] })
      queryClient.invalidateQueries({ queryKey: ['stacks'] })
    },
  })
}
