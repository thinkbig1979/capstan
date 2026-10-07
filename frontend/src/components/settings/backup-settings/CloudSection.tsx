import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NumericField } from '../NumericField'
import { Label } from '@/components/ui/label'
import { LoadingSpinner } from '@/components/LoadingSkeleton'
import { HelpHint } from '@/components/ui/help-hint'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import type { SyncPreflightResponse } from '@/types'
import type { Draft } from './types'

interface CloudSectionProps {
  draft: Draft
  onChange: <K extends keyof Draft>(key: K, value: Draft[K]) => void
  rcloneAvailable: boolean
  onTestCloud: () => void
  isTestingCloud: boolean
  onSyncNow: () => void
  isSyncing: boolean
  /** The form has unsaved edits; a sync runs on the SAVED settings. */
  isDirty: boolean
  /** A pre-flight count above the cap, awaiting confirmation (agent-os-z91e.9). */
  pendingLargeDelete: SyncPreflightResponse | null
  onConfirmLargeDelete: () => void
  onLargeDeleteOpenChange: (open: boolean) => void
}

function syncBlockedReason(rcloneAvailable: boolean, remote: string, isDirty: boolean): string | undefined {
  if (!rcloneAvailable) return 'rclone not available'
  if (!remote) return 'Set and save a remote first'
  if (isDirty) return 'Save your changes first: a sync uses the saved settings'
  return undefined
}

export function CloudSection({
  draft,
  onChange,
  rcloneAvailable,
  onTestCloud,
  isTestingCloud,
  onSyncNow,
  isSyncing,
  isDirty,
  pendingLargeDelete,
  onConfirmLargeDelete,
  onLargeDeleteOpenChange,
}: CloudSectionProps) {
  const syncBlocked = syncBlockedReason(rcloneAvailable, draft.rcloneRemote, isDirty)
  return (
    <div className="space-y-4 pt-4 border-t">
      <div className="flex items-center gap-1.5">
        <h3 className="text-lg font-medium">Cloud (rclone)</h3>
        <HelpHint
          label="Cloud sync"
          title="Cloud sync"
          side="right"
          href="https://github.com/thinkbig1979/capstan/blob/main/docs/how-to/configure-backups.md#cloud-sync-optional"
        >
          <p>rclone copies the restic repository to off-site storage like S3 or Backblaze.</p>
          <p>
            &apos;Remote&apos; is the name you gave that storage in your rclone config, and
            &apos;path&apos; is the folder inside it. Test connectivity before you depend on it.
          </p>
        </HelpHint>
      </div>

      <div className="space-y-2">
        <Label htmlFor="backup-rclone-remote">Remote</Label>
        <Input
          id="backup-rclone-remote"
          type="text"
          placeholder="myremote"
          value={draft.rcloneRemote}
          onChange={(e) => onChange('rcloneRemote', e.target.value)}
          className="max-w-md"
        />
        <p className="text-xs text-muted-foreground">
          Name of the rclone remote as configured in your rclone config.
        </p>
      </div>

      <div className="space-y-2">
        <Label htmlFor="backup-rclone-path">Path on remote</Label>
        <Input
          id="backup-rclone-path"
          type="text"
          placeholder="bucket/backups"
          value={draft.rclonePath}
          onChange={(e) => onChange('rclonePath', e.target.value)}
          className="max-w-md"
        />
      </div>

      <div className="space-y-2">
        <Label htmlFor="backup-rclone-transfers">Parallel transfers</Label>
        <NumericField
          id="backup-rclone-transfers"
          min={1}
          max={32}
          value={draft.rcloneTransfers}
          onValueChange={(v) => onChange('rcloneTransfers', v)}
          className="max-w-xs"
        />
        <p className="text-xs text-muted-foreground">
          Number of parallel file transfers (rclone <code>--transfers</code>). Default: 4.
        </p>
      </div>

      <Button
        type="button"
        variant="outline"
        size="sm"
        onClick={onTestCloud}
        disabled={isTestingCloud || !rcloneAvailable}
        title={!rcloneAvailable ? 'rclone not available' : undefined}
      >
        {isTestingCloud ? (
          <>
            <span className="mr-2"><LoadingSpinner size="small" /></span>
            Testing…
          </>
        ) : (
          'Test connectivity'
        )}
      </Button>

      <Button
        type="button"
        variant="outline"
        size="sm"
        className="ml-2"
        onClick={onSyncNow}
        disabled={isSyncing || syncBlocked !== undefined}
        title={syncBlocked}
      >
        {isSyncing ? (
          <>
            <span className="mr-2"><LoadingSpinner size="small" /></span>
            Syncing…
          </>
        ) : (
          'Sync now'
        )}
      </Button>

      <ConfirmDialog
        open={pendingLargeDelete !== null}
        onOpenChange={onLargeDeleteOpenChange}
        title="Delete files from the remote?"
        description={
          pendingLargeDelete
            ? `This sync would delete ${pendingLargeDelete.remoteOnly} files from the remote, more than the ${pendingLargeDelete.cap} a sync deletes without asking. ` +
              'That is expected right after a forget/prune. If you have not pruned, the local repository may have lost files: cancel and check it with restic check.'
            : ''
        }
        confirmText={pendingLargeDelete ? `Sync and delete ${pendingLargeDelete.remoteOnly} files` : 'Sync'}
        onConfirm={onConfirmLargeDelete}
        isDangerous
      />
    </div>
  )
}
