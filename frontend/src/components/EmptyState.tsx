import type { ReactNode } from 'react'

interface EmptyStateProps {
  icon?: ReactNode
  title: string
  description?: string
  action?: ReactNode
}

// Neutral "empty tray" illustration. A plain circle-plus reads as an actionable
// "add" button (ST-7); an inbox glyph reads as "nothing here yet" instead.
const DefaultIcon = () => (
  <svg
    xmlns="http://www.w3.org/2000/svg"
    width="48"
    height="48"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    strokeWidth="1.5"
    strokeLinecap="round"
    strokeLinejoin="round"
    className="text-muted-foreground"
    aria-hidden="true"
  >
    <path d="M22 12h-6l-2 3h-4l-2-3H2" />
    <path d="M5.45 5.11 2 12v6a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-6l-3.45-6.89A2 2 0 0 0 16.76 4H7.24a2 2 0 0 0-1.79 1.11z" />
  </svg>
)

export function EmptyState({ icon, title, description, action }: EmptyStateProps) {
  return (
    <div className="flex flex-col items-center justify-center min-h-[400px] text-center p-8">
      {icon || <DefaultIcon />}
      <h3 className="text-lg font-semibold mt-4 mb-2">{title}</h3>
      {description && <p className="text-sm text-muted-foreground mb-4 max-w-md">{description}</p>}
      {action && <div>{action}</div>}
    </div>
  )
}
