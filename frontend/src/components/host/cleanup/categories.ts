import type { DockerCleanupCategories, DockerCleanupPolicy, DockerCleanupRun } from '@/types'

export type CleanupCategory = keyof DockerCleanupCategories

/** The order every list of categories is shown in, in copy and in the form. */
export const CLEANUP_CATEGORY_ORDER: readonly CleanupCategory[] = [
  'danglingImages',
  'unusedImages',
  'buildCache',
  'containers',
  'volumes',
  'networks',
]

/** How a category reads inside a sentence (spec "UI consequences"). */
const CLEANUP_CATEGORY_NAMES: Record<CleanupCategory, string> = {
  danglingImages: 'dangling images',
  unusedImages: 'unused tagged images',
  buildCache: 'build cache',
  containers: 'stopped containers',
  volumes: 'volumes',
  networks: 'networks',
}

/** "a", "a and b", "a, b and c". */
export function joinNames(names: string[]): string {
  if (names.length <= 1) return names.join('')
  return `${names.slice(0, -1).join(', ')} and ${names[names.length - 1]}`
}

export function enabledCategoryNames(categories: DockerCleanupCategories): string[] {
  return CLEANUP_CATEGORY_ORDER.filter((c) => categories[c]).map((c) => CLEANUP_CATEGORY_NAMES[c])
}

/** What the schedule does, built from the stored policy: never a hardcoded
 *  number or category, because the operator can change every one of them. */
export function policySentences(policy: DockerCleanupPolicy): { removes: string; never: string | null } {
  const on = enabledCategoryNames(policy.categories)
  const off = CLEANUP_CATEGORY_ORDER.filter((c) => !policy.categories[c]).map((c) => CLEANUP_CATEGORY_NAMES[c])
  return {
    removes: `Every ${policy.intervalHours} h removes ${joinNames(on)} older than ${policy.minAgeHours} h.`,
    never: off.length > 0 ? `Never removed automatically: ${off.join(', ')}.` : null,
  }
}

/** What one run actually freed. Image, build-cache and container/volume bytes
 *  are recorded separately, and a run that only cleared cache has
 *  bytesReclaimed 0 — reading that field alone reports "0 B" for work that did
 *  happen. */
export function runFreedBytes(run: DockerCleanupRun): number {
  return run.bytesReclaimed + run.cacheBytesReclaimed + run.otherBytesReclaimed
}

function plural(n: number, word: string): string {
  return `${n} ${word}${n === 1 ? '' : 's'}`
}

/** "3 images, 1 container", or null when the run removed no object. */
export function runRemovedSummary(run: DockerCleanupRun): string | null {
  const parts = [
    run.imagesDeleted > 0 ? plural(run.imagesDeleted, 'image') : null,
    run.containersDeleted > 0 ? plural(run.containersDeleted, 'container') : null,
    run.volumesDeleted > 0 ? plural(run.volumesDeleted, 'volume') : null,
    run.networksDeleted > 0 ? plural(run.networksDeleted, 'network') : null,
  ].filter((p): p is string => p !== null)
  return parts.length > 0 ? parts.join(', ') : null
}
