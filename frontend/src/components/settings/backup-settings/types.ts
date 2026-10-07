export type Source = 'env' | 'db' | 'default'

/**
 * The six numeric fields below are null while their input is empty. Empty is
 * not 0 (0 disables the schedule / keeps no snapshots): useBackupForm blocks
 * Save until they are filled (agent-os-z91e.15).
 */
export interface Draft {
  repository: string
  keepDaily: number | null
  keepWeekly: number | null
  keepMonthly: number | null
  keepYearly: number | null
  autoPrune: boolean
  scheduleIntervalMinutes: number | null
  /** Whether backups run on a fixed interval or at a time of day. */
  scheduleMode: 'interval' | 'scheduled'
  /** "HH:MM" in server local time. */
  scheduleTime: string
  /** Go weekday ints, 0 = Sunday, ascending. */
  scheduleDays: number[]
  /**
   * Display-only, carried on the draft so ScheduleSection stays presentational
   * like its sibling sections. Never edited, and buildPayload never emits them.
   */
  serverTimezone: string
  serverTimeOffset: string
  syncAfterBackup: boolean
  rcloneRemote: string
  rclonePath: string
  rcloneTransfers: number | null
}
