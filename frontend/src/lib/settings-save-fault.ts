import { messageOrNull } from './narrow'

/**
 * The settings-save cause reader (agent-os-zlw0): the server's own sentence
 * when the save was refused for a reason the operator can act on, else null.
 *
 * Four settings forms share it — the update schedule, backup settings, history
 * retention and git settings — because all four mint the SAME two codes and all
 * four consume the result the same way: one line of description under an
 * unchanged generic title. It lives in lib/ rather than beside its first caller
 * for the reason repoFaultFrom does (agent-os-nhiv): its consumers sit in two
 * directories and two copies of an allow-list drift.
 *
 * THE ALLOW-LIST, and why it is an allow-list rather than a deny-list:
 *
 *   VALIDATION_ERROR        models.ErrValidation. What every one of the four
 *                           handlers answers when it rejects the INPUT, and the
 *                           only thing that says WHICH field was wrong out of
 *                           the five UpdateUpdateSettings distinguishes
 *                           (settings.go:647), the three UpdateLogRetention
 *                           does (settings.go:456), the two UpdateGitSettings
 *                           does (settings.go:914) and the several the backup
 *                           endpoint does (backup.go:402).
 *   ENCRYPTION_KEY_MISSING  models.ErrEncryptionUnavailable, a 422 minted by
 *                           respondIfEncryptionUnavailable (respond.go:229) and
 *                           reached from the git token write (settings.go:968)
 *                           and the backup password write (backup.go:475). It
 *                           carries a recovery, not just a cause: "Set
 *                           STORAGE_KEY (or JWT_SECRET) ... and restart Capstan".
 *
 * KEY ON THE WIRE VALUE, not the Go identifier: the constants are
 * models.ErrValidation and models.ErrEncryptionUnavailable, whose VALUES are
 * "VALIDATION_ERROR" and "ENCRYPTION_KEY_MISSING" (models/errors.go).
 *
 * KEYED ON THE CODE, NEVER ON "did something carry a message". When there is no
 * HTTP response at all the axios interceptor rejects with
 * `{ error: 'Unknown error', code: error.code || 'UNKNOWN', message: error.message }`
 * (api.ts:131), and that `message` is a non-empty string — literally "Network
 * Error" or "timeout of 30000ms exceeded". A reader shaped `err?.message ?? null`
 * would render an axios internal string as though the backend had said it, and
 * would pass tsc and vitest green while doing so. Every code outside the two
 * above, middleware codes included, falls through to null and leaves the caller
 * rendering exactly the sentence it rendered before this function existed.
 *
 * CODE-KEYING BUYS TWO DIFFERENT THINGS, and it is worth naming both because
 * only the first is obvious. The first is SAFETY, above: it is what stops
 * "Network Error" reaching an operator dressed as a backend sentence. The
 * second is COVERAGE, and it cuts the other way — code-keying PICKS UP faults
 * a status-keyed reader would silently drop. The backup endpoint mints
 * VALIDATION_ERROR at 422, not 400, for both repository refusals: the one that
 * refuses a value still carrying the *** redaction marker (backup.go:449) and
 * the one that refuses an inline credential restic would misread
 * (backup.go:462). A reader keyed on status 400 returns null for both, so the
 * two most operator-actionable sentences on that route would never render —
 * not a safety failure but a plain hole in coverage, and an invisible one,
 * since a null cause is indistinguishable from "the server said nothing".
 * Keying on the code is therefore both the conservative choice and the more
 * complete one; the usual trade between the two does not arise here.
 *
 * NOT credentialSaveFault (components/git/GitSettingsSection.tsx). That one
 * deliberately EXCLUDES VALIDATION_ERROR as saying nothing an operator can act
 * on for a credentials paste, and a test pins the exclusion. Here
 * VALIDATION_ERROR is the whole point, so this is a second function rather than
 * a widening of that one.
 *
 * NOT classifyError(). It is status-keyed, so it offers no code-keyed predicate
 * — and the code is precisely what separates a real backend refusal from the
 * network case above.
 */
export function settingsSaveFault(error: unknown): string | null {
  if (!error || typeof error !== 'object') return null
  const body = error as { code?: string; message?: unknown }
  if (body.code !== 'VALIDATION_ERROR' && body.code !== 'ENCRYPTION_KEY_MISSING') return null
  // Narrowed, not asserted (agent-os-06c1), and `|| null` preserved by
  // messageOrNull: an empty server sentence is not a usable description.
  return messageOrNull(body.message)
}
