import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Save, FileCheck, Variable } from 'lucide-react'

interface ComposeToolbarProps {
  onSave: () => void
  onLint: () => void
  onExtract: () => void
  isLoading: boolean
  isSaving: boolean
  isLintingBeforeSave: boolean
  isLinting: boolean
  selectedText: string
  hasUnsavedChanges: boolean
  errorCount: number
}

export function ComposeToolbar({
  onSave,
  onLint,
  onExtract,
  isLoading,
  isSaving,
  isLintingBeforeSave,
  isLinting,
  selectedText,
  hasUnsavedChanges,
  errorCount,
}: ComposeToolbarProps) {
  return (
    <div className="flex items-center justify-between gap-2">
      <div className="flex items-center gap-2 flex-wrap">
        <Button onClick={onSave} disabled={isLoading || isSaving || !hasUnsavedChanges || isLintingBeforeSave}>
          <Save className="mr-2 h-4 w-4" />
          {isLintingBeforeSave ? 'Validating...' : isSaving ? 'Saving...' : 'Save'}
        </Button>
        <Button variant="outline" onClick={onLint} disabled={isLoading || isLinting}>
          <FileCheck className="mr-2 h-4 w-4" />
          {isLinting ? 'Linting...' : 'Lint'}
        </Button>
        <Button
          variant="outline"
          size="sm"
          onClick={onExtract}
          disabled={!selectedText || isLoading || isSaving}
          title={selectedText ? 'Extract selected value to .env file' : 'Select a value in the editor to extract'}
        >
          <Variable className="mr-2 h-4 w-4" />
          Extract to .env
        </Button>
        {hasUnsavedChanges && (
          <Badge variant="secondary" className="text-xs">
            {errorCount > 0 ? `Unsaved changes (${errorCount} errors)` : 'Unsaved changes'}
          </Badge>
        )}
      </div>
      <div className="text-sm text-muted-foreground hidden sm:block">
        Ctrl+S to save
      </div>
    </div>
  )
}
