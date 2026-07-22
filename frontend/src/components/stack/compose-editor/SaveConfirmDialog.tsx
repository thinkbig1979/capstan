import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { AlertCircle } from 'lucide-react'
import type { LintResult } from '@/types'

interface SaveConfirmDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  errorCount: number
  lintResults: LintResult[]
  onSaveAnyway: () => void
}

export function SaveConfirmDialog({
  open,
  onOpenChange,
  errorCount,
  lintResults,
  onSaveAnyway,
}: SaveConfirmDialogProps) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <AlertCircle className="h-5 w-5 text-warning" />
            Save with Lint Errors?
          </DialogTitle>
          <DialogDescription>
            Your compose file has <span className="font-semibold text-destructive">{errorCount} error(s)</span>.
            Would you like to save anyway or fix the errors first?
          </DialogDescription>
        </DialogHeader>
        <div className="max-h-60 overflow-y-auto rounded-md border bg-muted/50 p-3">
          <div className="space-y-2">
            <div className="text-sm font-medium">Errors found:</div>
            {lintResults
              .filter((r) => r.level === 'error')
              .map((result) => (
                <div key={`${result.rule}-${result.line ?? 'no-line'}-${result.message}`} className="flex items-start gap-2 text-sm">
                  <AlertCircle className="h-4 w-4 text-destructive mt-0.5 shrink-0" />
                  <div className="flex-1 min-w-0">
                    <div className="font-medium">{result.message}</div>
                    {result.rule && <div className="text-xs text-muted-foreground">{result.rule}</div>}
                  </div>
                </div>
              ))}
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Fix errors first
          </Button>
          <Button onClick={onSaveAnyway}>
            Save anyway
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
