/**
 * The global auto-update switch as a consumer can actually know it. A boolean
 * cannot carry it: `undefined` from a query that is still in flight, and
 * `undefined` from one that FAILED, are not the same thing as a switch the
 * operator deliberately turned off (agent-os-bueb). All three lock the toggle,
 * which is the safe failure, but only one of them is caused by a setting.
 */
export type GlobalAutoUpdateState = 'enabled' | 'disabled' | 'loading' | 'unavailable'

/**
 * Maps the auto-update policies query onto that state. Every call site derives
 * its value through here so the three cases stay distinguished at the SOURCE,
 * where the query result is the only thing that can tell them apart.
 *
 * It lives beside AutoUpdateToggle rather than inside it because test files
 * mock that component: an export shared with the component would vanish from
 * every consumer the moment one of them stubbed the toggle.
 */
export function toGlobalAutoUpdateState(query: {
  data?: { globalEnabled: boolean }
  isPending: boolean
  isError: boolean
}): GlobalAutoUpdateState {
  if (query.isError) return 'unavailable'
  // `!query.data` is not redundant with isPending: a settled query with no data
  // would otherwise read `globalEnabled` off nothing and report a deliberate
  // "off" that nobody set.
  if (query.isPending || !query.data) return 'loading'
  return query.data.globalEnabled ? 'enabled' : 'disabled'
}
