import { useCallback, useRef } from 'react'

/**
 * Monotonically increasing id generator for tagging EnvEntry rows with a
 * stable synthetic identity, distinct from their (editable) key — mirrors
 * LogViewer's ingestion-id scheme (see logviewer/useLogStream.ts's
 * `nextLogIdRef`). One counter per EnvEditor mount is enough since ids only
 * need to be unique within that instance's entries array.
 */
export function useNextRowId() {
  const nextIdRef = useRef(0)
  return useCallback(() => nextIdRef.current++, [])
}
