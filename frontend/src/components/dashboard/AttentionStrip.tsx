import { useNavigate } from 'react-router'
import { X } from 'lucide-react'
import { useCheckUpdates, useDockerCleanupPolicy } from '@/hooks/useResources'
import { useHostReclaimable, useHostSummary } from '@/hooks/useHostSpace'
import { useUIStore } from '@/stores/uiStore'
import { formatBytes } from '@/lib/format'
import type { Stack } from '@/types'

interface AttentionStripProps {
  stacks: Stack[]
  onShowUpdates: () => void
  onFilterStopped: () => void
  onFilterError: () => void
}

interface Signal {
  key: string
  tone: 'warn' | 'err'
  text: React.ReactNode
  action: string
  onAction: () => void
  testId?: string
  /** Shows a dismiss button with this accessible name. */
  dismissLabel?: string
  onDismiss?: () => void
}

/**
 * D98-C: warn when one click could free at least the policy's threshold. The
 * threshold is the last policy read (kept through a failed refetch); with no
 * policy read yet, or a threshold of 0, there is no alert.
 */
function useHostSpaceAlert() {
  const reclaimable = useHostReclaimable()
  const { data: policy } = useDockerCleanupPolicy()
  const dismissedUntil = useUIStore((s) => s.hostSpaceAlertDismissedUntil)
  const dismiss = useUIStore((s) => s.dismissHostSpaceAlert)
  const threshold = policy?.alertThresholdBytes ?? 0
  // Dismissed as of the latest measurement: the first summary poll after the
  // 7 days brings the alert back without a reload. dataUpdatedAt, not
  // Date.now(), keeps the render pure.
  const { dataUpdatedAt } = useHostSummary()
  const dismissed = dismissedUntil !== null && dismissedUntil > dataUpdatedAt
  const show = reclaimable !== undefined && threshold > 0 && reclaimable >= threshold && !dismissed
  return { show, reclaimable: reclaimable ?? 0, dismiss }
}

/**
 * Renders only non-zero signals (available updates, stopped stacks, errored
 * stacks, unused Docker data over the alert threshold) above the fleet table;
 * renders nothing when all is quiet.
 */
export function AttentionStrip({
  stacks,
  onShowUpdates,
  onFilterStopped,
  onFilterError,
}: AttentionStripProps) {
  const { data: updateData } = useCheckUpdates()
  const updates = updateData?.updates ?? []
  const updateStackCount = new Set(updates.map((u) => u.stackId)).size
  const stopped = stacks.filter((s) => s.status === 'stopped').length
  const errored = stacks.filter((s) => s.status === 'error').length
  const navigate = useNavigate()
  const hostSpace = useHostSpaceAlert()

  const signals: Signal[] = []
  if (updates.length > 0) {
    signals.push({
      key: 'updates',
      tone: 'warn',
      text: (
        <>
          <b className="font-semibold">{updates.length} image update{updates.length !== 1 ? 's' : ''}</b>
          {' '}across {updateStackCount} stack{updateStackCount !== 1 ? 's' : ''}
        </>
      ),
      action: 'Review',
      onAction: onShowUpdates,
    })
  }
  if (errored > 0) {
    signals.push({
      key: 'errored',
      tone: 'err',
      text: (
        <>
          <b className="font-semibold">{errored} stack{errored !== 1 ? 's' : ''}</b> in error
        </>
      ),
      action: 'Show',
      onAction: onFilterError,
    })
  }
  if (stopped > 0) {
    signals.push({
      key: 'stopped',
      tone: 'warn',
      text: (
        <>
          <b className="font-semibold">{stopped} stack{stopped !== 1 ? 's' : ''}</b> stopped
        </>
      ),
      action: 'Show',
      onAction: onFilterStopped,
    })
  }

  if (hostSpace.show) {
    signals.push({
      key: 'host-space',
      testId: 'attention-host-space',
      tone: 'warn',
      text: (
        <>
          <b className="font-semibold">{formatBytes(hostSpace.reclaimable)}</b> of unused Docker data on this server
        </>
      ),
      action: 'Free up space',
      onAction: () => navigate('/host?free=1'),
      dismissLabel: 'Dismiss for 7 days',
      onDismiss: hostSpace.dismiss,
    })
  }

  if (signals.length === 0) return null

  return (
    <div className="flex flex-wrap gap-2.5" data-testid="attention-strip">
      {signals.map((s) => (
        <div
          key={s.key}
          data-testid={s.testId}
          className={`flex items-center gap-2.5 rounded-md border bg-card py-2 pl-3 pr-3.5 text-sm border-l-[3px] ${
            s.tone === 'err' ? 'border-l-destructive' : 'border-l-warning'
          }`}
        >
          <span
            aria-hidden="true"
            className={`h-1.5 w-1.5 rounded-full ${s.tone === 'err' ? 'bg-destructive' : 'bg-warning'}`}
          />
          <span>{s.text}</span>
          <button
            type="button"
            onClick={s.onAction}
            className="font-semibold text-primary underline underline-offset-2 whitespace-nowrap"
          >
            {s.action} →
          </button>
          {s.onDismiss && (
            <button
              type="button"
              onClick={s.onDismiss}
              aria-label={s.dismissLabel}
              title={s.dismissLabel}
              // 44 px touch target on narrow screens, compact beside the text above sm.
              className="-my-2 -mr-2 inline-flex h-11 w-11 shrink-0 items-center justify-center rounded text-muted-foreground hover:text-foreground sm:my-0 sm:mr-0 sm:h-6 sm:w-6"
            >
              <X className="h-3.5 w-3.5" aria-hidden="true" />
            </button>
          )}
        </div>
      ))}
    </div>
  )
}
