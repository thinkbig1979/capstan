import { toast } from 'sonner'
import { toastInvalid } from '@/lib/error-handler'
import { useInitRepo, useTestCloud } from '@/hooks/useBackup'
import { repoFaultFrom } from '@/lib/backup-repo-fault'
import { messageOrNull } from '@/lib/narrow'

/**
 * The two standalone backup engine actions: initializing the restic
 * repository and testing rclone connectivity. Both toast an error when the
 * request itself fails, and testCloud separately when the request succeeds but
 * the response reports the operation didn't actually succeed.
 */
/**
 * cloudTest's 400. Keys on the exact code the backend mints
 * (models.ErrValidation is the string "VALIDATION_ERROR", not "VALIDATION") so
 * that a plain Error, or an axios transport failure whose `message` is axios's
 * own text rather than the server's, cannot be mistaken for a server sentence.
 */
function validationMessage(error: unknown): string | null {
  if (!error || typeof error !== 'object') return null
  const body = error as { code?: string; message?: unknown }
  if (body.code !== 'VALIDATION_ERROR') return null
  // agent-os-06c1: narrowed, not asserted. This becomes the toast TITLE.
  return messageOrNull(body.message)
}

export function useBackupActions() {
  const initRepo = useInitRepo()
  const testCloud = useTestCloud()

  const handleInitRepo = () => {
    initRepo.mutate(undefined, {
      // States the resulting state rather than claiming work happened
      // (agent-os-jmom). repoInit returns 200 {"initialized": true} from BOTH
      // of its success paths, and the FIRST fires on RepoStateOK having created
      // nothing at all — so "Repository initialized successfully" was false for
      // every operator who pressed this on an already-healthy repository, with
      // no way for them to tell which of the two they got.
      //
      // There is no not-initialized arm here any more, and its absence is the
      // point. The `data.initialized === false` branch that used to sit here
      // guarded a shape the server does not emit, and a test pinned it, so the
      // pair read as covered behaviour. Do not reinstate it under another key:
      // "success fired, but repoState still isn't ok" is the same dead branch
      // re-keyed, and worse, because onSuccess only invalidates — the repoState
      // it would read is the PRE-init value, so it would report failure on a
      // successful init with tsc and vitest both green.
      onSuccess: () => toast.success('Backup repository is ready'),

      // Takes the error. It used to take nothing, which made the cause
      // unreadable in principle rather than merely unread: there was no unused
      // variable and no type error to notice (agent-os-nhiv). The endpoint mints
      // BACKUP_UNAVAILABLE and BACKUP_REPO_UNREACHABLE, and agent-os-81vr made
      // the second one matter — it stopped the server creating a repository over
      // an unreachable one, but left the operator reading "Failed to initialize
      // repository", learning nothing, and retrying. That retry loop is what
      // 81vr set out to break.
      //
      // Renders `title` and `detail` and NOT `hint`: the hints are written for
      // the full-width panel on the stack Backups tab and carry its framing
      // ("...or tell you whether any snapshots exist"), which is wrong copy for
      // a toast fired from Settings. Both fields used here are site-neutral, so
      // this reads the one shared mapping instead of keeping a reworded second
      // copy that would drift from it.
      onError: (error) => {
        const fault = repoFaultFrom(error)
        if (!fault) {
          // The code-keyed reader declined this rejection, so nothing is
          // claimed about the cause. toastInvalid, not presentError.
          toastInvalid('Failed to initialize repository')
          return
        }
        toast.error(fault.title, fault.detail ? { description: fault.detail } : undefined)
      },
    })
  }

  const handleTestCloud = () => {
    testCloud.mutate(undefined, {
      // A FAILED connectivity test arrives as 200, not as an error status, so
      // it never reaches onError — cloudTest answers `{ok: false, error}` and
      // the failure lands here (agent-os-3wyv). The cause was on the wire the
      // whole time; what threw it away was the response TYPE, which declared
      // `{ ok: boolean }` and omitted `error` entirely, so there was no unused
      // variable and no compiler error to notice.
      //
      // There is no "failed, cause unknown" arm and its absence is the point.
      // `error` is a required string on the false arm of CloudTestResult
      // because the server's single ok:false emitter always sends it, so a
      // fallback here would be a dead branch — the same one handleInitRepo's
      // comment above records this wave as having just deleted. Do not add it.
      onSuccess: (data) => {
        if (data.ok) {
          toast.success('Cloud connectivity test passed')
          return
        }
        toast.error(data.error)
      },

      // Takes the error. It used to take nothing, so the cause was unreadable
      // in principle rather than merely unread (agent-os-3wyv, same shape as
      // handleInitRepo's site above). This endpoint mints TWO discriminating
      // codes and they need two different readings:
      //
      // BACKUP_UNAVAILABLE goes through the shared mapping — but ONLY because
      // that mapping now branches on `details.cause`. cloudTest guards on
      // `!av.RclonePresent`, so the cause here is `rclone_missing`, and before
      // the cause arm existed repoFaultFrom answered every BACKUP_UNAVAILABLE
      // with restic copy. Routing this site through it unchanged would have
      // told an operator whose rclone is absent that their restic is missing: a
      // fabricated cause, green under both tsc and vitest, and precisely the
      // defect this line of work exists to stop.
      //
      // VALIDATION_ERROR is read HERE rather than in repoFaultFrom, and that is
      // deliberate. It is not a repository fault, and previewSnapshot mints the
      // same code, so an arm in the shared helper would change that consumer's
      // rendering as a side effect of fixing this one. The server's own message
      // ("rclone remote is not configured") is already the whole answer.
      onError: (error) => {
        const fault = repoFaultFrom(error)
        if (fault) {
          toast.error(fault.title, fault.detail ? { description: fault.detail } : undefined)
          return
        }

        const invalid = validationMessage(error)
        if (invalid) {
          toast.error(invalid)
          return
        }

        // Nothing discriminating was carried, so nothing is claimed. This arm
        // has TWO real producers, and they are reachable for different reasons.
        // On a transport failure the axios interceptor's no-response branch
        // builds a body whose `code` is AXIOS's (`error.code || 'UNKNOWN'`)
        // rather than a server one. A 500, by contrast, DOES carry a server
        // code: cloudTest's two `h.internalError` calls mint INTERNAL_ERROR
        // (backup.go:1757-1767). It is simply not a DISCRIMINATING one — it is
        // none of repoFaultFrom's three codes and it is not VALIDATION_ERROR,
        // so both readers above decline it and fall through to here.
        //
        // Keying the VALIDATION_ERROR read on the CODE rather than on "does it
        // have a message" is what keeps this arm reachable at all — that
        // interceptor branch does set `message`, to axios's own text.
        toastInvalid('Cloud connectivity test failed')
      },
    })
  }

  return {
    handleInitRepo,
    isInitializing: initRepo.isPending,
    handleTestCloud,
    isTestingCloud: testCloud.isPending,
  }
}
