/**
 * Field readers for WebSocket frame validators (agent-os-r4kf).
 *
 * useWebSocketJSON used to hand each consumer `JSON.parse(data) as T`, which
 * made T a claim nothing checked. Every caller now supplies a validator of
 * type `(raw: unknown) => T | null`, built with frameValidator from these
 * readers, and a frame the validator rejects is dropped before it reaches the
 * consumer.
 *
 * NOT reachable today, and must not be described as a live bug: every producer
 * is a Go struct serialised by encoding/json. The validators make the declared
 * frame types ones tsc enforces instead of ones resting on the backend.
 *
 * The readers decode what Go's encoding/json actually emits, not what a
 * hand-written TS type wishes it emitted. In particular a Go `string` field
 * tagged `omitempty` is ABSENT from the frame when it holds "", so
 * omitemptyString reads an absent key back as "" (the value Go held) rather
 * than rejecting the frame.
 */

class FrameFieldError extends Error {}

function reject(what: string): never {
  throw new FrameFieldError(what)
}

/**
 * Wraps a reader so a malformed frame comes back as null instead of throwing.
 * The throw is internal to the readers below and never leaves this function;
 * any other error is a bug in the reader and is rethrown.
 */
export function frameValidator<T>(read: (raw: unknown) => T): (raw: unknown) => T | null {
  return (raw) => {
    try {
      return read(raw)
    } catch (error) {
      if (error instanceof FrameFieldError) return null
      throw error
    }
  }
}

export function record(value: unknown): Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : reject('not an object')
}

export function str(value: unknown): string {
  return typeof value === 'string' ? value : reject('not a string')
}

export function num(value: unknown): number {
  return typeof value === 'number' ? value : reject('not a number')
}

/** A Go `string` tagged omitempty: absent means Go held "". */
export function omitemptyStr(value: unknown): string {
  return value === undefined ? '' : str(value)
}

/** A field the TS type marks optional: absent stays absent. */
export function optStr(value: unknown): string | undefined {
  return value === undefined ? undefined : str(value)
}

function isOneOf<T extends string>(value: string, allowed: readonly T[]): value is T {
  return (allowed as readonly string[]).includes(value)
}

/** A string that must be one of `allowed`. */
export function oneOf<T extends string>(value: unknown, allowed: readonly T[]): T {
  const s = str(value)
  return isOneOf(s, allowed) ? s : reject('not an allowed value')
}

/** oneOf for a field the TS type marks optional: absent stays absent. */
export function optOneOf<T extends string>(value: unknown, allowed: readonly T[]): T | undefined {
  return value === undefined ? undefined : oneOf(value, allowed)
}

/**
 * Every member through `read`. All-or-nothing, like narrow.ts's
 * stringArrayOr: one bad member rejects the whole frame, because a partly
 * valid list shown as if complete would under-report.
 */
export function arrayOf<T>(value: unknown, read: (raw: unknown) => T): T[] {
  return Array.isArray(value) ? value.map(read) : reject('not an array')
}
