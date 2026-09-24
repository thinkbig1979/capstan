import type { BackupRun } from '@/types'

/**
 * How a run kind reads in a sentence ("Last restore failed", "Last prune 5m
 * ago"). Shared by BackupToggle and the sidebar's BackupStatusFooter, which
 * both describe `lastRun`: the newest run of ANY kind, so hardcoding "backup"
 * in either calls a restore or a prune a backup (agent-os-4zx0).
 *
 * An exhaustive Record so a kind added to the union is a compile error here
 * rather than a sentence with a blank in it.
 */
export const RUN_KIND_LABEL: Record<BackupRun['kind'], string> = {
  backup: 'backup',
  sync: 'sync',
  restore: 'restore',
  dr_restore: 'DR restore',
  prune: 'prune',
  verify: 'repository check',
}
