import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { backupApi } from '@/lib/api'
import type {
  BackupPolicy,
  BackupOperationResult,
} from '@/types'

// ─── Query keys ─────────────────────────────────────────────────────────────

export const backupKeys = {
  all: ['backup'] as const,
  settings: () => [...backupKeys.all, 'settings'] as const,
  policies: () => [...backupKeys.all, 'policies'] as const,
  status: () => [...backupKeys.all, 'status'] as const,
  history: (limit?: number) => [...backupKeys.all, 'history', { limit }] as const,
  snapshots: (stackId: string) => [...backupKeys.all, 'snapshots', stackId] as const,
  run: (runId: string) => [...backupKeys.all, 'runs', runId] as const,
}

// ─── Settings ────────────────────────────────────────────────────────────────

export function useBackupSettings() {
  return useQuery({
    queryKey: backupKeys.settings(),
    queryFn: () => backupApi.getSettings(),
    retry: 1,
  })
}

export function useUpdateBackupSettings() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (data: Parameters<typeof backupApi.updateSettings>[0]) =>
      backupApi.updateSettings(data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: backupKeys.settings() })
      queryClient.invalidateQueries({ queryKey: backupKeys.status() })
    },
  })
}

// ─── Policies ────────────────────────────────────────────────────────────────

export function useBackupPolicies() {
  return useQuery({
    queryKey: backupKeys.policies(),
    queryFn: () => backupApi.getPolicies(),
    retry: 1,
  })
}

/**
 * Optimistic toggle mirroring useToggleAutoUpdate.
 * On success, invalidates policies + status caches.
 * On error, rolls back to the previous snapshot.
 */
export function useToggleBackup() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: ({
      stackId,
      enabled,
      stopPolicy,
    }: {
      stackId: string
      enabled: boolean
      stopPolicy?: 'stop' | 'hot'
    }) => backupApi.setPolicy(stackId, { enabled, stopPolicy }),

    onMutate: async ({ stackId, enabled, stopPolicy }) => {
      // Cancel any in-flight refetches so they don't overwrite our optimistic update.
      await queryClient.cancelQueries({ queryKey: backupKeys.policies() })

      // Snapshot previous value for rollback.
      const previous = queryClient.getQueryData<{ policies: BackupPolicy[] }>(
        backupKeys.policies(),
      )

      // Optimistically update the cache.
      queryClient.setQueryData<{ policies: BackupPolicy[] }>(
        backupKeys.policies(),
        (old) => {
          if (!old) return old
          const existing = old.policies.find((p) => p.targetId === stackId)
          if (existing) {
            return {
              policies: old.policies.map((p) =>
                p.targetId === stackId
                  ? {
                      ...p,
                      enabled,
                      ...(stopPolicy !== undefined ? { stopPolicy } : {}),
                    }
                  : p,
              ),
            }
          }
          // Policy doesn't exist yet — add a synthetic placeholder.
          const optimistic: BackupPolicy = {
            id: `optimistic-${stackId}`,
            targetType: 'stack',
            targetId: stackId,
            enabled,
            stopPolicy: stopPolicy ?? 'hot',
            createdAt: new Date().toISOString(),
            updatedAt: new Date().toISOString(),
          }
          return { policies: [...old.policies, optimistic] }
        },
      )

      return { previous }
    },

    onError: (_err, _vars, context) => {
      // Roll back to the snapshot we captured in onMutate.
      if (context?.previous !== undefined) {
        queryClient.setQueryData(backupKeys.policies(), context.previous)
      }
    },

    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: backupKeys.policies() })
      queryClient.invalidateQueries({ queryKey: backupKeys.status() })
    },
  })
}

// ─── Status & history ────────────────────────────────────────────────────────

export function useBackupStatus() {
  return useQuery({
    queryKey: backupKeys.status(),
    queryFn: () => backupApi.getStatus(),
    retry: 1,
  })
}

export function useBackupHistory(limit = 50) {
  return useQuery({
    queryKey: backupKeys.history(limit),
    queryFn: () => backupApi.getHistory(limit),
    retry: 1,
  })
}

export function useBackupRun(runId: string) {
  return useQuery({
    queryKey: backupKeys.run(runId),
    queryFn: () => backupApi.getRun(runId),
    enabled: !!runId,
    retry: 1,
  })
}

// ─── Snapshots ───────────────────────────────────────────────────────────────

export function useBackupSnapshots(stackId: string) {
  return useQuery({
    queryKey: backupKeys.snapshots(stackId),
    queryFn: () => backupApi.listSnapshots(stackId),
    enabled: !!stackId,
    retry: 1,
  })
}

// ─── Operations (mutations) ──────────────────────────────────────────────────

export function useRunBackup() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (data?: { stackIds?: string[] | null; dryRun?: boolean }) =>
      backupApi.runBackup(data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: backupKeys.status() })
      queryClient.invalidateQueries({ queryKey: backupKeys.history() })
    },
  })
}

export function useRunSync() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: () => backupApi.runSync(),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: backupKeys.status() })
      queryClient.invalidateQueries({ queryKey: backupKeys.history() })
    },
  })
}

export function useRestore() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (data: { stackId: string; snapshotId: string; target?: string }) =>
      backupApi.restore(data),
    onSuccess: (_data: BackupOperationResult, variables) => {
      queryClient.invalidateQueries({ queryKey: backupKeys.status() })
      queryClient.invalidateQueries({ queryKey: backupKeys.history() })
      queryClient.invalidateQueries({
        queryKey: backupKeys.snapshots(variables.stackId),
      })
    },
  })
}

export function useDrRestore() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (data: { confirm: boolean }) => backupApi.drRestore(data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: backupKeys.status() })
      queryClient.invalidateQueries({ queryKey: backupKeys.history() })
    },
  })
}

export function useInitRepo() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: () => backupApi.initRepo(),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: backupKeys.settings() })
      queryClient.invalidateQueries({ queryKey: backupKeys.status() })
    },
  })
}

export function useTestCloud() {
  return useMutation({
    mutationFn: () => backupApi.testCloud(),
  })
}

export function usePrune() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (data?: { dryRun?: boolean }) => backupApi.prune(data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: backupKeys.status() })
      queryClient.invalidateQueries({ queryKey: backupKeys.history() })
      queryClient.invalidateQueries({ queryKey: backupKeys.snapshots('') })
    },
  })
}

// ─── Policy deletion ─────────────────────────────────────────────────────────

export function useDeleteBackupPolicy() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (stackId: string) => backupApi.deletePolicy(stackId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: backupKeys.policies() })
      queryClient.invalidateQueries({ queryKey: backupKeys.status() })
    },
  })
}

// ─── Snapshot preview ────────────────────────────────────────────────────────

export function usePreviewSnapshot(snapshotId: string) {
  return useQuery({
    queryKey: [...backupKeys.all, 'snapshot-preview', snapshotId] as const,
    queryFn: () => backupApi.previewSnapshot(snapshotId),
    enabled: !!snapshotId,
    retry: 1,
  })
}
