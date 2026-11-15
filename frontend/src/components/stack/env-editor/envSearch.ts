/**
 * The text an env-row search list matches for one field: nothing while the
 * field is hidden. Every env-row search accessor goes through this, so a match
 * cannot tell a locked user what a masked value or a hidden key contains, one
 * substring at a time (agent-os-kxag, sopw, y6e8, f3am; guarded by
 * src/lib/__tests__/envLockGuards.test.ts, agent-os-uzgo).
 */
export function searchable(text: string, hidden: boolean): string {
  return hidden ? '' : text
}
