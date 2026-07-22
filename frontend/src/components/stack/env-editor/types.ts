import type { EnvEntry } from '@/types'

/** Entries tagged with their original index so filter never shifts edit targets. */
export interface IndexedEnvEntry extends EnvEntry {
  _originalIndex: number
}
