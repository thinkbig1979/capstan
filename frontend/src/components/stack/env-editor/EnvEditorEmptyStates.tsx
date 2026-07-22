import { Button } from '@/components/ui/button'

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
