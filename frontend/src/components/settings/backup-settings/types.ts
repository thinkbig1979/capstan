export type Source = 'env' | 'db' | 'default'

export interface Draft {
  repository: string
  keepDaily: number
  keepWeekly: number
  keepMonthly: number
  keepYearly: number
  autoPrune: boolean
  scheduleIntervalMinutes: number
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
  rcloneTransfers: number
}
