import * as React from 'react'
import { Link } from 'react-router'
import { toast } from 'sonner'
import { Loader2 } from 'lucide-react'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { LoadFailedNotice } from '@/components/LoadFailedNotice'
import { useDockerCleanupPolicy } from '@/hooks/useResources'
import { useAddAutoCleanupCategories, useFreeSpace, useHostCandidates } from '@/hooks/useHostSpace'
import { presentError } from '@/lib/error-handler'
import { formatBytes } from '@/lib/format'
import type { DockerCleanupCategories, DockerCleanupPolicy } from '@/types'
import type { HostCandidateItem, HostCandidates, HostFreeSpaceRequest } from '@/types/generated-services'

type RowId = 'images' | 'buildCache' | 'containers' | 'volumes' | 'networks'
type CategoryKey = keyof DockerCleanupCategories

const ROW_ORDER: readonly RowId[] = ['images', 'buildCache', 'containers', 'volumes', 'networks']

/** Ticked when the dialog opens (spec "Reclaimable rules"); their total is the "Free up N" headline (D98-B). */
const DEFAULT_TICKED: Record<RowId, boolean> = {
  images: true,
  buildCache: true,
  containers: true,
  volumes: false,
  networks: false,
}

const ROW_TEXT: Record<RowId, { title: string; what: string }> = {
  images: { title: 'Unused images', what: 'Not used by any container, running or stopped.' },
  buildCache: { title: 'Build cache', what: 'Layers left over from image builds. Rebuilt when needed.' },
  containers: { title: 'Stopped containers outside any stack', what: 'Leftovers from one-off docker run commands.' },
  volumes: { title: 'Unattached volumes', what: 'Not mounted by any container. May hold data you still want.' },
  networks: { title: 'Unused networks', what: 'No container is attached. Networks use no disk.' },
}

const VOLUME_WARNING =
  'Volumes can hold databases or uploads. Removal cannot be undone, so this is off by default. Back up first if unsure.'

const AUTO_VOLUME_WARNING =
  'Volumes can hold databases or uploads. Automatic removal cannot be undone. Stack volumes are never removed.'

/** Rows shown per list; the request still carries every ID up to the server's cap. */
const LIST_CAP = 200
/** The server refuses more than this many IDs in one category. */
const REQUEST_CAP = 1000

/** Which policy switches a ticked row turns on (spec "UI consequences"). */
const ROW_TO_CATEGORIES: Record<RowId, readonly CategoryKey[]> = {
  images: ['danglingImages', 'unusedImages'],
  buildCache: ['buildCache'],
  containers: ['containers'],
  volumes: ['volumes'],
  networks: ['networks'],
}

const CATEGORY_ORDER: readonly CategoryKey[] = [
  'danglingImages',
  'unusedImages',
  'buildCache',
  'containers',
  'volumes',
  'networks',
]

const CATEGORY_NAMES: Record<CategoryKey, string> = {
  danglingImages: 'dangling images',
  unusedImages: 'unused tagged images',
  buildCache: 'build cache',
  containers: 'stopped containers',
  volumes: 'unattached volumes',
  networks: 'unused networks',
}

interface RowModel {
  id: RowId
  count: number
  /** Sum of the sizes Docker reported. */
  bytes: number
  /** Some item had no size, so `bytes` is a floor. */
  sizeUnknown: boolean
  lines: string[]
  ids: string[]
}

function itemLine(item: HostCandidateItem, showSize: boolean): string {
  const parts = [item.label]
  if (item.detail) parts.push(item.detail)
  if (showSize) parts.push(item.size === undefined ? 'size unknown' : formatBytes(item.size))
  return parts.join(' · ')
}

function itemRow(id: RowId, items: HostCandidateItem[], showSize: boolean): RowModel {
  return {
    id,
    count: items.length,
    bytes: items.reduce((n, i) => n + (i.size ?? 0), 0),
    sizeUnknown: showSize && items.some((i) => i.size === undefined),
    lines: items.map((i) => itemLine(i, showSize)),
    ids: items.map((i) => i.id),
  }
}

