import type { EnvEntry } from '@/types'

/**
 * Entries tagged with a stable synthetic row id, assigned once when a row
 * enters state (query hydration or Add Entry) and carried through every
 * edit via object spread — unlike `key`, `_rowId` never changes while the
 * row is being edited, so it's what React keys the row on (see
 * env-editor/EnvTableView.tsx). Mirrors LogViewer's ingestion-id scheme
 * (logviewer/useLogStream.ts). Never sent to the API — useEnvMutations
 * strips it before the entries array reaches stacksApi.updateEnv().
 */
export interface EnvEntryRow extends EnvEntry {
  _rowId: number
}

/** Entries tagged with their original index so filter never shifts edit targets. */
export interface IndexedEnvEntry extends EnvEntryRow {
  _originalIndex: number
}
