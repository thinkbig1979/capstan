import type { GlobalEnvVar } from '@/types/generated-handlers'

/** A row as the server sends it: key, value and its flags (agent-os-ruuw). */
export type EnvVar = GlobalEnvVar

/** The fields a row's inputs edit. */
export type EditableField = 'key' | 'value'

// Indexed wrapper so the text-filter result can carry the original vars[] index,
// ensuring handleChange / handleDelete / toggleVisible always target the right entry.
export type IndexedEnvVar = EnvVar & { _originalIndex: number }

/**
 * A variable as the form holds it: `_rowId` is a client-side identity that stays
 * the same while the key and value are edited, so a row can be followed across a
 * lock change (agent-os-vqf5). Never sent to the API.
 */
export type EnvRow = EnvVar & { _rowId: number }
