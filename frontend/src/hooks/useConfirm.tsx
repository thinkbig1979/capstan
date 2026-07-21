import * as React from 'react'
import { ConfirmDialog } from '@/components/ConfirmDialog'

interface ConfirmState {
  title: string
  description: string
  confirmText?: string
  isDangerous?: boolean
  requireConfirmationText?: string
  onConfirm: () => void
}

export function useConfirm() {
  const [, forceUpdate] = React.useState(0)
  const resolveRef = React.useRef<((value: boolean) => void) | null>(null)
  const stateRef = React.useRef<ConfirmState | null>(null)

  const confirm = React.useCallback((
    title: string,
    description: string,
    options?: {
      confirmText?: string
      isDangerous?: boolean
      requireConfirmationText?: string
    },
  ) => {
    return new Promise<boolean>((resolve) => {
      resolveRef.current = resolve
      stateRef.current = {
        title,
        description,
        confirmText: options?.confirmText,
        isDangerous: options?.isDangerous,
        requireConfirmationText: options?.requireConfirmationText,
        onConfirm: () => {
          resolve(true)
          resolveRef.current = null
        },
      }
      forceUpdate((n) => n + 1)
    })
  }, [])

  const close = React.useCallback(() => {
    if (resolveRef.current) {
      resolveRef.current(false)
      resolveRef.current = null
    }
    stateRef.current = null
    forceUpdate((n) => n + 1)
  }, [])

  const ConfirmComponent = React.useMemo(
    () => function StableConfirmComponent() {
      const state = stateRef.current
      if (!state) return null
      return (
        <ConfirmDialog
          open={true}
          onOpenChange={(open) => { if (!open) close() }}
          title={state.title}
          description={state.description}
          confirmText={state.confirmText}
          onConfirm={state.onConfirm}
          isDangerous={state.isDangerous}
          requireConfirmationText={state.requireConfirmationText}
        />
      )
    },
    [close],
  )

  return { confirm, ConfirmComponent }
}
