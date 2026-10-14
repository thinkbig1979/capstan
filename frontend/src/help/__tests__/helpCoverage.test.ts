import { describe, expect, it } from 'vitest'
import dashboardSource from '@/pages/DashboardPage.tsx?raw'
import stackDetailSource from '@/components/stack/StackDetail.tsx?raw'
import { DASHBOARD_TABS, HOST_VIEWS, SETTINGS_SECTION_IDS, STACK_TABS } from '@/lib/app-views'
import { loadHelpLibrary, type HelpDoc } from '../content'
import { FIELD_HELP, PAGE_HELP, type HelpRef, type PageContextKey } from '../registry'

// Every view the app renders, keyed as the registry keys it.
const VIEW_KEYS: PageContextKey[] = [
  ...DASHBOARD_TABS.map((v) => `dashboard:${v}` as const),
  ...HOST_VIEWS.map((v) => `dashboard:${v}` as const),
  ...STACK_TABS.map((v) => `stack:${v}` as const),
  ...SETTINGS_SECTION_IDS.map((v) => `settings:${v}` as const),
]

/** Why a ref does not resolve against the bundled docs, or null if it does. */
function unresolved(ref: HelpRef, docs: HelpDoc[]): string | null {
  const doc = docs.find((d) => d.id === ref.doc)
  if (!doc) return `no bundled doc "${ref.doc}"`
  if (ref.anchor !== undefined && !doc.sections.some((s) => s.anchor === ref.anchor)) {
    return `no heading with anchor "#${ref.anchor}" in ${ref.doc}`
  }
  return null
}

describe('help coverage (a): every view and field resolves to a real heading', () => {
  it('every exported view has a PAGE_HELP entry', () => {
    const missing = VIEW_KEYS.filter((key) => !(key in PAGE_HELP))
    expect(missing).toEqual([])
    expect(VIEW_KEYS).toHaveLength(26)
  })

  it('every PAGE_HELP ref names a bundled doc and heading', async () => {
    const docs = await loadHelpLibrary()
    const broken = Object.entries(PAGE_HELP).flatMap(([key, ref]) => {
      const why = unresolved(ref, docs)
      return why ? [`${key}: ${why}`] : []
    })
    expect(broken).toEqual([])
  })

  it('every FIELD_HELP ref names a bundled doc and heading', async () => {
    const docs = await loadHelpLibrary()
    const broken = Object.entries(FIELD_HELP).flatMap(([id, ref]) => {
      const why = unresolved(ref, docs)
      return why ? [`${id}: ${why}`] : []
    })
    expect(broken).toEqual([])
    expect(Object.keys(FIELD_HELP)).toHaveLength(14)
  })
})

/**
 * Every `<TabsContent value="x">` literal in a page's source. Also counts the
 * `<TabsContent` openings, so a value the regex cannot read (an expression,
 * a different quote) fails the count instead of passing unseen.
 */
function tabsContentValues(source: string): { values: string[]; openings: number } {
  const values = [...source.matchAll(/<TabsContent\s+value="([^"]+)"/g)].map((m) => m[1])
  const openings = source.match(/<TabsContent\b/g)?.length ?? 0
  return { values, openings }
}

describe('help coverage (b): every rendered tab panel is in the exported arrays', () => {
  it.each([
    ['DashboardPage.tsx', dashboardSource, [...DASHBOARD_TABS, ...HOST_VIEWS] as readonly string[], 10],
    ['StackDetail.tsx', stackDetailSource, STACK_TABS as readonly string[], 6],
  ])('%s', (_file, source, exported, atLeast) => {
    const { values, openings } = tabsContentValues(source)
    expect(values.length).toBe(openings)
    expect(values.length).toBeGreaterThanOrEqual(atLeast)
    expect(values.filter((v) => !exported.includes(v))).toEqual([])
  })
})

describe('help coverage (c): anchors are unique within each doc', () => {
  it('no bundled doc has two H2/H3 headings with the same slug', async () => {
    const docs = await loadHelpLibrary()
    const duplicates = docs.flatMap((doc) => {
      const seen = new Set<string>()
      return doc.sections.flatMap((s) => {
        if (seen.has(s.anchor)) return [`${doc.id}#${s.anchor}`]
        seen.add(s.anchor)
        return []
      })
    })
    expect(duplicates).toEqual([])
  })
})
