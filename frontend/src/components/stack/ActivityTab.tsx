import { useSearchParams } from 'react-router'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { TabErrorBoundary } from '@/components/TabErrorBoundary'
import { GitHistory } from '../git/GitHistory'
import { StackUpdatesTab } from './StackUpdatesTab'
import { BackupsTab } from './BackupsTab'

interface ActivityTabProps {
  stackId: string
}

const VIEWS = ['history', 'updates', 'backups'] as const
type ActivityView = (typeof VIEWS)[number]

/**
 * Merged History / Updates / Backups tab. The active section is kept in the
 * ?view= search param so old per-tab deep links (redirected by StackPage)
 * land on the right section and the section survives reloads.
 *
 * Tab ORDER is JSX position only. The `value` strings are the ?view= contract
 * shared with StackPage's legacy-route redirects, so they are not display names
 * and must not be renamed to follow a label.
 */
export function ActivityTab({ stackId }: ActivityTabProps) {
  const [searchParams, setSearchParams] = useSearchParams()
  const viewParam = searchParams.get('view')
  const view: ActivityView = VIEWS.includes(viewParam as ActivityView)
    ? (viewParam as ActivityView)
    : 'history'

  const setView = (next: string) => {
    setSearchParams(next === 'history' ? {} : { view: next }, { replace: true })
  }

  return (
    <Tabs value={view} onValueChange={setView}>
      <TabsList>
        <TabsTrigger value="updates">Updates</TabsTrigger>
        <TabsTrigger value="backups">Backups</TabsTrigger>
        <TabsTrigger value="history">Git History</TabsTrigger>
      </TabsList>

      <TabsContent value="updates" className="mt-4">
        <TabErrorBoundary>
          <StackUpdatesTab stackId={stackId} />
        </TabErrorBoundary>
      </TabsContent>

      <TabsContent value="backups" className="mt-4">
        <TabErrorBoundary>
          <BackupsTab stackId={stackId} />
        </TabErrorBoundary>
      </TabsContent>

      <TabsContent value="history" className="mt-4">
        <TabErrorBoundary>
          <GitHistory stackId={stackId} />
        </TabErrorBoundary>
      </TabsContent>
    </Tabs>
  )
}
