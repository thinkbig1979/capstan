/**
 * Runtime narrowing for values that arrive through an `as` assertion on an
 * `unknown` error or an unvalidated response body.
 *
 * WHY THIS EXISTS (agent-os-06c1). The house idiom at these boundaries is
 *
 *     const body = error as { code?: string; message?: string }
 *     return body.message || null
 *
 * `error` is `unknown`, so the assertion claims a shape nobody validated, and
 * an `as` silences exactly the check that would catch it being wrong. A
 * non-string `message` escapes into a `string`-typed return with no compiler
 * error, no lint error and no failing test: the declared return type is a
 * claim tsc is not checking.
 *
 * NOT reachable today, and must not be described as a live bug.
 * models.AppError.Message is a Go `string`, so every producer on these routes
 * sends a string. These helpers make the declared type one tsc actually
 * enforces, so the claim stops resting on a property of the current backend.
 */

/**
 * A non-empty string, or null.
 *
 * The `!== ''` clause is deliberate: it preserves the `|| null` the fault
 * readers used before this helper existed. An empty server sentence is not a
 * usable one, and every caller falls back to its own generic copy on null.
 * See agent-os-06c1 criterion 3 — `||` is kept, not silently promoted to `??`.
 */
export function messageOrNull(value: unknown): string | null {
  return typeof value === 'string' && value !== '' ? value : null
}

/**
 * The value when it really is a string, else `fallback`.
 *
 * Unlike messageOrNull this keeps the empty string, because its callers used
 * `?? ''` or `?? 'unknown'` — they distinguish absent from empty and only the
 * absent case takes the fallback.
 */
export function stringOr(value: unknown, fallback: string): string {
  return typeof value === 'string' ? value : fallback
}

/**
 * The value when it is an array of strings, else `fallback`.
 *
 * All-or-nothing rather than per-member filtering: these lists are enumerated
 * to a user as "here is everything that will be affected", so silently
 * dropping the members that failed the check would under-report the blast
 * radius — the one outcome worse than showing the generic fallback.
 */
export function stringArrayOr(value: unknown, fallback: string[]): string[] {
  return Array.isArray(value) && value.every((v) => typeof v === 'string') ? value : fallback
}
