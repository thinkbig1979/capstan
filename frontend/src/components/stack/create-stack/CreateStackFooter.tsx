import { Button } from '@/components/ui/button'
import { DialogFooter } from '@/components/ui/dialog'
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { AlertCircle } from 'lucide-react'

interface CreateStackFooterProps {
  onCancel: () => void
  onCreate: () => void
  isCreateDisabled: boolean
  isPending: boolean
  validationErrors: { field: string; message: string }[]
}

export function CreateStackFooter({
  onCancel,
  onCreate,
  isCreateDisabled,
  isPending,
  validationErrors,
}: CreateStackFooterProps) {
  return (
    <DialogFooter className="mt-6 flex-col items-stretch gap-2">
      <div className="flex flex-col-reverse sm:flex-row sm:justify-end sm:space-x-2">
        <Button variant="outline" onClick={onCancel}>
          Cancel
        </Button>
        <TooltipProvider>
          <Tooltip>
            <TooltipTrigger asChild>
              <Button onClick={onCreate} disabled={isCreateDisabled}>
                {isPending ? 'Creating...' : 'Create Stack'}
              </Button>
            </TooltipTrigger>
            {isCreateDisabled && !isPending && (
              <TooltipContent>
                <p>Fix validation errors to create stack</p>
              </TooltipContent>
            )}
          </Tooltip>
        </TooltipProvider>
      </div>
      {validationErrors.length > 0 && (
        <div className="rounded-md border border-destructive/30 bg-destructive/10 p-3">
          <div className="text-sm font-medium text-destructive mb-2">
            Please fix the following errors:
          </div>
          <ul className="space-y-1">
             {validationErrors.map((error) => (
               <li key={error.field}>
                 <button
                   type="button"
                   className="text-sm text-destructive hover:underline flex items-center gap-1"
                 >
                   <AlertCircle className="h-3 w-3" />
                   {error.message}
                 </button>
               </li>
             ))}
          </ul>
        </div>
      )}
    </DialogFooter>
  )
}
