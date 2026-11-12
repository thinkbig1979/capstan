import { useState } from 'react'

interface UseExternalFileChangeOptions<T> {
  /** The editor holds edits that are not on disk. */
  isDirty: boolean
  /** Replace the editor's state with `next`, as the new saved baseline. */
  hydrate: (next: T) => void
  /**
   * True when a dirty editor can take `next` and lose nothing: it already
   * shows that content, or the change has to take the plain hydrate path.
   */
  canHydrateWhileDirty?: (next: T, prev: T | undefined) => boolean
  /** True when `next` is the editor's own last save coming back. */
  isOwnSave?: (next: T) => boolean
  /**
   * Called once when `isOwnSave` matched, so the caller can drop what it
   * remembered about the save and move its baseline. It may set state: it runs
   * during render, like `hydrate`.
   */
  onOwnSave?: (next: T) => void
  /**
   * Called first for a dirty editor. True when the caller kept the edits and
   * moved its own baseline to `next`, so there is nothing to hydrate and no
   * notice to raise (a lock change, agent-os-vqf5). It may set state: it runs
   * during render, like `hydrate`.
   */
  rebaseWhileDirty?: (next: T, prev: T | undefined) => boolean
}

/**
 * Loads new query data into a file editor without throwing away unsaved
 * edits (agent-os-umig). A clean editor reloads silently. A dirty one keeps
 * its edits and holds the newest data until the user reloads, or until the
 * refetch after a save brings the saved text back to a clean editor: a git
 * pull, a window-focus refetch or another tab's save would otherwise replace
 * what the user typed, and the next save would write over the change on disk.
 *
 * Adjusted during render, not in an effect, by comparing against the data
 * from the previous render (https://react.dev/learn/you-might-not-need-an-effect).
 * `prevData` starts undefined so data already in the cache at mount hydrates.
 */
export function useExternalFileChange<T>(
  data: T | undefined,
  { isDirty, hydrate, canHydrateWhileDirty, isOwnSave, onOwnSave, rebaseWhileDirty }: UseExternalFileChangeOptions<T>,
) {
  const [prevData, setPrevData] = useState<T | undefined>(undefined)
  const [pending, setPending] = useState<{ value: T } | null>(null)

  if (data !== prevData) {
    setPrevData(data)
    if (data !== undefined) {
      if (!isDirty) {
        setPending(null)
        hydrate(data)
      } else if (rebaseWhileDirty?.(data, prevData)) {
        // Newer data supersedes a held one: a pending value from before a lock
        // would load plaintext the lock just dropped.
        setPending(null)
      } else if (canHydrateWhileDirty?.(data, prevData)) {
        setPending(null)
        hydrate(data)
      } else if (isOwnSave?.(data)) {
        setPending(null)
        onOwnSave?.(data)
      } else {
        // A second change while the notice is up replaces the first, so
        // Reload always loads the newest version.
        setPending({ value: data })
      }
    }
  }

  return {
    changedOnDisk: pending !== null,
    /** Discard the edits and load the version on disk. */
    reload: () => {
      if (!pending) return
      hydrate(pending.value)
      setPending(null)
    },
  }
}
