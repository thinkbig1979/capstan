import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { gitApi } from '@/lib/api'

export function useGitStatus(stackId: string) {
  return useQuery({
    queryKey: ['git', stackId],
    queryFn: () => gitApi.status(stackId),
    staleTime: 60000,
    // A non-git stack returns a definitive 404 — retrying just repeats the error
    // in the console/network tab. The panel hides on first failure either way.
    retry: false,
  })
}

export function useGitLog(stackId: string, limit = 50, offset = 0, file?: string) {
  return useQuery({
    queryKey: ['git', stackId, 'log', limit, offset, file],
    queryFn: () => gitApi.log(stackId, limit, offset, file),
    staleTime: 60000,
  })
}

export function useGitDiff(stackId: string, hash: string) {
  return useQuery({
    queryKey: ['git', stackId, 'diff', hash],
    queryFn: () => gitApi.diff(stackId, hash),
    enabled: !!hash,
    staleTime: 60000,
  })
}

export function useGitPull() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: ({ stackId, redeploy = false }: { stackId: string; redeploy?: boolean }) =>
      gitApi.pull(stackId, redeploy),
    onSuccess: (_, { stackId }) => {
      queryClient.invalidateQueries({ queryKey: ['git', stackId] })
      queryClient.invalidateQueries({ queryKey: ['stacks'] })
    },
  })
}
