// Minimal ANSI SGR (Select Graphic Rendition) parser for the log viewer.
//
// Container logs frequently embed ANSI escape codes (colored framework output,
// `docker compose` progress, app loggers). Rendering them as raw `\x1b[31m`
// noise hurts readability, so we parse the standard 16-color + style codes into
// structured segments and map them to theme-aware Tailwind classes that stay
// legible on the muted log background in BOTH light and dark themes.
//
// Scope on purpose: the 16 named colors + bold/dim/italic/underline cover the
// overwhelming majority of real CLI output. 256-color and truecolor (38;5 /
// 38;2) sequences are parsed and consumed so they never leak as garbage, but
// their exact color is dropped (rendered in the default foreground) rather than
// risk an unreadable shade against one of the themes. Non-SGR CSI sequences
// (cursor moves, line erase) are stripped silently.

type AnsiColor =
  | 'black'
  | 'red'
  | 'green'
  | 'yellow'
  | 'blue'
  | 'magenta'
  | 'cyan'
  | 'white'
  | 'brightBlack'
  | 'brightRed'
  | 'brightGreen'
  | 'brightYellow'
  | 'brightBlue'
  | 'brightMagenta'
  | 'brightCyan'
  | 'brightWhite'

export interface AnsiStyle {
  fg?: AnsiColor
  bold?: boolean
  dim?: boolean
  italic?: boolean
  underline?: boolean
}

export interface AnsiSegment extends AnsiStyle {
  text: string
}

const NAMED: AnsiColor[] = ['black', 'red', 'green', 'yellow', 'blue', 'magenta', 'cyan', 'white']
const BRIGHT: AnsiColor[] = [
  'brightBlack',
  'brightRed',
  'brightGreen',
  'brightYellow',
  'brightBlue',
  'brightMagenta',
  'brightCyan',
  'brightWhite',
]

// Theme-aware foreground classes. Each pairs a darker shade for light themes
// with a lighter shade for dark themes (the -600/-400 pattern used elsewhere).
// Bright variants nudge one step brighter (-500/-300) to honor the intensity
// while staying readable.
const FG_CLASS: Record<AnsiColor, string> = {
  black: 'text-zinc-500',
  red: 'text-red-600 dark:text-red-400',
  green: 'text-green-600 dark:text-green-400',
  yellow: 'text-amber-600 dark:text-amber-400',
  blue: 'text-blue-600 dark:text-blue-400',
  magenta: 'text-fuchsia-600 dark:text-fuchsia-400',
  cyan: 'text-cyan-600 dark:text-cyan-400',
  white: '', // default foreground stays the most legible choice
  brightBlack: 'text-muted-foreground',
  brightRed: 'text-red-500 dark:text-red-300',
  brightGreen: 'text-green-500 dark:text-green-300',
  brightYellow: 'text-amber-500 dark:text-amber-300',
  brightBlue: 'text-blue-500 dark:text-blue-300',
  brightMagenta: 'text-fuchsia-500 dark:text-fuchsia-300',
  brightCyan: 'text-cyan-500 dark:text-cyan-300',
  brightWhite: '',
}

// Matches a single CSI sequence: ESC [ <params> <final-byte>.
// eslint-disable-next-line no-control-regex
const CSI = /\x1b\[[0-9;?]*[ -/]*[@-~]/g
// eslint-disable-next-line no-control-regex
const SGR = /^\x1b\[([0-9;]*)m$/

/** True if the string contains any ANSI escape sequence. */
export function hasAnsi(text: string): boolean {
  CSI.lastIndex = 0
  return CSI.test(text)
}

/** Remove all ANSI escape sequences, returning plain text. */
export function stripAnsi(text: string): string {
  return text.replace(CSI, '')
}

function applyCodes(style: AnsiStyle, codes: number[]): AnsiStyle {
  let next = { ...style }
  for (let i = 0; i < codes.length; i++) {
    const code = codes[i]
    if (code === 0) {
      next = {}
    } else if (code === 1) {
      next.bold = true
    } else if (code === 2) {
      next.dim = true
    } else if (code === 3) {
      next.italic = true
    } else if (code === 4) {
      next.underline = true
    } else if (code === 22) {
      next.bold = false
      next.dim = false
    } else if (code === 23) {
      next.italic = false
    } else if (code === 24) {
      next.underline = false
    } else if (code >= 30 && code <= 37) {
      next.fg = NAMED[code - 30]
    } else if (code === 38 || code === 48) {
      // Extended color: 38;5;n (256) or 38;2;r;g;b (truecolor). Consume the
      // parameters so they don't get misread as styles; drop the exact color.
      const mode = codes[i + 1]
      if (mode === 5) {
        i += 2
        if (code === 38) next.fg = undefined
      } else if (mode === 2) {
        i += 4
        if (code === 38) next.fg = undefined
      }
    } else if (code === 39) {
      next.fg = undefined
    } else if (code >= 90 && code <= 97) {
      next.fg = BRIGHT[code - 90]
    }
    // 40-47 / 49 / 100-107 (backgrounds) are intentionally ignored for
    // legibility but consumed by the loop so they don't corrupt parsing.
  }
  return next
}

/**
 * Parse a string with ANSI SGR codes into styled segments.
 *
 * Plain text (no escapes) yields a single segment with no style. Non-SGR CSI
 * sequences are stripped without producing a segment break.
 */
export function parseAnsi(text: string): AnsiSegment[] {
  const segments: AnsiSegment[] = []
  let style: AnsiStyle = {}
  let lastIndex = 0

  CSI.lastIndex = 0
  let match: RegExpExecArray | null
  while ((match = CSI.exec(text)) !== null) {
    const seq = match[0]
    const start = match.index
    if (start > lastIndex) {
      segments.push({ text: text.slice(lastIndex, start), ...style })
    }
    const sgr = SGR.exec(seq)
    if (sgr) {
      // Empty params (ESC[m) means reset.
      const codes = sgr[1] === '' ? [0] : sgr[1].split(';').map((n) => parseInt(n, 10))
      style = applyCodes(style, codes)
    }
    lastIndex = start + seq.length
  }

  if (lastIndex < text.length) {
    segments.push({ text: text.slice(lastIndex), ...style })
  }

  if (segments.length === 0) {
    segments.push({ text: '' })
  }

  return segments
}

/** Map a segment's style to a Tailwind className string. */
export function ansiSegmentClassName(seg: AnsiStyle): string {
  const classes: string[] = []
  if (seg.fg) {
    const c = FG_CLASS[seg.fg]
    if (c) classes.push(c)
  }
  if (seg.bold) classes.push('font-bold')
  if (seg.dim) classes.push('opacity-70')
  if (seg.italic) classes.push('italic')
  if (seg.underline) classes.push('underline')
  return classes.join(' ')
}
