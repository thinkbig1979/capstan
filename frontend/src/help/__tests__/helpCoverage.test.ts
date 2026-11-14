import { describe, expect, it } from 'vitest'
import appSource from '@/App.tsx?raw'
import dashboardSource from '@/pages/DashboardPage.tsx?raw'
import hostTabSource from '@/components/host/HostTab.tsx?raw'
import stackDetailSource from '@/components/stack/StackDetail.tsx?raw'
import { DASHBOARD_TABS, HOST_VIEWS, SETTINGS_SECTION_IDS, STACK_TABS } from '@/lib/app-views'
import { loadHelpLibrary, type HelpDoc } from '../content'
import { FIELD_HELP, PAGE_HELP, type HelpRef, type PageContextKey } from '../registry'

// Every view the app renders, keyed as the registry keys it.
const VIEW_KEYS: PageContextKey[] = [
  // The Host tab's topics are the host: keys below, not a dashboard: key.
  ...DASHBOARD_TABS.filter((v) => v !== 'host').map((v) => `dashboard:${v}` as const),
  'host:overview',
  ...HOST_VIEWS.map((v) => `host:${v}` as const),
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
    expect(Object.keys(FIELD_HELP)).toHaveLength(16)
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
    ['DashboardPage.tsx', dashboardSource, DASHBOARD_TABS as readonly string[], 5],
    ['StackDetail.tsx', stackDetailSource, STACK_TABS as readonly string[], 6],
  ])('%s', (_file, source, exported, atLeast) => {
    const { values, openings } = tabsContentValues(source)
    expect(values.length).toBe(openings)
    expect(values.length).toBeGreaterThanOrEqual(atLeast)
    expect(values.filter((v) => !exported.includes(v))).toEqual([])
  })
})

// The Host tab's lists are not TabsContent panels: the consumer arm reads the
// views HostTab switches on, so a sixth list fails here (agent-os-mi4r).
describe('help coverage (b2): the Host tab renders exactly the exported views', () => {
  it('HostTab.tsx switches on every HOST_VIEWS value and nothing else', () => {
    const cases = [...hostTabSource.matchAll(/case '([^']+)':/g)].map((m) => m[1])
    expect(cases.length).toBe(hostTabSource.match(/\bcase\b/g)?.length ?? 0)
    expect([...cases].sort()).toEqual([...HOST_VIEWS].sort())
  })

  it('App.tsx registers no /host route: the Host tab lives at /?tab=host', () => {
    expect(appSource).not.toMatch(/path="\/host/)
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
