import { useEffect, useState } from 'react'
import { toast } from 'sonner'
import { useUpdateBackupSettings } from '@/hooks/useBackup'
import type { BackupSettings } from '@/types'
import { buildPayload, toDraft } from './backup-payload'
import { settingsSaveFault } from '@/lib/settings-save-fault'
import { presentError, presentFault } from '@/lib/error-handler'
import type { Draft } from './types'

/**
 * Owns the draft/password editing state for backup settings: syncing the
 * server value into an editable draft once on load (not on every refetch, to
 * avoid overwriting in-progress edits), tracking dirtiness against the last
 * persisted values, and the save/discard/clear-password mutations.
 *
 * Must be called unconditionally before any loading/error early return in the
 * caller, so `settings` may still be `undefined` here — every derived value
 * degrades to an inert default (no draft, no pending changes) until it loads.
 */
export function useBackupForm(settings: BackupSettings | undefined) {
  const updateSettings = useUpdateBackupSettings()

  const [initialized, setInitialized] = useState(false)
  const [draft, setDraft] = useState<Draft | null>(null)
  // Password field: empty = do not send; populated = update
  const [password, setPassword] = useState('')

  useEffect(() => {
    if (settings && !initialized) {
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setDraft(toDraft(settings))
      setInitialized(true)
    }
  }, [settings, initialized])

  const set = <K extends keyof Draft>(key: K, value: Draft[K]) =>
    setDraft((prev) => (prev ? { ...prev, [key]: value } : prev))

  // Edits live only in `draft`/`password` until saved; compare against the last
  // persisted server values to know whether anything is pending.
  const pendingChanges = settings && draft ? buildPayload(settings, draft, password) : {}
  const isDirty = Object.keys(pendingChanges).length > 0

  const handleSave = () => {
    if (!settings || !draft) return
    const payload = buildPayload(settings, draft, password)
    if (Object.keys(payload).length === 0) {
      toast.info('No changes to save')
      return
    }
    updateSettings.mutate(payload, {
      onSuccess: () => {
        toast.success('Backup settings saved')
        setPassword('')
      },
      // Takes the error (agent-os-zlw0). This endpoint refuses a repository
      // still carrying the *** redaction marker, and an inline credential, with
      // a 422 VALIDATION_ERROR whose sentence is the only way to tell the two
      // apart — plus 422 ENCRYPTION_KEY_MISSING on the password write.
      // presentFault keeps the no-cause path a SINGLE-argument call, which a
      // pre-existing test still pins: see the WHY at UpdateScheduleContent's
      // onError.
      onError: (error) => {
        // presentFault, NOT presentError (agent-os-5g8a): the cause is read by
        // settingsSaveFault, which is CODE-keyed and deliberately not
        // classifyError. The full argument, measured, is in presentFault's
        // docblock; the short form is that swapping the key is not this
        // change's decision to take.
        presentFault('Failed to save backup settings', settingsSaveFault(error))
      },
    })
  }

  const handleDiscard = () => {
    if (!settings) return
    setDraft(toDraft(settings))
    setPassword('')
  }

  const handleClearPassword = () => {
    // Send explicit empty string to revert to env fallback
    updateSettings.mutate(
      { password: '' },
      {
        onSuccess: () => toast.success('Password cleared — reverted to environment fallback'),
        onError: (error) => presentError(error, { fallback: 'Failed to clear password' }),
      },
    )
  }

  return {
    draft,
    set,
    password,
    setPassword,
    isDirty,
    isSaving: updateSettings.isPending,
    handleSave,
    handleDiscard,
    handleClearPassword,
  }
}
