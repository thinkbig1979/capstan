import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { LoadingSpinner } from '@/components/LoadingSkeleton'
import { HelpHint } from '@/components/ui/help-hint'
import type { Draft } from './types'

interface CloudSectionProps {
  draft: Draft
  onChange: <K extends keyof Draft>(key: K, value: Draft[K]) => void
  rcloneAvailable: boolean
  onTestCloud: () => void
  isTestingCloud: boolean
}

export function CloudSection({ draft, onChange, rcloneAvailable, onTestCloud, isTestingCloud }: CloudSectionProps) {
  return (
    <div className="space-y-4 pt-4 border-t">
      <div className="flex items-center gap-1.5">
        <h3 className="text-lg font-medium">Cloud (rclone)</h3>
        <HelpHint label="Cloud sync" title="Cloud sync" side="right">
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
        <Input
          id="backup-rclone-transfers"
          type="number"
          min={1}
          max={32}
          value={draft.rcloneTransfers}
          onChange={(e) => onChange('rcloneTransfers', parseInt(e.target.value, 10) || 4)}
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
    </div>
  )
}
