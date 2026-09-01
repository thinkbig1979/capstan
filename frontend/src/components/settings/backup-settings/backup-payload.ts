import type { useUpdateBackupSettings } from '@/hooks/useBackup'
import type { BackupSettings } from '@/types'
import type { Draft } from './types'

/**
 * The schedule fields arrived after the rest of the settings, so a server that
 * predates them omits them. Normalizing on BOTH sides — the draft built from
 * settings, and the comparison in buildPayload — is what keeps an untouched
 * form from reading as dirty when a field is missing.
 */
function scheduleFields(s: BackupSettings) {
  return {
    scheduleMode: s.scheduleMode ?? 'interval',
    scheduleTime: s.scheduleTime ?? '03:00',
    scheduleDays: s.scheduleDays ?? [0, 1, 2, 3, 4, 5, 6],
  }
}

/**
 * Element-wise, because scheduleDays is an array and `!==` compares references:
 * a fresh array on every render would mark the field dirty every time, and
 * isDirty is derived from this payload, so the form would never go clean.
 * Both sides are canonical ascending ints, so index order is meaningful.
 */
function sameDays(a: number[], b: number[]): boolean {
  return a.length === b.length && a.every((day, i) => day === b[i])
}

/** Build update payload — only include fields that differ from remote settings. */
export function buildPayload(
  remote: BackupSettings,
  draft: Draft,
  password: string,
): Parameters<ReturnType<typeof useUpdateBackupSettings>['mutate']>[0] {
  const payload: Parameters<ReturnType<typeof useUpdateBackupSettings>['mutate']>[0] = {}
  const schedule = scheduleFields(remote)

  if (draft.repository !== remote.repository) payload.repository = draft.repository
  if (password) payload.password = password
  if (draft.keepDaily !== remote.keepDaily) payload.keepDaily = draft.keepDaily
  if (draft.keepWeekly !== remote.keepWeekly) payload.keepWeekly = draft.keepWeekly
  if (draft.keepMonthly !== remote.keepMonthly) payload.keepMonthly = draft.keepMonthly
  if (draft.keepYearly !== remote.keepYearly) payload.keepYearly = draft.keepYearly
  if (draft.autoPrune !== remote.autoPrune) payload.autoPrune = draft.autoPrune
  if (draft.scheduleIntervalMinutes !== remote.scheduleIntervalMinutes)
    payload.scheduleIntervalMinutes = draft.scheduleIntervalMinutes
  if (draft.scheduleMode !== schedule.scheduleMode) payload.scheduleMode = draft.scheduleMode
  if (draft.scheduleTime !== schedule.scheduleTime) payload.scheduleTime = draft.scheduleTime
  if (!sameDays(draft.scheduleDays, schedule.scheduleDays)) payload.scheduleDays = draft.scheduleDays
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
    ...scheduleFields(s),
    serverTimezone: s.serverTimezone ?? 'UTC',
    serverTimeOffset: s.serverTimeOffset ?? '+00:00',
    syncAfterBackup: s.syncAfterBackup,
    rcloneRemote: s.rcloneRemote ?? '',
    rclonePath: s.rclonePath ?? '',
    rcloneTransfers: s.rcloneTransfers ?? 4,
  }
}
