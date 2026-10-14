import * as React from 'react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

interface DrRestoreDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** The saved rclone remote name. The operator types it to enable Restore. */
  remote: string
  path: string
  /** The saved local restic repository path the restore replaces. */
  repository: string
  onConfirm: () => void
}

/**
 * Confirm step for the disaster-recovery repository pull. Not ConfirmDialog:
 * its description is one plain string, and this copy is several statements the
 * operator has to be able to read apart. The typed-name gate works the same way.
 *
 * The copy states what RunDRRestore does (services/backup.go): rclone sync from
 * the remote onto the repository with a sibling `--backup-dir`, then a check
 * that the result opens. It restores neither the database nor any stack.
 */
export function DrRestoreDialog({ open, onOpenChange, remote, path, repository, onConfirm }: DrRestoreDialogProps) {
  const [typed, setTyped] = React.useState('')

  // Reset the typed value each time the dialog opens, adjusted during render
  // against the previous `open` (same approach as ConfirmDialog).
  const [prevOpen, setPrevOpen] = React.useState(open)
  if (open !== prevOpen) {
    setPrevOpen(open)
    if (open) setTyped('')
  }

  const typedMatches = typed === remote

  const handleConfirm = () => {
    if (!typedMatches) return
    onConfirm()
    onOpenChange(false)
  }

  if (!open) return null

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center">
      <div className="fixed inset-0 bg-black/80" onClick={() => onOpenChange(false)} aria-hidden="true" />
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby="dr-restore-title"
        className="relative z-50 w-full max-w-lg rounded-lg border bg-background p-6 shadow-lg max-h-[90vh] overflow-y-auto"
      >
        <h3 id="dr-restore-title" className="text-lg font-semibold mb-3">
          Restore the repository from the remote?
        </h3>
        <div className="space-y-3 text-sm text-muted-foreground mb-6">
          <p>
            This makes the local restic repository at{' '}
            <span className="font-mono text-foreground break-all">{repository}</span> match the copy at{' '}
            <span className="font-mono text-foreground break-all">{`${remote}:${path}`}</span>.
          </p>
          <p>
            Files that exist only locally, or that the remote copy would replace, are not deleted. They are moved
            to a folder next to the repository, named <span className="font-mono">{repository}.pre-dr-&lt;time&gt;</span>.
            Capstan never removes that folder, so clear it out yourself once you are sure.
          </p>
          <p>
            When the copy finishes, Capstan checks that the repository opens with the saved password. If it does
            not, the run is marked failed and your files stay in that folder.
          </p>
          <p>
            This does not restore the database or your stacks. Those are separate steps, see Disaster recovery in
            the Restore a backup guide.
          </p>
          <p>
            It runs in the background and other backup operations are refused until it ends. The result appears in
            the backup history.
          </p>
        </div>
        <div className="mb-6 space-y-2">
          <Label htmlFor="dr-restore-typed" className="text-sm font-normal">
            Type <span className="font-mono font-semibold text-foreground">{remote}</span> to confirm
          </Label>
          <Input
            id="dr-restore-typed"
            value={typed}
            onChange={(e) => setTyped(e.target.value)}
            autoComplete="off"
            autoFocus
            onKeyDown={(e) => { if (e.key === 'Enter') handleConfirm() }}
          />
        </div>
        <div className="flex flex-col-reverse sm:flex-row sm:justify-end sm:space-x-2 gap-2">
          <Button variant="outline" onClick={() => onOpenChange(false)} className="min-h-[44px]">
            Cancel
          </Button>
          <Button variant="destructive" onClick={handleConfirm} disabled={!typedMatches} className="min-h-[44px]">
            Restore repository
          </Button>
        </div>
      </div>
    </div>
  )
}