function buildRows(c: HostCandidates): Record<RowId, RowModel> {
  return {
    images: itemRow('images', c.images, true),
    buildCache: {
      id: 'buildCache',
      count: c.buildCache.count,
      bytes: c.buildCache.bytes,
      sizeUnknown: false,
      lines: c.buildCache.count > 0 ? [`${c.buildCache.count} cache entries · ${formatBytes(c.buildCache.bytes)}`] : [],
      ids: [],
    },
    containers: itemRow('containers', c.containers, true),
    volumes: itemRow('volumes', c.volumes, true),
    networks: itemRow('networks', c.networks, false),
  }
}

function joinList(names: string[]): string {
  if (names.length <= 1) return names.join('')
  return `${names.slice(0, -1).join(', ')} and ${names[names.length - 1]}`
}

interface AutoPlan {
  /** Every category the ticked rows map to; the PUT sets each to true. */
  wanted: CategoryKey[]
  /** The policy already runs every wanted category. */
  covered: boolean
  copy: string
  /** Volumes would be newly removed on a schedule, so the submit asks first. */
  volumesNew: boolean
}

function planAuto(ticked: RowId[], policy: DockerCleanupPolicy): AutoPlan {
  const wanted = CATEGORY_ORDER.filter((k) => ticked.some((r) => ROW_TO_CATEGORIES[r].includes(k)))
  const stored = policy.categories
  const timing = `older than ${policy.minAgeHours} h, every ${policy.intervalHours} h.`
  const volumesNew = wanted.includes('volumes') && (!policy.enabled || !stored.volumes)
  if (policy.enabled) {
    const added = wanted.filter((k) => !stored[k])
    return {
      wanted,
      covered: added.length === 0,
      copy: `Also remove ${joinList(added.map((k) => CATEGORY_NAMES[k]))} ${timing}`,
      volumesNew,
    }
  }
  // Turning the policy on also runs what is already stored as on, so the copy
  // names the whole effective set, not only what this dialog adds.
  const effective = CATEGORY_ORDER.filter((k) => stored[k] || wanted.includes(k))
  return {
    wanted,
    covered: false,
    copy: `Automatically remove ${joinList(effective.map((k) => CATEGORY_NAMES[k]))} ${timing}`,
    volumesNew,
  }
}

function buildRequest(rows: Record<RowId, RowModel>, ticked: RowId[]): HostFreeSpaceRequest {
  const req: HostFreeSpaceRequest = {}
  for (const id of ticked) {
    if (id === 'buildCache') req.buildCache = true
    else req[id] = rows[id].ids.slice(0, REQUEST_CAP)
  }
  return req
}

interface FreeUpSpaceDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
}

/**
 * Lets the user pick what to remove from the server's candidate lists and,
 * optionally, add those categories to automatic cleanup (D98-A). Nothing a
 * stack uses is ever listed: the server builds the lists and checks every ID
 * again before removing it.
 */
