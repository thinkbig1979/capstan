/**
 * Guards for the env-lock masking class (agent-os-uzgo, follow-ups of
 * agent-os-oeqf, wm7t, kxag). Each check scans the real sources and has a
 * planted violation, made IN MEMORY (nothing is written to disk), that it must
 * flag, so a green run cannot be a scanner that matches nothing.
 *
 * 1. Rows built from a payload feed rememberMasked. In every file that uses
 *    useEnvUnlockRemask, a function that builds rows with `_rowId: nextRowId()`
 *    from a spread payload row must call rememberMasked, or the lock's remask
 *    cannot see a row the server flagged that the user revealed (oeqf). The
 *    scope is the nearest function bound to a name (a const or a function
 *    declaration), not the `.map` callback and not the whole component. A
 *    literal with no spread (Add Entry's blank row) is not from a payload.
 * 2. Display gates read the locked-now helper. Every file that READS a query
 *    the backend redacts for a locked session (useGitDiff, useGitChangesDiff,
 *    useGlobalEnv, or a useQuery keyed on the stack env or compose file) calls
 *    useEnvLockedNow: after a lock the payload on screen is still the unlocked
 *    one until the lock's refetch lands, and for good if it fails (wm7t, xz6h,
 *    f3am, npxh). The hooks that define those queries are the producers, named
 *    in PRODUCERS with the reason. Git status, log and change-list queries are
 *    not masked by the server (only diffs are, git.go GetChangeDiff/GetDiff),
 *    so they are not in the set.
 * 3. Env-row search lists read fields only through `searchable`
 *    (components/stack/env-editor/envSearch.ts). Every useTextFilter call in a
 *    file that names an env row type takes accessors whose body is a
 *    `searchable(...)` call; a list that cannot be followed is a hit.
 *
 * BLIND SPOTS: syntax only. Check 1 does not prove the ids passed are the
 * right ones; check 2 proves the helper is called, not that its value gates
 * the render; check 3 proves the helper is called, not that its `hidden`
 * argument is right. A query read through a renamed import or a helper of its
 * own is not seen by check 2.
 */
import { describe, it, expect } from 'vitest'
import ts from 'typescript'

const sources = import.meta.glob<string>('/src/**/*.{ts,tsx}', { query: '?raw', import: 'default', eager: true })
const production = Object.entries(sources).filter(
  ([path]) => !path.includes('/__tests__/') && !/\.test\.tsx?$/.test(path) && !path.startsWith('/src/test/'),
)

function parse(path: string, text: string) {
  return ts.createSourceFile(path, text, ts.ScriptTarget.Latest, true, path.endsWith('.tsx') ? ts.ScriptKind.TSX : ts.ScriptKind.TS)
}

function walk(node: ts.Node, visit: (n: ts.Node) => void) {
  visit(node)
  node.forEachChild((c) => walk(c, visit))
}

const calleeName = (call: ts.CallExpression) =>
  ts.isIdentifier(call.expression) ? call.expression.text : ts.isPropertyAccessExpression(call.expression) ? call.expression.name.text : ''

function callsTo(node: ts.Node, name: string): number {
  let n = 0
  walk(node, (c) => {
    if (ts.isCallExpression(c) && calleeName(c) === name) n++
  })
  return n
}

// ---- 1. payload rows feed rememberMasked -----------------------------------

/** The nearest function bound to a name: a const initializer or a declaration. */
function namedScope(node: ts.Node): ts.Node | undefined {
  for (let p = node.parent; p; p = p.parent) {
    if (ts.isFunctionDeclaration(p)) return p
    if ((ts.isArrowFunction(p) || ts.isFunctionExpression(p)) && ts.isVariableDeclaration(p.parent)) return p
  }
  return undefined
}

function payloadRowSites(path: string, text: string) {
  const sf = parse(path, text)
  const sites: Array<{ line: number; fed: boolean }> = []
  walk(sf, (n) => {
    if (!ts.isObjectLiteralExpression(n)) return
    const rowId = n.properties.find(
      (p) =>
        ts.isPropertyAssignment(p) &&
        p.name.getText(sf) === '_rowId' &&
        ts.isCallExpression(p.initializer) &&
        calleeName(p.initializer) === 'nextRowId',
    )
    if (!rowId || !n.properties.some(ts.isSpreadAssignment)) return
    const scope = namedScope(n)
    sites.push({
      line: sf.getLineAndCharacterOfPosition(n.getStart(sf)).line + 1,
      fed: scope !== undefined && callsTo(scope, 'rememberMasked') > 0,
    })
  })
  return sites
}

