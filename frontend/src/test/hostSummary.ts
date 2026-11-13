import type { HostSummary } from '@/types/generated-services'

export const GiB = 1024 ** 3

type Overrides = { [K in keyof HostSummary]?: Partial<HostSummary[K]> }

/**
 * A HostSummary for an empty, fully measured host, with any part overridden.
 * Nothing is reclaimable unless a test says so.
 */
export function makeHostSummary(o: Overrides = {}): HostSummary {
  return {
    disk: { total: 0, inUse: 0, reclaimable: 0, reclaimableAll: 0, volumesSizeKnown: true, containersSizeKnown: true, ...o.disk },
    containers: { count: 0, running: 0, stopped: 0, reclaimableCount: 0, reclaimableBytes: 0, reclaimableBytesKnown: true, ...o.containers },
    images: { count: 0, inUse: 0, inUseKnown: true, bytes: 0, reclaimableCount: 0, reclaimableBytes: 0, ...o.images },
    volumes: { count: 0, attached: 0, attachedKnown: true, bytes: 0, sizeKnown: true, reclaimableCount: 0, reclaimableBytes: 0, ...o.volumes },
    networks: { count: 0, builtIn: 0, stackOwned: 0, reclaimableCount: 0, ...o.networks },
    buildCache: { count: 0, bytes: 0, reclaimableBytes: 0, ...o.buildCache },
    measuredAt: '2026-11-13T10:00:00Z',
  }
}
