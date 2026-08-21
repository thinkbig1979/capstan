import { useState, useEffect, useMemo, useRef } from 'react'
import { StackDetail } from '@/components/stack/StackDetail'
import { Skeleton } from '@/components/ui/skeleton'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { AlertCircle, RefreshCw, Home, Trash2, ChevronDown, ChevronRight, MoreHorizontal, Play, Square, Download } from 'lucide-react'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { stacksApi } from '@/lib/api'
import { classifyError } from '@/lib/error-handler'
import { deleteStackWithCollateralConfirm, StackDeleteCancelledError } from '@/lib/stack-delete'
import { useParams, useNavigate, useLocation } from 'react-router'
import { toast } from 'sonner'
import { useConfirm } from '@/hooks/useConfirm'
import { useStackStore } from '@/stores/stackStore'
import { useCheckUpdates, useUpdateStack, useUpdateJobs } from '@/hooks/useResources'
import { useUpdateJobStore, type UpdateJob } from '@/stores/updateJobStore'
import { StackUpdateBadge } from '@/components/stack/StackUpdateBadge'
import { GitStatus } from '@/components/git/GitStatus'
import { UpdateJobLog } from '@/components/updates/UpdateJobLog'
import { OperationProgress } from '@/components/stack/OperationProgress'
import { Status as StatusPill, StatusDot, type StatusTone } from '@/components/ui/status'
import { HelpHint } from '@/components/ui/help-hint'
import { useStreamingOperation } from '@/hooks/useStreamingOperation'
import { stackUptime } from '@/lib/uptime'
import type { StackStatus } from '@/types'
import { queryKeys } from '@/lib/query-keys'

// Same status → tone/label mapping as the dashboard's StatusBadge: stopped is
// a normal state (neutral), red is reserved for actual errors.
const STATUS_PILL: Record<StackStatus, { label: string; tone: StatusTone }> = {
  running: { label: 'Running', tone: 'success' },
  stopped: { label: 'Stopped', tone: 'neutral' },
  partial: { label: 'Partial', tone: 'warning' },
  error: { label: 'Error', tone: 'error' },
  unknown: { label: 'Unknown', tone: 'neutral' },
}

// Most recently created job (by createdAt) without copying/sorting the array.
function latestByCreatedAt(jobs: UpdateJob[]): UpdateJob | undefined {
  return jobs.reduce<UpdateJob | undefined>(
    (latest, j) => (!latest || j.createdAt > latest.createdAt ? j : latest),
    undefined,
  )
}

