/**
 * Four backup endpoints answer a repository fault with one of THREE codes, all
 * minted in backend/internal/handlers/backup.go:
 *
 *   BACKUP_REPO_UNREACHABLE   503, repoFault()         — configured, not readable
 *   BACKUP_REPO_UNINITIALIZED 409, repoUninitialized() — no repository exists yet
 *   BACKUP_UNAVAILABLE        409, engineUnavailable() — a backup binary is absent,
 *                                                        which one is in details.cause
 *
 * The first two carry `details.repoState`, which of the states holds, plus
 * `message`, the cause sentence CheckRepository already computed. The third
 * carries `details.cause` and NO repoState, deliberately: on that path
 * CheckRepository is never called, so no repository state was ever obtained and
 * claiming one would be a fabrication. The axios interceptor in lib/api.ts
 * rejects with the body spread verbatim, so all of it arrives here untouched.
 *
 * Keys on `code` and `repoState`, never on the status: the code is what the
 * backend treats as the client's branch point, and a status check here would go
 * stale the moment the same fault were reported under a different status — which
 * is exactly what happened to the uninitialised state, which used to arrive as a
 * 500 and now arrives as a 409.
 *
 * Deliberately NOT routed through classifyError(). Its 5xx branch discards
 * `message` and answers "503: Something went wrong on the server" — a status
 * code, where an operator needs a cause and a recovery. The recovery is the
 * whole point, and the recoveries here are mutually exclusive: an unreachable
 * repository is fixed by checking the remote or the mount, an uninitialised one
 * by initialising, a password fault by correcting a credential — and offering
 * any of the others for an unreachable repository is destructive-adjacent.
 *
 * WHY THIS LIVES IN lib/ RATHER THAN IN ITS FIRST CONSUMER (agent-os-nhiv):
 * it started module-private inside components/stack/BackupsTab.tsx, when
 * listSnapshots was the only caller whose errors reached it. It now serves the
 * snapshot preview panel in that same component AND both toasts in
 * settings/backup-settings/useBackupActions.ts — the repository Initialize one,
 * and the cloud connectivity test that agent-os-3wyv added. A settings hook must
 * not import from a stack component, and two copies of this mapping would
 * drift, so there is one definition and the consumers differ only in how much
 * of the returned object they render.
 *
 * `hint` is the one field NOT site-neutral: it is written for a full-width
 * panel with room for a recovery. `title` and `detail` are — a cause sentence
 * and the server's own message — so a caller with no room for a paragraph, such
 * as a toast, consumes those two and leaves `hint` alone rather than
 * duplicating the mapping to reword it.
 */
export interface RepoFault {
  title: string
  hint: string
  detail: string
}

