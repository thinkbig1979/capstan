import * as React from 'react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

interface ConfirmDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: string
  description: string
  confirmText?: string
  onConfirm: () => void
  isDangerous?: boolean
  /** When set, the user must type this exact string before Confirm enables. */
  requireConfirmationText?: string
}

export const ConfirmDialog = React.memo(function ConfirmDialog({
  open,
  onOpenChange,
  title,
  description,
  confirmText = 'Confirm',
  onConfirm,
  isDangerous = false,
  requireConfirmationText,
}: ConfirmDialogProps) {
  const [typed, setTyped] = React.useState('')

  // Reset the typed value each time the dialog opens
  React.useEffect(() => {
    if (open) setTyped('')
  }, [open])

  const typedMatches = !requireConfirmationText || typed === requireConfirmationText

  const handleConfirm = React.useCallback(() => {
    if (!typedMatches) return
    onConfirm()
    onOpenChange(false)
  }, [onConfirm, onOpenChange, typedMatches])

  if (!open) return null

  return (
      <div className="fixed inset-0 z-50 flex items-center justify-center">
        <div className="fixed inset-0 bg-black/80" onClick={() => onOpenChange(false)} aria-hidden="true" />
      <div className="relative z-50 w-full max-w-md rounded-lg border bg-background p-6 shadow-lg">
        <h3 className="text-lg font-semibold mb-2">{title}</h3>
        <p className="text-sm text-muted-foreground mb-6">{description}</p>
        {requireConfirmationText && (
          <div className="mb-6 space-y-2">
            <Label htmlFor="confirm-typed" className="text-sm font-normal">
              Type <span className="font-mono font-semibold text-foreground">{requireConfirmationText}</span> to confirm
            </Label>
            <Input
              id="confirm-typed"
              value={typed}
              onChange={(e) => setTyped(e.target.value)}
              autoComplete="off"
              autoFocus
              onKeyDown={(e) => { if (e.key === 'Enter') handleConfirm() }}
              aria-label={`Type ${requireConfirmationText} to confirm`}
            />
          </div>
        )}
        <div className="flex flex-col-reverse sm:flex-row sm:justify-end sm:space-x-2 gap-2">
          <Button
            variant="outline"
            onClick={() => onOpenChange(false)}
            className="min-h-[44px]"
            aria-label="Cancel"
          >
            Cancel
          </Button>
          <Button
            variant={isDangerous ? 'destructive' : 'default'}
            onClick={handleConfirm}
            disabled={!typedMatches}
            className="min-h-[44px]"
          >
            {confirmText}
          </Button>
        </div>
      </div>
    </div>
  )
})

interface ConfirmState {
  title: string
  description: string
  confirmText?: string
  isDangerous?: boolean
  requireConfirmationText?: string
  onConfirm: () => void
}

function ConfirmDialogRenderer({ state, onClose }: {
  state: ConfirmState | null
  onClose: () => void
}) {
  const handleOpenChange = React.useCallback((open: boolean) => {
    if (!open) onClose()
  }, [onClose])

  if (!state) return null
  return (
    <ConfirmDialog
      open={true}
      onOpenChange={handleOpenChange}
      title={state.title}
      description={state.description}
      confirmText={state.confirmText}
      onConfirm={state.onConfirm}
      isDangerous={state.isDangerous}
      requireConfirmationText={state.requireConfirmationText}
    />
  )
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
      return <ConfirmDialogRenderer state={stateRef.current} onClose={close} />
    },
    [close],
  )

  return { confirm, ConfirmComponent }
}