export function StackPage() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const location = useLocation()
  const queryClient = useQueryClient()
  const { confirm, ConfirmComponent } = useConfirm()
  const [isDeleting, setIsDeleting] = useState(false)
  const { setSelectedStack } = useStackStore()

  const activeTab = location.pathname.split('/').slice(3).join('/') || 'overview'

  useEffect(() => {
    setSelectedStack(id ?? null)
  }, [id, setSelectedStack])

  // Old deep-link tabs redirect to the merged tab set: Compose/Environment/
  // Compose+Env became the Editor split view, History/Updates/Backups became
  // sections of the Activity tab (the section rides in ?view=).
  useEffect(() => {
    if (!id) return
    const redirects: Record<string, string> = {
      compose: 'editor',
      environment: 'editor',
      split: 'editor',
      history: 'activity?view=history',
      updates: 'activity?view=updates',
      backups: 'activity?view=backups',
    }
    const target = redirects[activeTab]
    if (target) navigate(`/stacks/${id}/${target}`, { replace: true })
  }, [activeTab, id, navigate])

  const handleTabChange = (newTab: string) => {
    navigate(`/stacks/${id}/${newTab}`, { replace: true })
  }

  const { data: stack, isLoading, error, refetch } = useQuery({
    // `id` is optional in the route params; the query is gated by `enabled`
    // below, so the '' key is only ever registered for a disabled query. Matches
    // the same coercion queryFn already applies.
    queryKey: queryKeys.stack.detail(id ?? ''),
    queryFn: () => stacksApi.get(id || ''),
    enabled: !!id,
    retry: 1,
  })

  const deleteMutation = useMutation({
    // Re-confirms with `confirm` (the same dialog as the initial delete
    // confirmation) when the backend refuses with 428 STACK_DELETE_COLLATERAL.
    // See deleteStackWithCollateralConfirm.
    mutationFn: (stackId: string) => deleteStackWithCollateralConfirm(stackId, confirm),
    onSuccess: () => {
      toast.success('Stack deleted successfully')
      queryClient.invalidateQueries({ queryKey: queryKeys.stacks() })
      navigate('/')
    },
    onError: (err) => {
      // A declined collateral confirmation is a user cancel, not a failure —
      // no error toast, just re-enable the Delete button.
      if (!(err instanceof StackDeleteCancelledError)) {
        toast.error('Failed to delete stack')
      }
      setIsDeleting(false)
    },
  })

  // Hydrate update jobs store so stack job status is available on mount
  useUpdateJobs()

  // Lifecycle operations (start/stop/restart/pull) live at the page level so
  // the header action buttons and the tab content share one stream; the
  // progress panel renders below the header for whichever tab is active.
  const operation = useStreamingOperation()
  const isStarting = operation.status === 'running' && operation.action === 'start'
  const isStopping = operation.status === 'running' && operation.action === 'stop'
  const isRestarting = operation.status === 'running' && operation.action === 'restart'
  const isPulling = operation.status === 'running' && operation.action === 'pull'
  const anyRunning = isStarting || isStopping || isRestarting || isPulling

  useEffect(() => {
    if (operation.status === 'success' && id) {
      queryClient.invalidateQueries({ queryKey: queryKeys.stack.detail(id) })
      queryClient.invalidateQueries({ queryKey: queryKeys.stacks() })
      toast.success(`${operation.action.charAt(0).toUpperCase() + operation.action.slice(1)} completed`)
    }
  }, [operation.status, operation.action, id, queryClient])

  // Available updates for this stack
  const { data: updateData } = useCheckUpdates()
  const updates = updateData?.updates
  const stackUpdatesCount = useMemo(() => {
    if (!updates || !id) return 0
    return updates.filter((u) => u.stackId === id).length
  }, [updates, id])

  // Stack job state. Derive from the raw jobs map so the memoized values keep a
  // stable identity across renders (a selector returning a fresh array each call
  // would make every dependent memo/effect re-run on every render).
  const updateStackMutation = useUpdateStack()
  const jobsMap = useUpdateJobStore((s) => s.jobs)
  const stackJobs = useMemo(
    () => (id ? Object.values(jobsMap).filter((j) => j.stackId === id) : []),
    [jobsMap, id],
  )

  // Most recent active job, for the button's live phase label.
  const activeJob = useMemo(
    () =>
      latestByCreatedAt(
        stackJobs.filter(
          (j) => j.status === 'queued' || j.status === 'pulling' || j.status === 'recreating',
        ),
      ),
    [stackJobs],
  )

  // Most recent stack job overall (active or finished), to report the outcome.
  const latestStackJob = useMemo(() => latestByCreatedAt(stackJobs), [stackJobs])

  // Collapsible live-output terminal for the stack update. Shown automatically
  // while an update is running (derived, not synced) and manually toggleable so
  // it stays available after the job finishes.
  const [updateLogOpen, setUpdateLogOpen] = useState(false)
  const updateLogVisible = updateLogOpen || activeJob != null

  // Surface the terminal outcome once per job. On a fail-fast partial update the
  // job error already names which services were left un-updated (see backend), so
  // the user sees what happened and the impact without opening the Updates tab.
  //
  // Toast derives from the typed `outcome` field (truth-first) so that:
  //   outcome='success'   → green "Stack updated and restarted"
  //   outcome='no_change' → info  "Stack already up to date" (NOT a green success)
  //   outcome='failed'    → error message with reason
  // Falls back to status for backends that have not yet shipped the outcome field.
  const reportedJobRef = useRef<string | null>(null)
  const didInitJobRef = useRef(false)
  useEffect(() => {
    const isTerminal = (s?: string) => s === 'success' || s === 'error'
    // On first run, treat a job that was already terminal when we arrived as
    // already-reported, so we don't toast a stale outcome on mount/navigation.
    if (!didInitJobRef.current) {
      didInitJobRef.current = true
      if (latestStackJob && isTerminal(latestStackJob.status)) {
        reportedJobRef.current = latestStackJob.id
        return
      }
    }
    if (!latestStackJob || !isTerminal(latestStackJob.status)) return
    const { id: jobId, error, outcome, reason } = latestStackJob
    if (reportedJobRef.current === jobId) return
    reportedJobRef.current = jobId

    // Derive strictly from the typed outcome — the backend always sends it now.
    // A missing or non-success outcome must NOT be reported as success, even if
    // the coarse job status happens to be 'success' (B1 frontend tightening).
    if (outcome === 'success') {
      toast.success(reason || 'Stack updated and restarted')
    } else if (outcome === 'no_change') {
      toast.info(reason || 'Stack already up to date')
    } else {
      // outcome='failed', an unknown/missing outcome, or status='error'.
      toast.error(reason || error || 'Stack update failed', { duration: 12000 })
    }
  }, [latestStackJob])

  const handleStackUpdate = () => {
    if (!id) return
    updateStackMutation.mutate(id, {
      onSuccess: (data) => {
        if (!data.jobId || data.noUpdates) {
          toast.info('No updates available for this stack')
        }
      },
      onError: (err) => {
        toast.error(classifyError(err).message || 'Failed to start stack update')
      },
    })
  }

  if (isLoading) {
    return (
      <div className="space-y-6">
        <div>
          <Skeleton className="h-8 w-64" />
          <Skeleton className="mt-2 h-4 w-96" />
        </div>
        <div className="space-y-4">
          <Skeleton className="h-64 w-full" />
        </div>
      </div>
    )
  }

  if (error || !stack) {
    const appError = error ? classifyError(error) : null

    return (
      <div className="space-y-6">
        <div>
          <h1 className="text-3xl font-bold tracking-tight">Stack Not Found</h1>
          <p className="text-muted-foreground">
            {appError?.message || 'The requested stack could not be found.'}
          </p>
        </div>

        {appError && (
          <Card className="border-destructive">
            <CardContent className="pt-6">
              <div className="flex items-start gap-4">
                <AlertCircle className="h-5 w-5 text-destructive mt-0.5" />
                <div className="flex-1 space-y-2">
                  <h3 className="font-semibold">Failed to load stack</h3>
                  <p className="text-sm text-muted-foreground">{appError.message}</p>
                  <div className="flex gap-2">
                    <Button
                      onClick={() => refetch()}
                      disabled={!appError.retryable}
                      variant="outline"
                      size="sm"
                    >
                      <RefreshCw className="mr-2 h-4 w-4" />
                      Retry
                    </Button>
                    <Button
                      onClick={() => navigate('/')}
                      variant="outline"
                      size="sm"
                    >
                      <Home className="mr-2 h-4 w-4" />
                      Dashboard
                    </Button>
                    {appError.type === 'auth' && (
                      <Button
                        onClick={() => navigate('/login')}
                        variant="outline"
                        size="sm"
                      >
                        Login
                      </Button>
                    )}
                  </div>
                </div>
              </div>
            </CardContent>
          </Card>
        )}

        {!appError && (
          <Button onClick={() => navigate('/')} variant="outline">
            <Home className="mr-2 h-4 w-4" />
            Back to Dashboard
          </Button>
        )}
      </div>
    )
  }

  const handleDelete = async () => {
    if (!stack) return
    const confirmed = await confirm(
      `Delete Stack "${stack.projectName}"?`,
      `This permanently removes the stack and everything under ${stack.directory}, including its compose file and any data. This cannot be undone.`,
      { confirmText: 'Delete', isDangerous: true, requireConfirmationText: stack.projectName }
    )
    if (confirmed) {
      setIsDeleting(true)
      deleteMutation.mutate(stack.id)
    }
  }

  const pill = STATUS_PILL[stack.status] || STATUS_PILL.unknown
  const containerCount = stack.containers?.length ?? 0
  const runningCount = stack.containers?.filter((c) => c.state === 'running').length ?? 0
  const uptime = stackUptime(stack.containers)
  const canStart = stack.status === 'stopped' || stack.status === 'partial'
  const canStop = stack.status === 'running'

  return (
    <>
      <div className="space-y-6">
        <div className="flex items-start justify-between gap-4 flex-wrap">
          <div className="min-w-0 flex-1">
            <h1 className="text-2xl font-bold tracking-tight truncate font-mono">{stack.projectName}</h1>
            <div className="mt-1.5 flex items-center gap-2 flex-wrap">
              <StatusPill
                tone={pill.tone}
                className="gap-1.5 text-[11px] font-semibold uppercase tracking-wider"
              >
                <StatusDot tone={pill.tone} pulse={stack.status === 'running'} />
                {containerCount > 0 ? `${pill.label} · ${runningCount}/${containerCount}` : pill.label}
              </StatusPill>
              <p className="text-sm text-muted-foreground truncate font-mono">{stack.directory}</p>
              <GitStatus stack={stack} />
              {uptime && (
                <span className="inline-flex items-center rounded-full bg-muted px-2.5 py-0.5 text-[11px] font-mono text-muted-foreground">
                  {uptime}
                </span>
              )}
            </div>
          </div>
          {/* Stack lifecycle actions live in the header so they work from any
              tab; the streaming operation they share renders its progress
              panel below. Destructive actions stay behind the overflow menu. */}
          <div className="flex items-center gap-2 flex-wrap shrink-0">
            <Button variant="outline" size="sm" onClick={() => operation.execute(stack.id, 'start')} disabled={!canStart || anyRunning}>
              <Play className="mr-1.5 h-4 w-4" />
              {isStarting ? 'Starting...' : 'Start'}
            </Button>
            <Button variant="outline" size="sm" onClick={() => operation.execute(stack.id, 'stop')} disabled={!canStop || anyRunning}>
              <Square className="mr-1.5 h-4 w-4" />
              {isStopping ? 'Stopping...' : 'Stop'}
            </Button>
            <Button variant="outline" size="sm" onClick={() => operation.execute(stack.id, 'restart')} disabled={!canStop || anyRunning}>
              <RefreshCw className={`mr-1.5 h-4 w-4 ${isRestarting ? 'animate-spin' : ''}`} />
              {isRestarting ? 'Restarting...' : 'Restart'}
            </Button>
            <div className="flex items-center gap-1">
              <Button variant="outline" size="sm" onClick={() => operation.execute(stack.id, 'pull')} disabled={anyRunning}>
                <Download className={`mr-1.5 h-4 w-4 ${isPulling ? 'animate-spin' : ''}`} />
                {isPulling ? 'Pulling...' : 'Pull Images'}
              </Button>
              <HelpHint label="Pull images" title="Pull images">
                <p>Downloads the latest image for each service without restarting anything.</p>
                <p>
                  Running containers keep using the old image until you restart or recreate them,
                  so this just stages the update.
                </p>
              </HelpHint>
            </div>
            <StackUpdateBadge
              count={stackUpdatesCount}
              onUpdate={handleStackUpdate}
              jobStatus={activeJob?.status}
              updatePending={updateStackMutation.isPending}
            />
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button
                variant="outline"
                size="icon"
                className="shrink-0"
                title="More actions"
                aria-label="More stack actions"
              >
                <MoreHorizontal className="h-4 w-4" />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              <DropdownMenuItem
                className="text-destructive focus:text-destructive"
                disabled={isDeleting || deleteMutation.isPending}
                onClick={handleDelete}
              >
                <Trash2 className="h-4 w-4" />
                Delete Stack
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
          </div>
        </div>

        {latestStackJob && (
          <div className="space-y-2">
            <button
              type="button"
              onClick={() => setUpdateLogOpen((o) => !o)}
              className="flex items-center gap-1.5 text-sm text-muted-foreground transition-colors hover:text-foreground"
            >
              {updateLogVisible ? <ChevronDown className="h-4 w-4" /> : <ChevronRight className="h-4 w-4" />}
              Update output
            </button>
            {updateLogVisible && <UpdateJobLog job={latestStackJob} enabled={updateLogVisible} />}
          </div>
        )}

        <StackDetail
          key={stack.id}
          stack={stack}
          activeTab={activeTab}
          onTabChange={handleTabChange}
        />

        <OperationProgress
          status={operation.status}
          lines={operation.lines}
          action={operation.action}
          error={operation.error}
          onDismiss={operation.reset}
        />
      </div>
      <ConfirmComponent />
    </>
  )
}
