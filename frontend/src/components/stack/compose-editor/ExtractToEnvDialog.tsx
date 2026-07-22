import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Variable } from 'lucide-react'

interface ExtractToEnvDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  selectedText: string
  extractVarName: string
  onExtractVarNameChange: (name: string) => void
  isExtracting: boolean
  onConfirm: () => void
}

export function ExtractToEnvDialog({
  open,
  onOpenChange,
  selectedText,
  extractVarName,
  onExtractVarNameChange,
  isExtracting,
  onConfirm,
}: ExtractToEnvDialogProps) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <Variable className="h-5 w-5" />
            Extract to .env
          </DialogTitle>
          <DialogDescription>
            Replace the selected value with a variable reference and add it to the .env file.
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-4">
          <div className="space-y-2">
            <div className="text-sm text-muted-foreground">Selected value:</div>
            <code className="block rounded-md bg-muted p-2 text-sm font-mono break-all">
              {selectedText.length > 100 ? `${selectedText.slice(0, 100)}...` : selectedText}
            </code>
          </div>
          <div className="space-y-2">
            <Label htmlFor="extract-var-name">Variable name</Label>
            <Input
              id="extract-var-name"
              value={extractVarName}
              onChange={(e) => onExtractVarNameChange(e.target.value.toUpperCase().replace(/[^A-Z0-9_]/g, '_'))}
              placeholder="VARIABLE_NAME"
              className="font-mono"
            />
          </div>
          <div className="text-sm text-muted-foreground">
            Will become: <code className="font-mono">${'{'}{extractVarName || 'VARIABLE_NAME'}{'}'}</code> in compose, <code className="font-mono">{extractVarName || 'VARIABLE_NAME'}={selectedText}</code> in .env
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button onClick={onConfirm} disabled={!extractVarName.trim() || isExtracting}>
            {isExtracting ? 'Extracting...' : 'Extract'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
