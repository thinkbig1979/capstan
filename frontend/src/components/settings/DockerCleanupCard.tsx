import { Link } from 'react-router'
import { Button } from '@/components/ui/button'

/** The Settings section for Docker cleanup. The schedule itself moved to the
 *  Docker host page (D94), beside the numbers it acts on; this section stays so
 *  the Settings search, bookmarks and help links to it keep working. */
export function DockerCleanupCard() {
  return (
    <div data-testid="settings-docker-cleanup-moved" className="space-y-3">
      <h3 className="text-lg font-medium">Docker cleanup</h3>
      <p className="text-sm text-muted-foreground">Docker cleanup moved to the Docker host page.</p>
      <Button asChild variant="outline">
        <Link to="/host#automatic-cleanup">Open automatic cleanup</Link>
      </Button>
    </div>
  )
}
