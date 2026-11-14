import { Link } from 'react-router'
import { Button } from '@/components/ui/button'
import { AUTO_CLEANUP_HREF } from '@/components/host/host-views'

/** The Settings section for Docker cleanup. The schedule itself moved to the
 *  dashboard's Host tab (D94, agent-os-mi4r), beside the numbers it acts on; this section stays so
 *  the Settings search, bookmarks and help links to it keep working. */
export function DockerCleanupCard() {
  return (
    <div data-testid="settings-docker-cleanup-moved" className="space-y-3">
      <h3 className="text-lg font-medium">Docker cleanup</h3>
      <p className="text-sm text-muted-foreground">Docker cleanup moved to the Host tab on the dashboard.</p>
      <Button asChild variant="outline">
        <Link to={AUTO_CLEANUP_HREF}>Open automatic cleanup</Link>
      </Button>
    </div>
  )
}
