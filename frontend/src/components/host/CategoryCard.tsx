import { Link } from 'react-router'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import type { HostView } from './host-views'

interface CategoryCardProps {
  category: HostView
  title: string
  /** The headline figure: a count or a size, already formatted. */
  value: string
  detail: string
  /** What can be removed; absent means nothing can. */
  reclaim?: string
  viewHref: string
  /** Opens the Free-up-space dialog; shown only when something can be removed. */
  onCleanUp?: () => void
}

/**
 * One Docker host category: its headline figure, a line of detail, and either
 * what can be removed or "Nothing to clean up". Clean up opens the
 * Free-up-space dialog (agent-os-f9cf).
 */
export function CategoryCard({ category, title, value, detail, reclaim, viewHref, onCleanUp }: CategoryCardProps) {
  const testId = `host-card-${category}`
  return (
    <Card data-testid={testId} className="flex flex-col">
      <CardContent className="flex flex-1 flex-col gap-2 pt-6">
        <h2 className="text-sm font-medium text-muted-foreground">{title}</h2>
        <p className="font-mono text-2xl font-semibold tabular-nums">{value}</p>
        <p className="text-sm text-muted-foreground">{detail}</p>
        <div className="mt-auto pt-2">
          {reclaim ? (
            <p data-testid={`${testId}-reclaim`} className="text-sm font-medium text-primary">
              {reclaim}
            </p>
          ) : (
            <p className="text-sm text-success">Nothing to clean up</p>
          )}
        </div>
        <div className="flex items-center gap-2 pt-1">
          {reclaim && onCleanUp && (
            <Button size="sm" onClick={onCleanUp} aria-label={`Clean up ${title.toLowerCase()}`}>
              Clean up
            </Button>
          )}
          <Button asChild variant="outline" size="sm">
            <Link to={viewHref} aria-label={`View all ${title.toLowerCase()}`}>
              View all
            </Link>
          </Button>
        </div>
      </CardContent>
    </Card>
  )
}
