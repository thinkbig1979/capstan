/**
 * Guard for WCAG 1.4.1 (use of colour) on links that sit in or beside text
 * (agent-os-25k4, F4).
 *
 * A link styled only with a colour and `hover:underline` is told apart from the
 * text around it by colour alone until the pointer is over it. axe's
 * `link-in-text-block` passes it on the 3:1 colour difference, so the scan in
 * a11y.spec.ts cannot go red on it. This test reads the source instead: every
 * class string that carries `hover:underline` must also carry a standing
 * `underline`.
 *
 * BLIND SPOTS, stated so a green run is not read as more than it is:
 *  - It pins the class, not the rendered decoration (jsdom has no computed CSS).
 *  - Only quoted string literals and template literals are scanned. A class
 *    assembled from pieces (`'hover:' + 'underline'`) is not seen.
 *  - Test files are not scanned.
 */
import { describe, it, expect } from 'vitest'

const sources = import.meta.glob('/src/**/*.tsx', {
  query: '?raw',
  import: 'default',
  eager: true,
}) as Record<string, string>

// Files whose `hover:underline` is deliberate. Each entry must still hold such a
// string, so the list cannot go stale.
const EXEMPT: Record<string, string> = {
  '/src/components/ui/button.tsx':
    'the `link` variant is a text-style button used as a stand-alone control, not inline text',
  '/src/components/ui/badge.tsx':
    'the `link` badge variant is a stand-alone control, not inline text',
  '/src/components/ui/help-hint.tsx':
    'replaced by the in-app help work (agent-os-25k4 scope note)',
}

const STRING_LITERAL = /"([^"\n]*)"|'([^'\n]*)'|`([^`]*)`/g

/** Class strings in `text` that carry hover:underline but no standing underline. */
function hoverOnlyUnderlines(text: string): string[] {
  const hits: string[] = []
  for (const m of text.matchAll(STRING_LITERAL)) {
    const value = m[1] ?? m[2] ?? m[3] ?? ''
    if (!value.includes('hover:underline')) continue
    if (value.split(/\s+/).includes('underline')) continue
    hits.push(value.trim())
  }
  return hits
}

const isTest = (path: string) => /__tests__|\.test\.tsx$/.test(path)

describe('in-text links carry a standing underline', () => {
  const files = Object.entries(sources).filter(([path]) => !isTest(path))

  it('scans the component tree (the glob matched files)', () => {
    expect(files.length).toBeGreaterThan(100)
  })

  it('no class string pairs hover:underline with colour alone', () => {
    const offenders = files
      .filter(([path]) => !(path in EXEMPT))
      .flatMap(([path, text]) => hoverOnlyUnderlines(text).map((cls) => `${path}: "${cls}"`))
    expect(offenders).toEqual([])
  })

  it.each(Object.keys(EXEMPT))('exempt entry %s still holds a hover-only underline', (path) => {
    expect(sources[path], `${path} not found; remove the EXEMPT entry`).toBeDefined()
    expect(
      hoverOnlyUnderlines(sources[path]).length,
      `${path} no longer has a hover-only underline; remove the EXEMPT entry (${EXEMPT[path]})`,
    ).toBeGreaterThan(0)
  })

  it('CONTROL: the detector flags a planted hover-only link and passes the fixed one', () => {
    expect(hoverOnlyUnderlines('<a className="text-primary hover:underline">x</a>')).toEqual([
      'text-primary hover:underline',
    ])
    expect(
      hoverOnlyUnderlines('<a className="text-primary underline underline-offset-2">x</a>'),
    ).toEqual([])
    expect(
      hoverOnlyUnderlines('<a className="text-primary underline hover:underline">x</a>'),
    ).toEqual([])
  })
})
