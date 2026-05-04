import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { ChevronDown, ChevronUp } from 'lucide-react'

interface SettingsSection {
  id: string
  title: string
  description: string
  icon: React.ReactNode
  defaultExpanded: boolean
}

interface CollapsibleSectionProps {
  section: SettingsSection
  expanded: boolean
  onToggle: () => void
  children: React.ReactNode
}

export function CollapsibleSection({ section, expanded, onToggle, children }: CollapsibleSectionProps) {
  return (
    <Card>
      <CardHeader
        className="cursor-pointer hover:bg-muted/50 transition-colors select-none"
        onClick={onToggle}
        onKeyDown={(e) => {
          if (e.key === 'Enter' || e.key === ' ') {
            e.preventDefault()
            onToggle()
          }
        }}
        role="button"
        tabIndex={0}
        aria-expanded={expanded}
        aria-controls={`${section.id}-content`}
      >
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-3">
            <div className="p-2 rounded-lg bg-primary/10 text-primary">
              {section.icon}
            </div>
            <div>
              <CardTitle className="text-base">{section.title}</CardTitle>
              <p className="text-sm text-muted-foreground">{section.description}</p>
            </div>
          </div>
          <Button variant="ghost" size="icon" aria-label={expanded ? `Collapse ${section.title}` : `Expand ${section.title}`}>
            {expanded ? <ChevronUp className="h-4 w-4" /> : <ChevronDown className="h-4 w-4" />}
          </Button>
        </div>
      </CardHeader>
      {expanded && (
        <CardContent id={`${section.id}-content`} className="pt-0">
          {children}
        </CardContent>
      )}
    </Card>
  )
}
