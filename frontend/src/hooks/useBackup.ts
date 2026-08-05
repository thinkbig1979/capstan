import { useCallback, useEffect, useRef, useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { backupApi } from '@/lib/api'
import { WSClient } from '@/lib/ws'
import { reconcileOnClose } from '@/lib/ws-reconcile'
import { queryKeys } from '@/lib/query-keys'
import type { BackupPolicy, BackupOperationResult } from '@/types'

// ─── Settings ────────────────────────────────────────────────────────────────

export function useBackupSettings() {
  return useQuery({
    queryKey: queryKeys.backup.settings(),
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
      queryClient.invalidateQueries({ queryKey: queryKeys.backup.settings() })
      queryClient.invalidateQueries({ queryKey: queryKeys.backup.status() })
    },
  })
}

// ─── Policies ────────────────────────────────────────────────────────────────

export function useBackupPolicies() {
  return useQuery({
    queryKey: queryKeys.backup.policies(),
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
      await queryClient.cancelQueries({ queryKey: queryKeys.backup.policies() })

      // Snapshot previous value for rollback.
      const previous = queryClient.getQueryData<{ policies: BackupPolicy[] }>(
        queryKeys.backup.policies(),
      )

      // Optimistically update the cache.
      queryClient.setQueryData<{ policies: BackupPolicy[] }>(
        queryKeys.backup.policies(),
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
        queryClient.setQueryData(queryKeys.backup.policies(), context.previous)
      }
    },

    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.backup.policies() })
      queryClient.invalidateQueries({ queryKey: queryKeys.backup.status() })
    },
  })
}

// ─── Status & history ────────────────────────────────────────────────────────

export function useBackupStatus() {
  return useQuery({
    queryKey: queryKeys.backup.status(),
    queryFn: () => backupApi.getStatus(),
    retry: 1,
  })
}

// ─── Snapshots ───────────────────────────────────────────────────────────────

export function useBackupSnapshots(stackId: string) {
  return useQuery({
    queryKey: queryKeys.backup.snapshots(stackId),
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
      queryClient.invalidateQueries({ queryKey: queryKeys.backup.status() })
      queryClient.invalidateQueries({ queryKey: queryKeys.backup.history() })
    },
  })
}

export function useRestore() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (data: { stackId: string; snapshotId: string; target?: string }) =>
      // Restore is destructive; the server requires an explicit confirm flag
      // (the user has already confirmed via the ConfirmDialog before we get here).
      backupApi.restore({ ...data, confirm: true }),
    onSuccess: (_data: BackupOperationResult, variables) => {
      queryClient.invalidateQueries({ queryKey: queryKeys.backup.status() })
      queryClient.invalidateQueries({ queryKey: queryKeys.backup.history() })
      queryClient.invalidateQueries({
        queryKey: queryKeys.backup.snapshots(variables.stackId),
      })
    },
  })
}

export function useInitRepo() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: () => backupApi.initRepo(),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.backup.settings() })
      queryClient.invalidateQueries({ queryKey: queryKeys.backup.status() })
    },
  })
}

export function useTestCloud() {
  return useMutation({
    mutationFn: () => backupApi.testCloud(),
  })
}

// ─── Snapshot preview ────────────────────────────────────────────────────────

export function usePreviewSnapshot(snapshotId: string) {
  return useQuery({
    queryKey: queryKeys.backup.snapshotPreview(snapshotId),
    queryFn: () => backupApi.previewSnapshot(snapshotId),
    enabled: !!snapshotId,
    retry: 1,
  })
}

// ─── Per-stack run history ───────────────────────────────────────────────────

/**
 * Returns recent BackupRun records for display in the per-stack Backups tab.
 * The global history endpoint is fetched (limited) and returned as-is;
 * all runs are relevant because they represent scheduler or manual backup passes
 * that include every enabled stack. A stackId param is accepted for future
 * server-side filtering.
 */
export function useStackBackupRuns(stackId: string, limit = 20) {
  const result = useQuery({
    queryKey: queryKeys.backup.stackHistory(limit, stackId),
    queryFn: () => backupApi.getHistory(limit),
    enabled: !!stackId,
    retry: 1,
  })

  return {
    runs: result.data?.runs ?? [],
    isLoading: result.isLoading,
    isError: result.isError,
  }
}

// ─── Backup streaming ────────────────────────────────────────────────────────

/**
 * BackupStreamStatus mirrors the Action Truth Contract outcomes.
 * - idle      : no operation in progress
 * - running   : operation in progress
 * - success   : completed successfully
 * - partial   : completed with partial success (render as warning)
 * - error     : failed (maps from outcome:'failed', legacy success:false,
 *               or a genuine WS connection error — NOT a bare disconnect)
 */
type BackupStreamStatus = 'idle' | 'running' | 'success' | 'partial' | 'error'

export interface BackupStreamState {
  status: BackupStreamStatus
  lines: string[]
  error: string | null
  // onDone receives the terminal status so callers can derive an honest toast
  // (success/partial/error) instead of unconditionally reporting success.
  connect: (wsPath: string, onDone?: (status: BackupStreamStatus) => void) => void
  reset: () => void
}

/**
 * Map a done-frame's typed outcome (Action Truth Contract) or legacy success
 * flag to a BackupStreamStatus. Backend 'failed' → 'error' so callers only
 * deal with frontend-level status names.
 */
