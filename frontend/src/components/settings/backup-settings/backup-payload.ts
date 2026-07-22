import type { useUpdateBackupSettings } from '@/hooks/useBackup'
import type { BackupSettings } from '@/types'
import type { Draft } from './types'

/** Build update payload — only include fields that differ from remote settings. */
export function buildPayload(
  remote: BackupSettings,
  draft: Draft,
  password: string,
): Parameters<ReturnType<typeof useUpdateBackupSettings>['mutate']>[0] {
  const payload: Parameters<ReturnType<typeof useUpdateBackupSettings>['mutate']>[0] = {}

  if (draft.repository !== remote.repository) payload.repository = draft.repository
  if (password) payload.password = password
  if (draft.keepDaily !== remote.keepDaily) payload.keepDaily = draft.keepDaily
  if (draft.keepWeekly !== remote.keepWeekly) payload.keepWeekly = draft.keepWeekly
  if (draft.keepMonthly !== remote.keepMonthly) payload.keepMonthly = draft.keepMonthly
  if (draft.keepYearly !== remote.keepYearly) payload.keepYearly = draft.keepYearly
  if (draft.autoPrune !== remote.autoPrune) payload.autoPrune = draft.autoPrune
  if (draft.scheduleIntervalMinutes !== remote.scheduleIntervalMinutes)
    payload.scheduleIntervalMinutes = draft.scheduleIntervalMinutes
  if (draft.syncAfterBackup !== remote.syncAfterBackup) payload.syncAfterBackup = draft.syncAfterBackup
  if (draft.rcloneRemote !== remote.rcloneRemote) payload.rcloneRemote = draft.rcloneRemote
  if (draft.rclonePath !== remote.rclonePath) payload.rclonePath = draft.rclonePath
  if (draft.rcloneTransfers !== remote.rcloneTransfers) payload.rcloneTransfers = draft.rcloneTransfers

  return payload
}

export function toDraft(s: BackupSettings): Draft {
  return {
    repository: s.repository ?? '',
    keepDaily: s.keepDaily,
    keepWeekly: s.keepWeekly,
    keepMonthly: s.keepMonthly,
    keepYearly: s.keepYearly,
    autoPrune: s.autoPrune,
    scheduleIntervalMinutes: s.scheduleIntervalMinutes,
    syncAfterBackup: s.syncAfterBackup,
    rcloneRemote: s.rcloneRemote ?? '',
    rclonePath: s.rclonePath ?? '',
    rcloneTransfers: s.rcloneTransfers ?? 4,
  }
}