export function repoFaultFrom(error: unknown): RepoFault | null {
  if (!error || typeof error !== 'object') return null
  // `cause` is declared because the backend ALWAYS sends it on
  // BACKUP_UNAVAILABLE (every one of its six sites goes through
  // engineUnavailable) AND because this function now branches on it. It did
  // not, and the comment that used to sit here said it never would: it claimed
  // every endpoint reaching this function guarded on `!av.ResticPresent` alone,
  // so `cause` was `restic_missing` on every path, and that `rclone_missing`
  // "comes only from cloudTest, whose errors do not reach this function".
  //
  // Both halves were wrong, and the second more broadly than agent-os-3wyv
  // first supposed. MEASURED on 2528c1c — engineUnavailable's six call sites in
  // backup.go are :997 listSnapshots, :1051 previewSnapshot, :1276 runDRRestore,
  // :1401 repoInit, :1458 cloudTest and :1740 requireAvailable. TWO of them
  // guard on `!av.RclonePresent`, cloudTest AND runDRRestore, and the code's own
  // comment at both calls them "the two rclone sites"; requireAvailable guards
  // on the generic `!av.Available` and can carry either cause. So
  // `rclone_missing` was never cloudTest's alone, and cloudTest's errors now
  // reach here — the re-check condition the old comment wrote for itself has
  // fired, and this is the `cause` arm it asked for.
  //
  // The arm is POSITIVE per value with a fallback that names NEITHER binary,
  // the same way round as the repoState switch below and for the same reason:
  // requireAvailable can reach here having computed no cause at all, and a
  // default that kept the restic copy would fabricate one. `details.cause` is a
  // plain string, so tsc cannot pin this — only the unit tests in
  // lib/__tests__/backup-repo-fault.test.ts can.
  const body = error as {
    code?: string
    message?: string
    details?: { repoState?: string; cause?: string }
  }

  const detail = body.message ?? ''

  // A backup binary is missing, so nothing about the repository is known and
  // nothing about it is claimed. Before agent-os-9f5c this path answered 200
  // with an empty array and rendered as the ordinary empty state — including
  // "Run a backup to create the first one", which cannot work when the binary
  // that would run it is absent.
  if (body.code === 'BACKUP_UNAVAILABLE') {
    // rclone is the cloud leg, not the engine that reads the repository, so
    // this arm claims nothing about snapshots: at the two rclone-guarded sites
    // restic may be perfectly present and the repository perfectly readable.
    if (body.details?.cause === 'rclone_missing') {
      return {
        title: 'The cloud sync engine is not available.',
        hint: 'Capstan could not find the rclone binary, so it could not reach the cloud remote. This is a deployment problem rather than a settings one.',
        detail,
      }
    }

    if (body.details?.cause === 'restic_missing') {
      return {
        title: 'The backup engine is not available.',
        hint: 'Capstan could not find the restic binary, so it could not read the repository or tell you whether any snapshots exist. This is a deployment problem rather than a settings one.',
        detail,
      }
    }

    // No cause, so no binary is named. requireAvailable guards on the generic
    // `!av.Available`, and `detail` still carries the server's own sentence.
    return {
      title: 'The backup engine is not available.',
      hint: 'Capstan could not determine which backup component is missing. This is a deployment problem rather than a settings one.',
      detail,
    }
  }

  // A different CODE, not merely a different repoState: this function returned
  // null for anything but BACKUP_REPO_UNREACHABLE, so a new code needs its own
  // arm regardless. Initialising is the correct recovery here and ONLY here.
  if (body.code === 'BACKUP_REPO_UNINITIALIZED') {
    return {
      title: 'The backup repository does not exist yet.',
      hint: 'Nothing has been lost. Initialise the repository in Backup settings, then run a backup.',
      detail,
    }
  }

  if (body.code !== 'BACKUP_REPO_UNREACHABLE') return null

  switch (body.details?.repoState) {
    case 'password_missing':
      return {
        title: 'No restic password is configured.',
        hint: 'The repository was never contacted, so nothing is known to be wrong with it. Set the restic password in Backup settings — the remote and the mount are not the problem.',
        detail,
      }
    case 'wrong_password':
      return {
        title: 'The restic password was rejected.',
        hint: 'The repository answered and your snapshots are not missing; the credential is what is wrong. Correct the restic password in Backup settings — do not initialise a new repository over this one.',
        detail,
      }
    case 'unreachable':
      return {
        title: 'The backup repository could not be reached.',
        hint: 'Your snapshots are not missing. The repository did not answer, so check the remote or the mount — initialising a new repository would not bring them back.',
        detail,
      }
    case 'settings_unreadable':
      return {
        title: 'Backup repository state is unknown.',
        hint: 'Capstan could not load the backup settings, so it cannot tell which repository this stack uses. Check the Backup settings.',
        detail,
      }
    default:
      // Every specific state is a POSITIVE arm and this is the fallback,
      // deliberately that way round. Prescribing a recovery by default means a
      // repoState added backend-side would be answered with advice written for a
      // different fault, silently and with no failing test — tsc cannot help
      // here, because details.repoState is a plain string and this switch has a
      // default, so the compiler sees no missing case however many states the
      // backend mints. Say only what the code itself carries.
      return {
        title: 'The backup repository could not be read.',
        hint: 'Capstan could not determine the cause. Check the Backup settings and the repository.',
        detail,
      }
  }
}
