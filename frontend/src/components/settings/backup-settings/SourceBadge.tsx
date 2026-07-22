import { Badge } from '@/components/ui/badge'
import type { Source } from './types'

const SOURCE_BADGE_LABELS: Record<Source, string> = {
  env: 'from environment',
  db: 'saved',
  default: 'default',
}

const SOURCE_BADGE_VARIANTS: Record<Source, 'default' | 'secondary' | 'outline'> = {
  env: 'default',
  db: 'secondary',
  default: 'outline',
}

export function SourceBadge({ source }: { source: Source }) {
  return (
    <Badge variant={SOURCE_BADGE_VARIANTS[source]} className="text-xs font-normal">
      {SOURCE_BADGE_LABELS[source]}
    </Badge>
  )
}