function doneFrameToStatus(msg: {
  outcome?: 'success' | 'no_change' | 'partial' | 'failed' | 'interrupted'
  success?: boolean
}): BackupStreamStatus {
  if (msg.outcome) {
    switch (msg.outcome) {
      case 'success':     return 'success'
      case 'no_change':   return 'success'  // treat no_change as success for backup ops
      case 'partial':     return 'partial'
      case 'failed':      return 'error'
      // 'interrupted' (agent-os-pid): a run swept to this status by a
      // previous-process crash or restore, reported via Attach's DB fallback
      // when a client reconnects after a restart. The live-stream widget has
      // no dedicated "interrupted" bucket (only success/partial/error/idle/
      // running) and doesn't need one -- this is edge-case reconnect
      // territory, not the primary status surface (that's the dashboard
      // badge, which does get its own state). 'error' is the closest bucket.
      case 'interrupted': return 'error'
    }
  }
  // Legacy fallback: key off success boolean.
  return msg.success ? 'success' : 'error'
}

/**
 * Streams live output for a backup/restore operation over WebSocket.
 *
 * Finding #17 fix: on a WS close WITHOUT a terminal 'done' frame, the hook
 * does NOT assert failure. It calls reconcileOnClose() which refetches the
 * backup history query so the UI reconciles to server truth. The backend op
 * runs on a detached context and persists a durable run record — the server
 * state is the source of truth, not the connection.
 *
 * wsPath is the path component from BackupOperationResult.wsUrl
 * (e.g. '/ws/backups/restore/abc123').
 */
export function useBackupStreaming(): BackupStreamState {
  const queryClient = useQueryClient()
  const [status, setStatus] = useState<BackupStreamStatus>('idle')
  const [lines, setLines] = useState<string[]>([])
  const [error, setError] = useState<string | null>(null)
  const clientRef = useRef<WSClient | null>(null)
  const completedRef = useRef(false)

  useEffect(() => {
    return () => {
      clientRef.current?.close()
      clientRef.current = null
    }
  }, [])

  const reset = useCallback(() => {
    clientRef.current?.close()
    clientRef.current = null
    setStatus('idle')
    setLines([])
    setError(null)
    completedRef.current = false
  }, [])

  const connect = useCallback((wsPath: string, onDone?: (status: BackupStreamStatus) => void) => {
    clientRef.current?.close()
    setLines([])
    setError(null)
    setStatus('running')
    completedRef.current = false

    const client = new WSClient()
    clientRef.current = client

    // Refetch helper: invalidate backup history + status so the UI reflects
    // the persisted server run record rather than the transient WS state.
    const refetchHistory = () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.backup.history() })
      queryClient.invalidateQueries({ queryKey: queryKeys.backup.status() })
    }

    client.connect(
      wsPath,
      (data) => {
        if (typeof data !== 'string') return
        try {
          const msg = JSON.parse(data) as {
            type: string
            line?: string
            message?: string
            // Action Truth Contract fields (B5 backend, migrated backends)
            outcome?: 'success' | 'no_change' | 'partial' | 'failed'
            reason?: string
            // Legacy fields (pre-migration backends)
            success?: boolean
            error?: string
          }

          if (msg.type === 'data' && msg.line) {
            setLines((prev) => [...prev, msg.line!])
          } else if (msg.type === 'phase' && msg.message) {
            setLines((prev) => [...prev, `--- ${msg.message} ---`])
          } else if (msg.type === 'done') {
            const finalStatus = doneFrameToStatus(msg)

            // Set completedRef BEFORE calling client.close() so the onClose
            // callback (which fires synchronously on close() in some runtimes)
            // sees completed=true and does NOT overwrite the real outcome.
            // Mirrors the B2 race fix in useStreamingOperation.ts.
            completedRef.current = true

            if (finalStatus === 'success') {
              setStatus('success')
              const label = msg.reason || 'Backup completed successfully.'
              setLines((prev) => [...prev, label])
            } else if (finalStatus === 'partial') {
              setStatus('partial')
              const label = msg.reason || 'Backup partially completed.'
              setLines((prev) => [...prev, label])
            } else {
              // error / failed
              const errMsg = msg.error || msg.reason || 'Backup failed'
              setStatus('error')
              setError(errMsg)
              setLines((prev) => [...prev, `Error: ${errMsg}`])
            }

            // Always invalidate history/status so the run list reflects the
            // persisted record, regardless of the outcome.
            refetchHistory()

            client.close()
            clientRef.current = null
            onDone?.(finalStatus)
          } else if (msg.type === 'error') {
            const errMsg = msg.error || 'Unknown error'
            setStatus('error')
            setError(errMsg)
            setLines((prev) => [...prev, `Error: ${errMsg}`])
          }
        } catch {
          // ignore non-JSON frames
        }
      },
      {
        onClose: () => {
          // Finding #17: if a terminal done frame was received, completedRef is
          // true and we must NOT overwrite the real outcome. If the socket closed
          // without a done frame we cannot safely assert failure — the backend op
          // runs on a detached context and may have succeeded. Reconcile by
          // refetching the source-of-truth history query instead of lying.
          reconcileOnClose({
            completed: completedRef.current,
            refetch: refetchHistory,
          })
          if (!completedRef.current) {
            // Append a note so the log isn't empty, but do NOT set status='error'.
            setLines((prev) => [...prev, 'Connection closed — refreshing run history…'])
          }
          clientRef.current = null
        },
        onError: () => {
          // A genuine connection error (not just a close) warrants error status.
          if (!completedRef.current) {
            setStatus('error')
            setError('WebSocket connection failed')
          }
          clientRef.current = null
        },
        onReconnectFailed: () => {
          if (!completedRef.current) {
            setStatus('error')
            setError('Connection lost, reconnect failed')
          }
          clientRef.current = null
        },
      },
    )
  }, [queryClient])

  return { status, lines, error, connect, reset }
}
