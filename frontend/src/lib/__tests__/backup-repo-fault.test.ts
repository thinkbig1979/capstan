import { describe, it, expect } from 'vitest'
import { repoFaultFrom } from '../backup-repo-fault'

/**
 * A DIRECT unit test rather than another component arm, because the thing under
 * test is a distinction the component cannot show: both causes render through
 * the same two fields at the same call site, so a component test can only ever
 * pin the copy one caller happens to send. The fabrication this file guards
 * against is repoFaultFrom answering a cause it was not given.
 *
 * It lives under src/lib/__tests__ and that placement is load-bearing: a vitest
 * run scoped to src/components is structurally blind to this file, so the gate
 * for this change is `vitest run src` (agent-os-3wyv, criterion 6).
 */
describe('repoFaultFrom — BACKUP_UNAVAILABLE cause arm', () => {
  it('names rclone, not restic, when the cause is rclone_missing', () => {
    // cloudTest and runDRRestore both guard on `!av.RclonePresent`, and
    // engineUnavailable mints `rclone_missing` unless restic is ALSO absent.
    // Before this arm existed, this input answered with the restic copy — a
    // fabricated cause, green under both tsc and vitest.
    const fault = repoFaultFrom({
      code: 'BACKUP_UNAVAILABLE',
      message: 'rclone binary not found in PATH',
      details: { cause: 'rclone_missing' },
    })

    expect(fault).not.toBeNull()
    expect(fault?.title).toBe('The cloud sync engine is not available.')
    expect(fault?.hint).toContain('rclone')
    expect(fault?.hint).not.toContain('restic')
    expect(fault?.detail).toBe('rclone binary not found in PATH')
  })

  it('still names restic when the cause is restic_missing', () => {
    // The other side of the same instrument. Without it, an implementation that
    // answered the rclone copy for EVERY BACKUP_UNAVAILABLE would pass the case
    // above — the mirror image of the defect, and just as fabricated.
    const fault = repoFaultFrom({
      code: 'BACKUP_UNAVAILABLE',
      message: 'restic binary not found in PATH',
      details: { cause: 'restic_missing' },
    })

    expect(fault?.title).toBe('The backup engine is not available.')
    expect(fault?.hint).toContain('restic')
    expect(fault?.hint).not.toContain('rclone')
  })

  // There is deliberately NO case here for a BACKUP_UNAVAILABLE carrying no
  // cause. engineUnavailable always sets `details.cause` to one of the two
  // literals above, and it is the only thing in the backend that mints this
  // code (backup.go:960), so such a body cannot arrive. The neutral return in
  // repoFaultFrom exists because `details.cause` is typed `string | undefined`
  // and TypeScript forces a return, not because the route is reachable —
  // pinning it would assert against a shape the server cannot send, which is
  // the dead branch this wave has spent its time deleting. The repoState
  // switch's own `default` arm is untested for exactly the same reason.

  it('declines VALIDATION_ERROR, which is not a repository fault', () => {
    // Pins criterion 5's boundary. cloudTest's 400 is served by a site-local
    // read in useBackupActions, NOT by an arm here: previewSnapshot mints
    // VALIDATION_ERROR too, so an arm here would change that consumer's
    // rendering as a side effect.
    expect(
      repoFaultFrom({ code: 'VALIDATION_ERROR', message: 'rclone remote is not configured' }),
    ).toBeNull()
  })

  // agent-os-06c1: `detail` is declared `string` on RepoFault and is reached
  // through an unchecked assertion on an `unknown` error.
  it('does not let a non-string message escape into RepoFault.detail', () => {
    const fault = repoFaultFrom({
      code: 'BACKUP_UNAVAILABLE',
      message: { nested: 'object' },
      details: { cause: 'restic_missing' },
    })
    expect(fault).not.toBeNull()
    expect(typeof fault!.detail).toBe('string')
    expect(fault!.detail).toBe('')
  })

  it('still carries a real string message, so nothing reachable changed', () => {
    const fault = repoFaultFrom({
      code: 'BACKUP_UNAVAILABLE',
      message: 'restic is not installed',
      details: { cause: 'restic_missing' },
    })
    expect(fault!.detail).toBe('restic is not installed')
  })
})
