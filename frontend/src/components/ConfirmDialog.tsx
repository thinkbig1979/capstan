import * as React from 'react'
import { Button } from '@/components/ui/button'

interface ConfirmDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: string
  description: string
  confirmText?: string
  onConfirm: () => void
  isDangerous?: boolean
}

function sanitizeText(text: string, maxLength: number): string {
  if (!text) return ''
  
  let sanitized = text.replace(/<[^>]*>/g, '')
  
  sanitized = sanitized.replace(/&/g, '&amp;')
  sanitized = sanitized.replace(/</g, '&lt;')
  sanitized = sanitized.replace(/>/g, '&gt;')
  sanitized = sanitized.replace(/"/g, '&quot;')
  sanitized = sanitized.replace(/'/g, '&#x27;')
  
  if (sanitized.length > maxLength) {
    sanitized = sanitized.substring(0, maxLength) + '...'
  }
  
  return sanitized
}

function validateNoHTML(text: string): boolean {
  return !/<[^>]*>/.test(text)
}

export const ConfirmDialog = React.memo(function ConfirmDialog({
  open,
  onOpenChange,
  title,
  description,
  confirmText = 'Confirm',
  onConfirm,
  isDangerous = false,
}: ConfirmDialogProps) {
  const handleConfirm = React.useCallback(() => {
    onConfirm()
    onOpenChange(false)
  }, [onConfirm, onOpenChange])

  React.useEffect(() => {
    if (!validateNoHTML(title) || !validateNoHTML(description)) {
      console.warn('ConfirmDialog: HTML detected in props, which may indicate an XSS attempt')
    }
  }, [title, description])

  if (!open) return null

  const sanitizedTitle = sanitizeText(title, 100)
  const sanitizedDescription = sanitizeText(description, 500)

  return (
      <div className="fixed inset-0 z-50 flex items-center justify-center">
        <div className="fixed inset-0 bg-black/80" onClick={() => onOpenChange(false)} aria-hidden="true" />
      <div className="relative z-50 w-full max-w-md rounded-lg border bg-background p-6 shadow-lg">
        <h3 className="text-lg font-semibold mb-2">{sanitizedTitle}</h3>
        <p className="text-sm text-muted-foreground mb-6">{sanitizedDescription}</p>
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
            className="min-h-[44px]"
          >
            {confirmText}
          </Button>
        </div>
      </div>
    </div>
  )
})

export function useConfirm() {
  const [state, setState] = React.useState<{
    open: boolean
    title: string
    description: string
    confirmText?: string
    isDangerous?: boolean
    onConfirm: () => void
  } | null>(null)

  const confirm = (
    title: string,
    description: string,
    options?: {
      confirmText?: string
      isDangerous?: boolean
    },
  ) => {
    return new Promise<boolean>((resolve) => {
      setState({
        open: true,
        title,
        description,
        confirmText: options?.confirmText,
        isDangerous: options?.isDangerous,
        onConfirm: React.useCallback(() => resolve(true), []),
      })
    })
  }

  const close = () => {
    setState(null)
  }

  const ConfirmComponent = React.memo(() => {
    if (!state) return null
    return (
      <ConfirmDialog
        open={state.open}
        onOpenChange={(open) => {
          if (!open) {
            close()
          }
        }}
        title={state.title}
        description={state.description}
        confirmText={state.confirmText}
        onConfirm={state.onConfirm}
        isDangerous={state.isDangerous}
      />
    )
  })

  return { confirm, ConfirmComponent }
}
