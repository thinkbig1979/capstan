import { atLeastBytes, formatBytes } from '@/lib/format'
import { HOST_VIEWS, type HostView } from '@/lib/app-views'
import type { HostSummary } from '@/types/generated-services'

export type { HostView }

const HOST_VIEW_LABELS: Record<HostView, string> = {
  containers: 'Containers',
  images: 'Images',
  volumes: 'Volumes',
  networks: 'Networks',
  'build-cache': 'Build cache',
}

/** The five Docker host list views, in display order, from the shared list in app-views. */
export const HOST_VIEW_ITEMS: readonly { view: HostView; label: string }[] = HOST_VIEWS.map(
  (view) => ({ view, label: HOST_VIEW_LABELS[view] }),
)

export function isHostView(value: string | null | undefined): value is HostView {
  return HOST_VIEWS.some((v) => v === value)
}

export function hostViewLabel(view: HostView): string {
  return HOST_VIEW_LABELS[view]
}

const SIZE_UNKNOWN = 'size unknown'

function plural(n: number, one: string, many: string) {
  return `${n} ${n === 1 ? one : many}`
}

/** A count Docker could not compute for every item is a floor (agent-os-m84q). */
function atLeast(n: number, known: boolean) {
  return known ? String(n) : `at least ${n}`
}

export interface HostCardModel {
  category: HostView
  title: string
  value: string
  detail: string
  reclaim?: string
}

/**
 * The five cards' text from one summary. Volume sizes read "size unknown"
 * when Docker could not measure every volume, never a sum that looks exact
 * (AC15).
 */
export function hostCards(s: HostSummary): HostCardModel[] {
  const volumeSize = (bytes: number) => (s.volumes.sizeKnown ? formatBytes(bytes) : SIZE_UNKNOWN)
  return [
    {
      category: 'containers',
      title: 'Containers',
      value: String(s.containers.count),
      detail: `${s.containers.running} running · ${s.containers.stopped} stopped.`,
      reclaim:
        s.containers.reclaimableCount > 0
          ? `${plural(s.containers.reclaimableCount, 'stopped container', 'stopped containers')} outside any stack · ${atLeastBytes(s.containers.reclaimableBytes, s.containers.reclaimableBytesKnown)}`
          : undefined,
    },
    {
      category: 'images',
      title: 'Images',
      value: formatBytes(s.images.bytes),
      detail: `${plural(s.images.count, 'image', 'images')} · ${atLeast(s.images.inUse, s.images.inUseKnown)} used by a container.`,
      // "about": per-image Size minus SharedSize is a lower bound.
      reclaim:
        s.images.reclaimableCount > 0
          ? `${plural(s.images.reclaimableCount, 'unused image', 'unused images')} · about ${formatBytes(s.images.reclaimableBytes)}`
          : undefined,
    },
    {
      category: 'volumes',
      title: 'Volumes',
      value: volumeSize(s.volumes.bytes),
      detail: `${plural(s.volumes.count, 'volume', 'volumes')} · ${atLeast(s.volumes.attached, s.volumes.attachedKnown)} attached to a container.`,
      reclaim:
        s.volumes.reclaimableCount > 0
          ? `${plural(s.volumes.reclaimableCount, 'unattached volume', 'unattached volumes')} · ${volumeSize(s.volumes.reclaimableBytes)} (check before removing)`
          : undefined,
    },
    {
      category: 'networks',
      title: 'Networks',
      value: String(s.networks.count),
      detail: `${s.networks.stackOwned} used by stacks · ${s.networks.builtIn} built in.`,
      reclaim:
        s.networks.reclaimableCount > 0
          ? plural(s.networks.reclaimableCount, 'unused network', 'unused networks')
          : undefined,
    },
    {
      category: 'build-cache',
      title: 'Build cache',
      value: formatBytes(s.buildCache.bytes),
      detail: 'Layers kept from image builds.',
      reclaim:
        s.buildCache.reclaimableBytes > 0
          ? `Unused cache · ${formatBytes(s.buildCache.reclaimableBytes)}`
          : undefined,
    },
  ]
}
