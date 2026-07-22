import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Undo, Redo } from 'lucide-react'
import { EnvUnlockStatus } from '@/components/EnvUnlockStatus'

interface EnvEditorToolbarProps {
  view: 'table' | 'raw'
  onViewChange: (view: 'table' | 'raw') => void
  canUndo: boolean
  canRedo: boolean
  onUndo: () => void
  onRedo: () => void
  hasUnsavedChanges: boolean
}

export function EnvEditorToolbar({
  view,
  onViewChange,
  canUndo,
  canRedo,
  onUndo,
  onRedo,
  hasUnsavedChanges,
}: EnvEditorToolbarProps) {
  return (
    <div className="flex items-center justify-between">
      <div className="flex items-center gap-2">
        <Tabs value={view} onValueChange={(v: string) => onViewChange(v as 'table' | 'raw')}>
          <TabsList>
            <TabsTrigger value="table">Table View</TabsTrigger>
            <TabsTrigger value="raw">Raw Editor</TabsTrigger>
          </TabsList>
        </Tabs>
        <div className="flex items-center gap-1 ml-2">
          <Button
            variant="ghost"
            size="icon"
            onClick={onUndo}
            disabled={!canUndo}
            title="Undo (Ctrl+Z)"
          >
            <Undo className="h-4 w-4" />
          </Button>
          <Button
            variant="ghost"
            size="icon"
            onClick={onRedo}
            disabled={!canRedo}
            title="Redo (Ctrl+Y)"
          >
            <Redo className="h-4 w-4" />
          </Button>
        </div>
      </div>

      <div className="flex items-center gap-2">
        <EnvUnlockStatus />
        {hasUnsavedChanges && (
          <Badge variant="secondary" className="text-xs">
            Unsaved changes
          </Badge>
        )}
      </div>
    </div>
  )
}