export function FreeUpSpaceDialog({ open, onOpenChange }: FreeUpSpaceDialogProps) {
  const candidates = useHostCandidates(open)
  const policy = useDockerCleanupPolicy()
  const freeSpace = useFreeSpace()
  const addAuto = useAddAutoCleanupCategories()

  const [ticked, setTicked] = React.useState<Record<RowId, boolean>>(DEFAULT_TICKED)
  const [auto, setAuto] = React.useState(false)
  // The volume confirmation is a step inside this dialog, not useConfirm: a
  // modal Radix dialog blocks pointer and focus outside itself, so a second
  // overlay rendered beside it could not be clicked.
  const [confirmingVolumes, setConfirmingVolumes] = React.useState(false)
  // Radix returns focus only to a Dialog.Trigger, and this dialog has several
  // openers (the page button, each card's Clean up), so it keeps its own.
  const openerRef = React.useRef<HTMLElement | null>(null)

  // Each opening starts from the defaults (adjusted during render, as
  // ConfirmDialog does, rather than in an effect).
  const [prevOpen, setPrevOpen] = React.useState(open)
  if (open !== prevOpen) {
    setPrevOpen(open)
    if (open) {
      setTicked(DEFAULT_TICKED)
      setAuto(false)
      setConfirmingVolumes(false)
    }
  }

  const rows = candidates.data ? buildRows(candidates.data) : undefined
  // An empty row cannot be ticked, so it never counts as selected.
  const selected = rows ? ROW_ORDER.filter((id) => ticked[id] && rows[id].count > 0) : []
  const total = rows ? selected.reduce((n, id) => n + rows[id].bytes, 0) : 0
  const totalUnknown = rows ? selected.some((id) => rows[id].sizeUnknown) : false
  const totalLabel = `${totalUnknown ? 'at least ' : selected.includes('images') ? 'about ' : ''}${formatBytes(total)}`

  // Hidden while the policy is loading or unreadable: its copy is built from
  // the stored policy, and made-up timing would be worse than no offer.
  const plan = policy.data && selected.length > 0 ? planAuto(selected, policy.data) : undefined
  const autoOn = auto && !!plan && !plan.covered
  const withheld = candidates.data?.withheldStacks ?? []

  async function submit(volumesConfirmed: boolean) {
    if (!rows || selected.length === 0) return
    if (autoOn && plan.volumesNew && !volumesConfirmed) {
      setConfirmingVolumes(true)
      return
    }
    setConfirmingVolumes(false)
    try {
      await freeSpace.mutateAsync(buildRequest(rows, selected))
    } catch {
      // useActionMutation has already shown why; the policy is left as it was.
      return
    }
    if (autoOn) {
      const categories = Object.fromEntries(plan.wanted.map((k) => [k, true])) as Partial<DockerCleanupCategories>
      addAuto.mutate(categories, {
        onSuccess: () => toast.success('Automatic cleanup is on'),
        onError: (err) => presentError(err, { fallback: 'Could not turn on automatic cleanup' }),
      })
    }
    onOpenChange(false)
  }

  let body
  if (candidates.isPending) {
    body = <p className="text-sm text-muted-foreground">Loading...</p>
  } else if (!rows) {
    body = <LoadFailedNotice what="what can be removed" onRetry={() => void candidates.refetch()} />
  } else {
    body = (
      <div className="space-y-3">
        {withheld.length > 0 && (
          <p data-testid="free-space-withheld" className="rounded-md border border-warning/40 bg-warning/10 p-3 text-sm">
            Capstan could not read the compose file of {joinList(withheld)}. Until it can, only build cache is
            listed: those stacks could use any image, container, volume or network.
          </p>
        )}
        {ROW_ORDER.map((id) => (
          <CategoryRow
            key={id}
            row={rows[id]}
            checked={ticked[id] && rows[id].count > 0}
            onChange={(on) => setTicked((t) => ({ ...t, [id]: on }))}
          />
        ))}
        {plan && (plan.covered ? (
          <p data-testid="free-space-auto-covered" className="text-sm text-muted-foreground">
            Automatic cleanup already covers these.{' '}
            <Link to="/host#automatic-cleanup" onClick={() => onOpenChange(false)}>
              Change timing
            </Link>
          </p>
        ) : (
          <div className="space-y-1 rounded-md border p-3">
            <label className="flex items-start gap-2 text-sm font-medium">
              <input
                type="checkbox"
                className="mt-0.5 h-4 w-4 accent-[var(--primary)] focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
                checked={auto}
                onChange={(e) => setAuto(e.target.checked)}
              />
              Do this automatically
            </label>
            <p data-testid="free-space-auto-copy" className="pl-6 text-sm text-muted-foreground">
              {plan.copy}
            </p>
            {auto && plan.wanted.includes('volumes') && (
              <p data-testid="free-space-auto-volume-warning" className="pl-6 text-sm text-warning">
                {AUTO_VOLUME_WARNING}
              </p>
            )}
          </div>
        ))}
      </div>
    )
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        className="max-h-[90vh] overflow-y-auto sm:max-w-2xl"
        onOpenAutoFocus={() => {
          openerRef.current = document.activeElement instanceof HTMLElement ? document.activeElement : null
        }}
        onCloseAutoFocus={(e) => {
          const opener = openerRef.current
          openerRef.current = null
          if (opener?.isConnected) {
            e.preventDefault()
            opener.focus()
          }
        }}
      >
        <DialogHeader>
          <DialogTitle>Free up space</DialogTitle>
          <DialogDescription>Pick what to remove. Nothing a stack uses is ever listed.</DialogDescription>
        </DialogHeader>
        {body}
        {confirmingVolumes && autoOn && policy.data ? (
          <div
            role="alert"
            data-testid="free-space-volume-confirm"
            className="space-y-3 rounded-md border border-destructive/50 p-3 text-sm"
          >
            <p>
              Automatic cleanup will remove unattached volumes, every {policy.data.intervalHours} h, once they are
              older than {policy.data.minAgeHours} h. {AUTO_VOLUME_WARNING}
            </p>
            <div className="flex justify-end gap-2">
              {/* Going back cancels the whole submit: no removal and no policy change. */}
              <Button variant="outline" onClick={() => setConfirmingVolumes(false)}>
                Go back
              </Button>
              <Button variant="destructive" onClick={() => void submit(true)}>
                Remove and turn on
              </Button>
            </div>
          </div>
        ) : (
          <DialogFooter className="items-center gap-2 sm:justify-between">
            <p data-testid="free-space-selected-total" className="text-sm font-medium">
              Selected: {totalLabel} to reclaim
            </p>
            <div className="flex gap-2">
              <Button variant="outline" onClick={() => onOpenChange(false)}>
                Cancel
              </Button>
              <Button
                variant="destructive"
                disabled={selected.length === 0 || freeSpace.isPending}
                onClick={() => void submit(false)}
              >
                {freeSpace.isPending && <Loader2 className="h-4 w-4 animate-spin" aria-hidden="true" />}
                {freeSpace.isPending ? 'Removing...' : 'Remove selected'}
              </Button>
            </div>
          </DialogFooter>
        )}
      </DialogContent>
    </Dialog>
  )
}

