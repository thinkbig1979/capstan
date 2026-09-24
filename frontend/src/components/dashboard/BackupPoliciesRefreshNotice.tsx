import { RefreshFailedNotice } from '@/components/RefreshFailedNotice'
import { useBackupPolicies } from '@/hooks/useBackup'

/**
 * agent-os-r6fx: a failed REFETCH of the backup policies keeps the last list,
 * and every BackupToggle seeds BOTH fields of its next write from it (changing
 * the stop policy re-sends `enabled`, toggling re-sends `stopPolicy`). Mounted
 * ONCE per surface that renders BackupToggles, not inside each toggle: they
 * sit one per table row and share one query, and the rule is one notice per
 * rendered list (agent-os-6iui). Its own module, not an export of
 * BackupToggle.tsx, because the parents' tests replace that module wholesale.
 */
export function BackupPoliciesRefreshNotice({ className }: { className?: string }) {
  const { data, isError, refetch } = useBackupPolicies()
  if (!isError || !data) return null
  return (
    <RefreshFailedNotice
      what="the backup settings"
      beforeSave
      onRetry={() => void refetch()}
      className={className}
    />
  )
}
