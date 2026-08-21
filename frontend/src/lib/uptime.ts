import type { Container } from '@/types'

const UNIT_SECONDS: Record<string, number> = {
  second: 1,
  minute: 60,
  hour: 3600,
  day: 86400,
  week: 604800,
  month: 2592000,
  year: 31536000,
}

interface ParsedUptime {
  seconds: number
  /** The human phrase as Docker reported it, e.g. "6 days" or "About an hour". */
  label: string
}

// Docker status strings look like "Up 2 hours", "Up About a minute" or
// "Up 6 days (healthy)". Anything else (including "Up Less than a second")
// yields null and simply produces no uptime chip.
export function parseContainerUptime(status: string): ParsedUptime | null {
  const m = /^Up\s+((?:About\s+)?(?:(\d+)|an?)\s+(second|minute|hour|day|week|month|year)s?)\b/i.exec(status)
  if (!m) return null
  const n = m[2] ? parseInt(m[2], 10) : 1
  return { seconds: n * UNIT_SECONDS[m[3].toLowerCase()], label: m[1] }
}

/**
 * Stack-level uptime derived from container data: the uptime of the most
 * recently (re)started running container, so the chip never claims more
 * uptime than the stack as a whole has. Null when nothing running parses.
 */
export function stackUptime(containers: Container[] | undefined): string | null {
  if (!containers?.length) return null
  let youngest: ParsedUptime | null = null
  for (const c of containers) {
    if (c.state !== 'running') continue
    const parsed = parseContainerUptime(c.status)
    if (!parsed) continue
    if (!youngest || parsed.seconds < youngest.seconds) youngest = parsed
  }
  return youngest ? `up ${youngest.label}` : null
}
