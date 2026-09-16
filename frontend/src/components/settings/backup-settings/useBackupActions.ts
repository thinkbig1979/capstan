import { toast } from 'sonner'
import { useInitRepo, useTestCloud } from '@/hooks/useBackup'
import { repoFaultFrom } from '@/lib/backup-repo-fault'

/**
 * The two standalone backup engine actions: initializing the restic
 * repository and testing rclone connectivity. Both toast an error when the
 * request itself fails, and testCloud separately when the request succeeds but
 * the response reports the operation didn't actually succeed.
 */
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
          toast.error('Failed to initialize repository')
          return
        }
        toast.error(fault.title, fault.detail ? { description: fault.detail } : undefined)
      },
    })
  }

  const handleTestCloud = () => {
    testCloud.mutate(undefined, {
      onSuccess: (data) => {
        if (data.ok) {
          toast.success('Cloud connectivity test passed')
        } else {
          toast.error('Cloud connectivity test failed')
        }
      },
      onError: () => toast.error('Cloud connectivity test failed'),
    })
  }

  return {
    handleInitRepo,
    isInitializing: initRepo.isPending,
    handleTestCloud,
    isTestingCloud: testCloud.isPending,
  }
}
