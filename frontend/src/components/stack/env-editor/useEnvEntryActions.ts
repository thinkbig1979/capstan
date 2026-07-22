import { useState, type Dispatch, type SetStateAction } from 'react'
import type { EnvEntry } from '@/types'
import { isSensitiveKey } from './sensitiveKey'

interface UseEnvEntryActionsArgs {
  entries: EnvEntry[]
  setEntries: Dispatch<SetStateAction<EnvEntry[]>>
  rawContent: string
  setRawContent: Dispatch<SetStateAction<string>>
  pushToHistory: (entries: EnvEntry[], raw: string) => void
  setHasUnsavedChanges: Dispatch<SetStateAction<boolean>>
  authDisabled: boolean
  isUnlocked: () => boolean
}

/**
 * Entry-level mutation handlers (add/delete/edit, raw-textarea sync) plus the
 * sensitive-value reveal flow, including the unlock-dialog gating: revealing
 * a sensitive value that's locked and auth-enabled opens EnvUnlockDialog
 * instead of revealing it directly.
 */
export function useEnvEntryActions({
  entries,
  setEntries,
  rawContent,
  setRawContent,
  pushToHistory,
  setHasUnsavedChanges,
  authDisabled,
  isUnlocked,
}: UseEnvEntryActionsArgs) {
  const [unlockDialogOpen, setUnlockDialogOpen] = useState(false)
  const [pendingRevealIndex, setPendingRevealIndex] = useState<number | null>(null)

  const handleAddEntry = () => {
    const newEntries = [...entries, { key: '', value: '', sensitive: false }]
    setEntries(newEntries)
    pushToHistory(newEntries, rawContent)
    setHasUnsavedChanges(true)
  }

  const handleDeleteEntry = (index: number) => {
    const newEntries = entries.filter((_, i) => i !== index)
    setEntries(newEntries)
    pushToHistory(newEntries, rawContent)
    setHasUnsavedChanges(true)
  }

  const handleEntryChange = (index: number, field: keyof EnvEntry, value: string | boolean) => {
    const newEntries = [...entries]
    newEntries[index] = { ...newEntries[index], [field]: value }

    if (field === 'key' && typeof value === 'string') {
      newEntries[index].sensitive = isSensitiveKey(value)
    }

    setEntries(newEntries)
    pushToHistory(newEntries, rawContent)
    setHasUnsavedChanges(true)
  }

  const handleRawChange = (newRaw: string) => {
    setRawContent(newRaw)
    pushToHistory(entries, newRaw)
    setHasUnsavedChanges(true)
  }

  const applyVisibilityToggle = (index: number) => {
    const newEntries = [...entries]
    newEntries[index] = { ...newEntries[index], sensitive: !newEntries[index].sensitive }
    setEntries(newEntries)
    pushToHistory(newEntries, rawContent)
  }

  const toggleVisibility = (index: number) => {
    const current = entries[index]
    const willReveal = current?.sensitive === true
    if (!willReveal || authDisabled || isUnlocked()) {
      applyVisibilityToggle(index)
      return
    }
    setPendingRevealIndex(index)
    setUnlockDialogOpen(true)
  }

  const handleUnlocked = () => {
    if (pendingRevealIndex !== null) {
      applyVisibilityToggle(pendingRevealIndex)
    }
    setPendingRevealIndex(null)
  }

  const handleUnlockDialogOpenChange = (open: boolean) => {
    setUnlockDialogOpen(open)
    if (!open) setPendingRevealIndex(null)
  }

  return {
    unlockDialogOpen,
    handleUnlockDialogOpenChange,
    handleAddEntry,
    handleDeleteEntry,
    handleEntryChange,
    handleRawChange,
    toggleVisibility,
    handleUnlocked,
  }
}
