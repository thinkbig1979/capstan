import { Link, useLocation } from 'react-router'
import { Badge } from '@/components/ui/badge'
import { CheckSquare, Square as SquareIcon, Star } from 'lucide-react'
import type { Stack } from '@/types'
import { statusDotColor } from './constants'

interface StackRowProps {
  stack: Stack
  selecting: boolean
  selected: boolean
  onToggleSelect: () => void
  pinned: boolean
  onTogglePin: () => void
}

export function StackRow({ stack, selecting, selected, onToggleSelect, pinned, onTogglePin }: StackRowProps) {
  const location = useLocation()
  const dotColor = statusDotColor[stack.status] || statusDotColor.unknown

  // Selection mode: a non-navigating row with a checkbox.
  if (selecting) {
    return (
      <button
        type="button"
        onClick={onToggleSelect}
        aria-pressed={selected}
        className={`flex items-center gap-2 px-3 py-1.5 rounded text-sm w-full text-left transition-colors ${
          selected
            ? 'bg-sidebar-accent text-sidebar-accent-foreground'
            : 'hover:bg-sidebar-accent/50 text-sidebar-foreground'
        }`}
      >
        {selected ? (
          <CheckSquare className="h-4 w-4 shrink-0 text-primary" />
        ) : (
          <SquareIcon className="h-4 w-4 shrink-0 text-muted-foreground" />
        )}
        <span className={`h-2 w-2 rounded-full shrink-0 ${dotColor}`} aria-hidden="true" />
        <span className="flex-1 truncate">{stack.projectName}</span>
      </button>
    )
  }

  const isActive = location.pathname.startsWith(`/stacks/${stack.id}`)
  return (
    <Link
      to={`/stacks/${stack.id}`}
      className={`group flex items-center gap-2 px-3 py-1.5 rounded text-sm transition-colors ${
        isActive
          ? 'bg-sidebar-accent text-sidebar-accent-foreground font-medium'
          : 'hover:bg-sidebar-accent/50 text-sidebar-foreground'
      }`}
      aria-label={`${stack.projectName} - ${stack.status}`}
    >
      <span className={`h-2 w-2 rounded-full shrink-0 ${dotColor}`} aria-hidden="true" />
      <span className="flex-1 truncate">{stack.projectName}</span>
      {!!stack.containers?.length && (
        <Badge
          variant="secondary"
          className="h-4 min-w-5 px-1 text-[10px] leading-none"
        >
          {stack.containers.length}
        </Badge>
      )}
      {stack.isGitRepo && stack.gitDirty && (
        <span
          className="h-1.5 w-1.5 rounded-full bg-warning shrink-0"
          title="Uncommitted changes"
        />
      )}
      <button
        type="button"
        aria-label={pinned ? `Unpin ${stack.projectName}` : `Pin ${stack.projectName}`}
        title={pinned ? 'Unpin' : 'Pin to top'}
        onClick={(e) => {
          e.preventDefault()
          e.stopPropagation()
          onTogglePin()
        }}
        onKeyDown={(e) => {
          if (e.key === 'Enter' || e.key === ' ') {
            e.preventDefault()
            e.stopPropagation()
            onTogglePin()
          }
        }}
        className={`shrink-0 rounded p-0.5 hover:text-warning transition-opacity ${
          pinned
            ? 'text-warning opacity-100'
            : 'text-muted-foreground opacity-0 group-hover:opacity-100 focus:opacity-100'
        }`}
      >
        <Star className={`h-3 w-3 ${pinned ? 'fill-current' : ''}`} />
      </button>
    </Link>
  )
}
