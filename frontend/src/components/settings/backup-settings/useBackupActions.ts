import { toast } from 'sonner'
import { useInitRepo, useTestCloud } from '@/hooks/useBackup'

/**
 * The two standalone backup engine actions: initializing the restic
 * repository and testing rclone connectivity. Both toast an error when the
 * request itself fails, and separately when the request succeeds but the
 * response reports the operation didn't actually succeed.
 */
export function useBackupActions() {
  const initRepo = useInitRepo()
  const testCloud = useTestCloud()

  const handleInitRepo = () => {
    initRepo.mutate(undefined, {
      onSuccess: (data) => {
        if (data.initialized) {
          toast.success('Repository initialized successfully')
        } else {
          toast.error('Repository initialization reported not-initialized')
        }
      },
      onError: () => toast.error('Failed to initialize repository'),
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
