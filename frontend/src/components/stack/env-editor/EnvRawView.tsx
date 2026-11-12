import { Textarea } from '@/components/ui/textarea'
import { Button } from '@/components/ui/button'
import { Save } from 'lucide-react'

interface EnvRawViewProps {
  rawContent: string
  onRawChange: (value: string) => void
  onSaveRaw: () => void
  saving: boolean
  hasUnsavedChanges: boolean
  /** The rows kept from the locked view could not be filled (agent-os-yt2f). */
  saveRefused: boolean
}

export function EnvRawView({ rawContent, onRawChange, onSaveRaw, saving, hasUnsavedChanges, saveRefused }: EnvRawViewProps) {
  return (
    <div className="space-y-4">
      <Textarea
        value={rawContent}
        onChange={(e) => onRawChange(e.target.value)}
        placeholder="KEY=value"
        className="font-mono min-h-[400px]"
      />
      <div className="flex justify-end">
        <Button onClick={onSaveRaw} disabled={saving || !hasUnsavedChanges || saveRefused}>
          <Save className="mr-2 h-4 w-4" />
          {saving ? 'Saving...' : 'Save'}
        </Button>
      </div>
    </div>
  )
}
