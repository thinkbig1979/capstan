/**
 * Guard for PruneButton's stack check (agent-os-6kxx).
 *
 * Docker's prune removes what is "unused" by its own rule, which covers a
 * stopped or removed stack's volumes, networks, images and containers. The
 * stackImpact prop (agent-os-8d8a, agent-os-tt94) makes the confirm dialog name
 * those stacks and ask for a typed word. It is opt-in, so a new prune route can
 * skip it silently. This guard makes skipping it a visible edit to this file:
 *  - every `<PruneButton` passes `stackImpact`, or its file is in
 *    NO_STACK_IMPACT below with the reason no stack can lose anything to it.
 *    An entry that no longer holds such a site is itself a failure, so the list
 *    cannot go stale;
 *  - a resource prune call (resourcesApi.prune*) sits in a PruneButton's
 *    `pruneFn`, never anywhere else, so a prune cannot route around the dialog;
 *  - PruneButton is not imported under another name or re-exported, and takes
 *    no spread props: those shapes cannot be followed, so they are hits.
 *
 * BLIND SPOTS, stated so a green run is not read as more than it is:
 *  - Syntax only. A prune reached through an alias of resourcesApi, or through
 *    a hook that wraps it, is not seen. The host page's cleanup run
 *    (resourcesApi.runCleanup) is not a resource prune: it removes only
 *    candidates the server already filtered by stack references.
 *  - Test files and lib/api.ts (the producer) are not scanned.
 */
import { describe, it, expect } from 'vitest'
import ts from 'typescript'

// Files whose PruneButton passes no stackImpact, each with the reason no stack
// can lose anything to that prune.
const NO_STACK_IMPACT: Record<string, string> = {
  '/src/components/dashboard/BuildCacheTab.tsx':
    'build cache belongs to no stack: no compose project label or compose file names a cache record, and losing one costs only a slower next build',
}

// The census on the tree this guard was written against (agent-os-6kxx):
// volumes, networks, images, containers with the check; build cache without.
// Pinned so a scan that silently matches nothing cannot pass.
const PINNED_SITES = [
  '/src/components/dashboard/BuildCacheTab.tsx',
  '/src/components/dashboard/ContainersOverviewTab.tsx',
  '/src/components/dashboard/ImagesTab.tsx',
  '/src/components/dashboard/NetworksTab.tsx',
  '/src/components/dashboard/VolumesTab.tsx',
]

const PRUNE_CALL = /^prune[A-Z]/

const sources = import.meta.glob<string>('/src/**/*.{ts,tsx}', { query: '?raw', import: 'default', eager: true })

const scanned = (path: string) =>
  !path.includes('/__tests__/') && !/\.test\.tsx?$/.test(path) && path !== '/src/lib/api.ts'

interface Scan {
  sites: { path: string; stackImpact: boolean }[]
  hits: string[]
}

function scan(files: Record<string, string>): Scan {
  const out: Scan = { sites: [], hits: [] }
  for (const [path, text] of Object.entries(files)) {
    if (!scanned(path)) continue
    const sf = ts.createSourceFile(path, text, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX)
    const at = (n: ts.Node) => `${path}:${sf.getLineAndCharacterOfPosition(n.getStart(sf)).line + 1}`

    const visit = (n: ts.Node) => {
      if (ts.isImportSpecifier(n) && (n.propertyName ?? n.name).text === 'PruneButton' && n.propertyName) {
        out.hits.push(`${at(n)} imports PruneButton under another name`)
      }
      if (ts.isExportSpecifier(n) && (n.propertyName ?? n.name).text === 'PruneButton') {
        out.hits.push(`${at(n)} re-exports PruneButton`)
      }
      if ((ts.isJsxSelfClosingElement(n) || ts.isJsxOpeningElement(n)) && n.tagName.getText(sf) === 'PruneButton') {
        const props = n.attributes.properties
        if (props.some(ts.isJsxSpreadAttribute)) out.hits.push(`${at(n)} passes PruneButton spread props`)
        const stackImpact = props.some((p) => ts.isJsxAttribute(p) && p.name.getText(sf) === 'stackImpact')
        out.sites.push({ path, stackImpact })
        if (!stackImpact && !NO_STACK_IMPACT[path]) {
          out.hits.push(`${at(n)} <PruneButton> passes no stackImpact and its file is not in NO_STACK_IMPACT`)
        }
      }
      if (
        ts.isPropertyAccessExpression(n) &&
        n.expression.getText(sf) === 'resourcesApi' &&
        PRUNE_CALL.test(n.name.text) &&
        !inPruneButtonPruneFn(n, sf)
      ) {
        out.hits.push(`${at(n)} calls resourcesApi.${n.name.text} outside a PruneButton's pruneFn`)
      }
      ts.forEachChild(n, visit)
    }
    visit(sf)
  }
  return out
}

function inPruneButtonPruneFn(n: ts.Node, sf: ts.SourceFile): boolean {
  for (let p: ts.Node | undefined = n.parent; p; p = p.parent) {
    if (ts.isJsxAttribute(p)) {
      const el = p.parent.parent
      return (
        p.name.getText(sf) === 'pruneFn' &&
        (ts.isJsxSelfClosingElement(el) || ts.isJsxOpeningElement(el)) &&
        el.tagName.getText(sf) === 'PruneButton'
      )
    }
  }
  return false
}

describe('every PruneButton names the stacks its prune takes (agent-os-6kxx)', () => {
  const result = scan(sources)

  it('finds the pinned census', () => {
    expect([...new Set(result.sites.map((s) => s.path))].sort()).toEqual(PINNED_SITES)
  })

  it('has no PruneButton without stackImpact or an allowlist reason, and no prune outside one', () => {
    expect(result.hits).toEqual([])
  })

  it('has no stale NO_STACK_IMPACT entry', () => {
    const stale = Object.keys(NO_STACK_IMPACT).filter(
      (path) => !result.sites.some((s) => s.path === path && !s.stackImpact),
    )
    expect(stale).toEqual([])
  })
})

// Positive controls: each planted shape must be flagged, and the compliant one
// must not, so a green run above is not the scan matching nothing.
describe('the guard flags what it exists to catch', () => {
  const plant = (text: string) => scan({ '/src/components/dashboard/Planted.tsx': text }).hits

  it.each([
    ['a PruneButton with no stackImpact', `<PruneButton resourceType="x" pruneFn={(o) => resourcesApi.pruneImages(o)} />`],
    ['an aliased import', `import { PruneButton as PB } from '@/components/dashboard/PruneButton'`],
    ['a re-export', `export { PruneButton } from '@/components/dashboard/PruneButton'`],
    ['spread props', `<PruneButton {...props} stackImpact={impact} />`],
    ['a prune outside PruneButton', `useMutation({ mutationFn: () => resourcesApi.pruneContainers() })`],
  ])('%s', (_name, text) => {
    expect(plant(text)).not.toEqual([])
  })

  it('passes a PruneButton that names its stacks', () => {
    expect(
      plant(`<PruneButton resourceType="x" pruneFn={(o) => resourcesApi.pruneImages(o)} stackImpact={impact} />`),
    ).toEqual([])
  })
})
