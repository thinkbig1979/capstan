import type { ContainerUpdateInfo, CachedUpdate } from '@/types'

export type SortKey = 'name' | 'image' | 'state' | 'stack'

export type UpdateItem = ContainerUpdateInfo | CachedUpdate

export function isCachedUpdate(item: UpdateItem): item is CachedUpdate {
  return 'localDigest' in item && 'remoteDigest' in item
}

export const UPDATE_SEARCH_FIELDS = [
  (u: UpdateItem) => u.containerName,
  (u: UpdateItem) => u.imageRef,
  (u: UpdateItem) => u.projectName,
]
