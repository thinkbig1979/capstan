export type EnvVar = { key: string; value: string }

// Indexed wrapper so the text-filter result can carry the original vars[] index,
// ensuring handleChange / handleDelete / toggleVisible always target the right entry.
export type IndexedEnvVar = EnvVar & { _originalIndex: number }