const remaskFiles = () => production.filter(([, text]) => /\buseEnvUnlockRemask\(/.test(text) && !/export function useEnvUnlockRemask/.test(text))

describe('guard 1: payload rows feed rememberMasked (agent-os-uzgo)', () => {
  it('every payload row site in a remask file is fed', () => {
    const files = remaskFiles()
    expect(files.map(([p]) => p)).toEqual(['/src/components/stack/EnvEditor.tsx'])
    const sites = files.flatMap(([p, t]) => payloadRowSites(p, t).map((s) => ({ p, ...s })))
    expect(sites.length).toBeGreaterThan(0) // the scanner found the hydrate site
    expect(sites.filter((s) => !s.fed)).toEqual([])
  })

  it('flags a planted hydrate that no longer calls rememberMasked', () => {
    const [[path, text]] = remaskFiles()
    const planted = text.replace(/\n\s*rememberMasked\(rows\.filter[^\n]*/, '\n')
    expect(planted).not.toBe(text)
    expect(payloadRowSites(path, planted).filter((s) => !s.fed).length).toBeGreaterThan(0)
  })
})

// ---- 2. display gates read the locked-now helper ---------------------------

const GATED_HOOKS = new Set(['useGitDiff', 'useGitChangesDiff', 'useGlobalEnv'])
const GATED_KEY = /^queryKeys\.(stack\.(env|compose)|settings\.globalEnv|git\.diff)\(/

/** Files that define the gated queries: they hand the payload on, they show nothing. */
const PRODUCERS: Record<string, string> = {
  '/src/hooks/useGit.ts': 'defines useGitDiff and useGitChangesDiff; stamps servedUnlocked for the views',
  '/src/hooks/useResources.ts': 'defines useGlobalEnv',
}

function readsGatedQuery(path: string, text: string): boolean {
  const sf = parse(path, text)
  let hit = false
  walk(sf, (n) => {
    if (!ts.isCallExpression(n)) return
    const name = calleeName(n)
    if (GATED_HOOKS.has(name)) hit = true
    if (name === 'useQuery' && n.arguments[0] && ts.isObjectLiteralExpression(n.arguments[0])) {
      for (const p of n.arguments[0].properties) {
        if (ts.isPropertyAssignment(p) && p.name.getText(sf) === 'queryKey' && GATED_KEY.test(p.initializer.getText(sf))) hit = true
      }
    }
  })
  return hit
}

const gatedReaders = () => production.filter(([p, t]) => !(p in PRODUCERS) && readsGatedQuery(p, t))

describe('guard 2: display gates read useEnvLockedNow (agent-os-uzgo)', () => {
  it('every reader of a redacted query calls the helper', () => {
    const readers = gatedReaders()
    expect(readers.map(([p]) => p).sort()).toEqual([
      '/src/components/git/CommitPushDialog.tsx',
      '/src/components/git/DiffViewer.tsx',
      '/src/components/settings/global-env/useGlobalEnvVars.ts',
      '/src/components/stack/ComposeEditor.tsx',
      '/src/components/stack/EnvEditor.tsx',
    ])
    expect(readers.filter(([p, t]) => callsTo(parse(p, t), 'useEnvLockedNow') === 0).map(([p]) => p)).toEqual([])
  })

  it('every producer entry still defines a gated query', () => {
    for (const path of Object.keys(PRODUCERS)) {
      expect(readsGatedQuery(path, sources[path])).toBe(true)
    }
  })

  it('flags a planted reader that dropped the helper', () => {
    const path = '/src/components/git/DiffViewer.tsx'
    const planted = sources[path].replace(/useEnvLockedNow\(\)/g, 'false')
    expect(readsGatedQuery(path, planted)).toBe(true)
    expect(callsTo(parse(path, planted), 'useEnvLockedNow')).toBe(0)
  })
})

// ---- 3. env-row search lists go through `searchable` -----------------------

const ENV_ROW_TYPE = /\b(IndexedEnvEntry|IndexedEnvVar|EnvEntryRow|EnvRow|EnvVar)\b/

/** The accessor array a useTextFilter call takes, followed through a const and a useMemo. */
function accessorArray(sf: ts.SourceFile, arg: ts.Expression): ts.ArrayLiteralExpression | undefined {
  if (ts.isArrayLiteralExpression(arg)) return arg
  if (ts.isCallExpression(arg) && calleeName(arg) === 'useMemo') {
    const fn = arg.arguments[0]
    if (fn && ts.isArrowFunction(fn)) {
      const body = ts.isParenthesizedExpression(fn.body) ? fn.body.expression : fn.body
      if (ts.isArrayLiteralExpression(body)) return body
    }
    return undefined
  }
  if (ts.isIdentifier(arg)) {
    let init: ts.Expression | undefined
    walk(sf, (n) => {
      if (ts.isVariableDeclaration(n) && ts.isIdentifier(n.name) && n.name.text === arg.text && n.initializer) init = n.initializer
    })
    return init ? accessorArray(sf, init) : undefined
  }
  return undefined
}

function searchListHits(path: string, text: string) {
  const sf = parse(path, text)
  const lists: Array<{ line: number; ok: boolean }> = []
  walk(sf, (n) => {
    if (!ts.isCallExpression(n) || calleeName(n) !== 'useTextFilter' || !n.arguments[1]) return
    const arr = accessorArray(sf, n.arguments[1])
    const ok =
      arr !== undefined &&
      arr.elements.length > 0 &&
      arr.elements.every(
        (el) => ts.isArrowFunction(el) && ts.isCallExpression(el.body) && calleeName(el.body) === 'searchable',
      )
    lists.push({ line: sf.getLineAndCharacterOfPosition(n.getStart(sf)).line + 1, ok })
  })
  return lists
}

const envSearchFiles = () =>
  production.filter(([, t]) => /\buseTextFilter\(/.test(t) && ENV_ROW_TYPE.test(t) && !/export function useTextFilter/.test(t))

describe('guard 3: env-row search reads through searchable (agent-os-uzgo)', () => {
  it('every env-row search list uses the helper for every field', () => {
    const files = envSearchFiles()
    expect(files.map(([p]) => p).sort()).toEqual([
      '/src/components/settings/global-env/useGlobalEnvVars.ts',
      '/src/components/stack/env-editor/EnvTableView.tsx',
    ])
    const lists = files.flatMap(([p, t]) => searchListHits(p, t).map((l) => ({ p, ...l })))
    expect(lists).toHaveLength(2)
    expect(lists.filter((l) => !l.ok)).toEqual([])
  })

  it('flags a planted raw value accessor', () => {
    const path = '/src/components/stack/env-editor/EnvTableView.tsx'
    const planted = sources[path].replace('searchable(e.value, e.sensitive)', 'e.value')
    expect(planted).not.toBe(sources[path])
    expect(searchListHits(path, planted).filter((l) => !l.ok)).toHaveLength(1)
  })
})
