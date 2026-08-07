import * as React from 'react'
import { ExternalLink, Info } from 'lucide-react'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { cn } from '@/lib/utils'

interface HelpHintProps {
  /** Accessible name for the trigger, e.g. "Build cache". Announced as "Help: <label>". */
  label: string
  /** Optional bold heading shown above the body inside the popover. */
  title?: string
  /** Help body. Plain text or rich nodes. */
  children: React.ReactNode
  /**
   * Optional URL to a docs page with more detail. Renders a "Learn more" link
   * in the popover footer, opening in a new tab. Omit when the hint's own
   * text already fully answers the question it raises — don't force a link
   * just because one is available.
   */
  href?: string
  side?: 'top' | 'right' | 'bottom' | 'left'
  align?: 'start' | 'center' | 'end'
  /** Extra classes for the trigger button (e.g. spacing). */
  className?: string
  /** Extra classes for the icon (e.g. sizing). */
  iconClassName?: string
}

/**
 * A small, clickable info icon that opens a popover with contextual help.
 * Click (not hover) so the text stays put while the user reads it, and so it
 * works on touch. Use for features that aren't self-explanatory.
 */
export function HelpHint({
  label,
  title,
  children,
  href,
  side = 'top',
  align = 'center',
  className,
  iconClassName,
}: HelpHintProps) {
  return (
    <Popover>
      <PopoverTrigger asChild>
        <button
          type="button"
          aria-label={`Help: ${label}`}
          // Stop the click from reaching parents (table rows, tab headers) that
          // have their own onClick navigation.
          onClick={(e: React.MouseEvent) => e.stopPropagation()}
          className={cn(
            'inline-flex h-5 w-5 shrink-0 items-center justify-center rounded-full text-muted-foreground/70 transition-colors hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring',
            className,
          )}
        >
          <Info className={cn('h-3.5 w-3.5', iconClassName)} />
        </button>
      </PopoverTrigger>
      <PopoverContent
        side={side}
        align={align}
        className="w-80 text-sm font-normal"
        onClick={(e: React.MouseEvent) => e.stopPropagation()}
      >
        {title &&<p className="mb-1.5 font-medium text-foreground">{title}</p>}
        <div className="space-y-1.5 leading-relaxed text-muted-foreground">{children}</div>
        {href && (
          <a
            href={href}
            target="_blank"
            rel="noopener noreferrer"
            aria-label={`Learn more about ${label}`}
            className="mt-2 inline-flex items-center gap-1 text-sm text-primary hover:underline"
          >
            Learn more
            <ExternalLink className="h-3 w-3" />
          </a>
        )}
      </PopoverContent>
    </Popover>
  )
}
