import { parseAnsi, hasAnsi, ansiSegmentClassName } from '@/lib/ansi'
import { escapeRegExp } from './log-utils'

function highlightSearchTerm(text: string, searchTerm: string): React.ReactNode {
  if (!searchTerm) return text

  const escaped = escapeRegExp(searchTerm)
  const parts = text.split(new RegExp(`(${escaped})`, 'gi'))
  return parts.map((part, i) =>
    part.toLowerCase() === searchTerm.toLowerCase() ? (
      <mark key={i} className="bg-warning/40 text-warning-foreground rounded px-0.5">
        {part}
      </mark>
    ) : (
      <span key={i}>{part}</span>
    )
  )
}

// Render a log message: ANSI-styled spans when escape codes are present,
// otherwise plain text. Search matches are highlighted within either path.
export function renderMessage(message: string, searchTerm: string): React.ReactNode {
  if (!hasAnsi(message)) {
    return highlightSearchTerm(message, searchTerm)
  }
  return parseAnsi(message).map((seg, i) => (
    <span key={i} className={ansiSegmentClassName(seg)}>
      {highlightSearchTerm(seg.text, searchTerm)}
    </span>
  ))
}
