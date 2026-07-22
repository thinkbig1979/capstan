import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { Plus } from 'lucide-react'

interface EnvFileSectionProps {
  showEnv: boolean
  onToggle: () => void
  envContent: string
  onEnvContentChange: (value: string) => void
}

export function EnvFileSection({ showEnv, onToggle, envContent, onEnvContentChange }: EnvFileSectionProps) {
  return (
    <div className="space-y-2">
      <Button
        variant="ghost"
        onClick={onToggle}
        className="w-full justify-start"
      >
        <Plus className="mr-2 h-4 w-4" />
        {showEnv ? 'Hide' : 'Add'} .env File
      </Button>

      {showEnv && (
        <div className="space-y-2 pl-4 border-l-2">
          <Label htmlFor="env">.env File</Label>
          <Textarea
            id="env"
            value={envContent}
            onChange={(e) => onEnvContentChange(e.target.value)}
            placeholder="KEY=value&#10;ANOTHER_KEY=value"
            className="font-mono min-h-[150px]"
          />
          <p className="text-xs text-muted-foreground">
            Creates a .env file alongside compose.yaml. Variables are available via {'${VAR}'} in the compose file. You can also extract hardcoded values from the Compose tab after creation.
          </p>
        </div>
      )}
    </div>
  )
}
