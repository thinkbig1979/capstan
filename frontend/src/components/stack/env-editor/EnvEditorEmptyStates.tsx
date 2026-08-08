import { Button } from '@/components/ui/button'
import { Lock } from 'lucide-react'

interface EnvLockedNoticeProps {
  onUnlock: () => void
}

/**
 * Shown when the server withheld the secret values because this session has not
 * re-entered its password. It says editing is unavailable rather than letting the
 * user type into a form whose save the backend will refuse (agent-os-7o5s).
 */
export function EnvLockedNotice({ onUnlock }: EnvLockedNoticeProps) {
  return (
    <div className="flex flex-wrap items-center justify-between gap-3 rounded-md border border-warning/30 bg-warning/10 px-3 py-2 text-sm">
      <div className="flex items-center gap-2">
        <Lock className="h-4 w-4 shrink-0 text-warning" />
        <span>
          Values that look like secrets are hidden and editing is disabled. Enter your password
          to reveal and edit them.
        </span>
      </div>
      <Button type="button" variant="outline" size="sm" onClick={onUnlock}>
        Unlock
      </Button>
    </div>
  )
}

export function EnvLoadingState() {
  return <div className="flex items-center justify-center py-8">Loading...</div>
}

interface EnvErrorStateProps {
  onRetry: () => void
}

export function EnvErrorState({ onRetry }: EnvErrorStateProps) {
  return (
    <div className="flex flex-col items-center justify-center py-8 text-muted-foreground">
      <p>Failed to load environment file</p>
      <Button variant="outline" onClick={onRetry} className="mt-4">
        Retry
      </Button>
    </div>
  )
}

interface EnvNoFileStateProps {
  onCreate: () => void
  creating: boolean
}

export function EnvNoFileState({ onCreate, creating }: EnvNoFileStateProps) {
  return (
    <div className="flex flex-col items-center justify-center py-8 text-muted-foreground">
      <p>No environment file found for this stack</p>
      <Button variant="outline" onClick={onCreate} disabled={creating} className="mt-4">
        {creating ? 'Creating...' : 'Create Environment File'}
      </Button>
    </div>
  )
}
