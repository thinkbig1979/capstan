/**
 * A whole number typed into a field, or null for anything else: empty, "-5",
 * "1.5", "1e3", " ". An empty field is "no value yet", never 0 (agent-os-z91e.15):
 * 0 means something on every screen that uses this ("disabled", "keep none"), so
 * mapping empty to it saved a setting the operator never chose.
 */
export function parseWholeNumber(raw: string): number | null {
  return /^\d+$/.test(raw) ? Number(raw) : null
}
