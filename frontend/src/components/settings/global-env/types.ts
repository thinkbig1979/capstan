import type { GlobalEnvVar } from '@/types/generated-handlers'

/** A row as the server sends it: key, value and its flags (agent-os-ruuw). */
export type EnvVar = GlobalEnvVar

/** The fields a row's inputs edit. */
export type EditableField = 'key' | 'value'

// Indexed wrapper so the text-filter result can carry the original vars[] index,
// ensuring handleChange / handleDelete / toggleVisible always target the right entry.
// `keyHidden`: the row's key is secret content and is not shown, edited or
// searched (agent-os-f3am).
// `valueLocked`: the row's key is hidden while the session is locked, so its
// value takes no typed text either (agent-os-9fsu F10).
export type IndexedEnvVar = EnvRow & { _originalIndex: number; keyHidden: boolean; valueLocked?: boolean }

/**
 * A variable as the form holds it: `_rowId` is a client-side identity that stays
 * the same while the key and value are edited, so a row can be followed across a
 * lock change (agent-os-vqf5). Never sent to the API.
 */
export type EnvRow = EnvVar & { _rowId: number }
