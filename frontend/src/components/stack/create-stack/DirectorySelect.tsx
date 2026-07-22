import { Label } from '@/components/ui/label'
import { Badge } from '@/components/ui/badge'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'

interface DirectorySelectProps {
  stacksDir?: string
  stacksDirectories: string[]
  selectedDir: string
  onSelectedDirChange: (dir: string) => void
}

export function DirectorySelect({
  stacksDir,
  stacksDirectories,
  selectedDir,
  onSelectedDirChange,
}: DirectorySelectProps) {
  return (
    <div className="space-y-2">
      <Label htmlFor="directory">Target Directory</Label>
      <Select value={selectedDir || stacksDir || ''} onValueChange={onSelectedDirChange}>
        <SelectTrigger id="directory">
          <SelectValue placeholder="Select directory" />
        </SelectTrigger>
        <SelectContent>
          {stacksDirectories.map((dir: string) => (
            <SelectItem key={dir} value={dir}>
              <span className="flex items-center gap-2">
                {dir === stacksDir && (
                  <Badge variant="secondary" className="text-[10px] px-1 py-0">Default</Badge>
                )}
                {dir}
              </span>
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      <p className="text-xs text-muted-foreground">
        Choose which monitored directory to create the stack in.
      </p>
    </div>
  )
}
