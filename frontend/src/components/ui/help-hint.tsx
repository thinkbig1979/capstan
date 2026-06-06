import * as React from 'react'
import { Info } from 'lucide-react'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { cn } from '@/lib/utils'

interface HelpHintProps {
  /** Accessible name for the trigger, e.g. "Build cache". Announced as "Help: <label>". */
  label: string
  /** Optional bold heading shown above the body inside the popover. */
  title?: string
  /** Help body. Plain text or rich nodes. */
  children: React.ReactNode
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
      </PopoverContent>
    </Popover>
  )
}
