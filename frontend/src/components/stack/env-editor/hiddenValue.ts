/** What a value input shows where the locked view blanked a secret (agent-os-0b4m). */
const HIDDEN_VALUE_PLACEHOLDER = 'Hidden, unlock to view'

/**
 * The placeholder of a value input. A secret the locked view blanked arrives
 * as '', which an empty password input shows as nothing at all, the same as a
 * value that really is empty; while locked it says it is hidden instead.
 */
export function valuePlaceholder(locked: boolean, masked: boolean, value: string): string {
  return locked && masked && value === '' ? HIDDEN_VALUE_PLACEHOLDER : 'value'
}
