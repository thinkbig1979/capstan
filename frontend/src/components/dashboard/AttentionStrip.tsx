import { useCheckUpdates } from '@/hooks/useResources'
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
}

/**
 * Renders only non-zero signals (available updates, stopped stacks, errored
 * stacks) above the fleet table; renders nothing when all is quiet.
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

  if (signals.length === 0) return null

  return (
    <div className="flex flex-wrap gap-2.5" data-testid="attention-strip">
      {signals.map((s) => (
        <div
          key={s.key}
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
            className="font-semibold text-primary hover:underline whitespace-nowrap"
          >
            {s.action} →
          </button>
        </div>
      ))}
    </div>
  )
}