function CategoryRow({ row, checked, onChange }: { row: RowModel; checked: boolean; onChange: (on: boolean) => void }) {
  const { title, what } = ROW_TEXT[row.id]
  const empty = row.count === 0
  const size = row.id === 'networks' ? '' : `${row.sizeUnknown ? 'at least ' : ''}${formatBytes(row.bytes)}`
  return (
    <div data-testid={`free-space-row-${row.id}`} className="space-y-1 rounded-md border p-3">
      <label className="flex items-start gap-2">
        <input
          type="checkbox"
          className="mt-1 h-4 w-4 accent-[var(--primary)] focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
          checked={checked}
          disabled={empty}
          onChange={(e) => onChange(e.target.checked)}
        />
        <span className="flex-1">
          <span className="block text-sm font-medium">{title}</span>
          <span className="block text-sm text-muted-foreground">{empty ? 'Nothing to remove.' : what}</span>
        </span>
        {!empty && size && <span className="font-mono text-sm tabular-nums">{size}</span>}
      </label>
      {row.id === 'volumes' && <p className="pl-6 text-sm text-warning">{VOLUME_WARNING}</p>}
      {row.ids.length > REQUEST_CAP && (
        <p className="pl-6 text-sm text-muted-foreground">
          Removes the first {REQUEST_CAP} of {row.count} now. Run it again for the rest.
        </p>
      )}
      {!empty && (
        <details className="pl-6 text-sm">
          <summary className="cursor-pointer text-muted-foreground">Show the {row.count} items</summary>
          <ul className="mt-1 space-y-0.5 font-mono text-xs">
            {row.lines.slice(0, LIST_CAP).map((line, i) => (
              <li key={i}>{line}</li>
            ))}
            {row.lines.length > LIST_CAP && <li>and {row.lines.length - LIST_CAP} more</li>}
          </ul>
        </details>
      )}
    </div>
  )
}
