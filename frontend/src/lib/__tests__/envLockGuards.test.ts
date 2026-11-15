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
 *    A queryKey held in a variable is followed to its initializer (agent-os-uttl
 *    item 1).
 *    Every useTextFilter caller is held to the rule unless NON_ENV names it
 *    with what it lists (agent-os-uttl item 2).
 * 4. Env-row key inputs: a JSX value reading `.key` is
 *    `<keyHidden> ? '' : X.key` (agent-os-uttl item 3).
 * 5. Every gitApi diff read sits inside stampServed, and every other gitApi
 *    method is named in UNMASKED_GIT (agent-os-uttl item 5).
 * 6. Reveal identity: global-env reveal state is indexed by `_rowId`, EnvEditor's
 *    hydrate carries reveals by rowSlot, and every value input that can be a
 *    password field takes valuePlaceholder (agent-os-uttl item 7).
 *
 * BLIND SPOTS: syntax only. Check 1 does not prove the ids passed are the
 * right ones. Check 2 proves the helper is CALLED, not that its value gates
 * the render (agent-os-uttl item 6, kept as a stated blind spot): a mutant
 * `const stale = false && lockedNow` stays green here, so what the value does
 * is pinned by each view's own lock tests (DiffViewer.npxh-after-lock.test.tsx
 * and the editors' f3am/9wp3 tests), not by this file. Check 3 proves the
 * helper is called, not that its `hidden` argument is right. A query read
 * through a renamed import or a helper of its own is not seen by check 2, nor a
 * key built inside a function. Check 4 does not see a key shown through
 * anything but a JSX value attribute. Check 6's placeholder rule keys on a
 * `type` attribute naming 'password' and a value read as `X.value`.
 * Also not seen, each OBSERVED green with the violation present (review of
 * run/envfe, rv-envfe F3): check 2, a queryKey reached through two consts
 * (`heldBy` follows one hop) or passed in as a parameter; check 3, an env-row
 * search written as a raw `.filter` with no useTextFilter; check 4, a key shown
 * through a helper call (`value={keyText(entry)}`); check 5, a diff read through
 * an alias of gitApi (`const api = gitApi; api.diff(...)`); check 6, reveal state
 * read through an alias of `visible` (`const v = visible; v[index]`).
 */
import { describe, it, expect } from 'vitest'
import ts from 'typescript'
import { readSourceFiles } from '@/test/sourceFiles'

// Read from disk at run time, not import.meta.glob: see src/test/sourceFiles.ts (agent-os-3j4i).
const sources = readSourceFiles()
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

/**
 * What an expression can hold: an identifier is followed to the initializer of
 * every variable of that name in the file (agent-os-uttl item 1), so
 * `const key = queryKeys.stack.env(id); useQuery({ queryKey: key })` is read as
 * the key. Any same-named declaration that matches counts, which over-reports
 * rather than under-reports.
 */
function heldBy(sf: ts.SourceFile, expr: ts.Expression): ts.Expression[] {
  if (!ts.isIdentifier(expr)) return [expr]
  const inits: ts.Expression[] = []
  walk(sf, (n) => {
    if (ts.isVariableDeclaration(n) && ts.isIdentifier(n.name) && n.name.text === expr.text && n.initializer) inits.push(n.initializer)
  })
  return [expr, ...inits]
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
        if (p.name?.getText(sf) !== 'queryKey') continue
        const key = ts.isPropertyAssignment(p) ? p.initializer : ts.isShorthandPropertyAssignment(p) ? p.name : undefined
        if (key && heldBy(sf, key).some((e) => GATED_KEY.test(e.getText(sf)))) hit = true
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

  it.each([
    ['a key held in a const', `const key = queryKeys.stack.env(id)\nexport function Zz() { return useQuery({ queryKey: key, queryFn }) }`],
    ['a shorthand queryKey', `export function Zz() { const queryKey = queryKeys.settings.globalEnv(); return useQuery({ queryKey, queryFn }) }`],
  ])('flags a planted reader whose redacted key is %s (agent-os-uttl item 1)', (_name, planted) => {
    expect(readsGatedQuery('/src/components/Zz.tsx', planted)).toBe(true)
  })

  it('control: a const key that is not redacted is not a reader', () => {
    const src = `const key = queryKeys.git.status(id)\nexport function Zz() { return useQuery({ queryKey: key, queryFn }) }`
    expect(readsGatedQuery('/src/components/Zz.tsx', src)).toBe(false)
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

/**
 * Files whose useTextFilter lists hold no env row, each with what they list
 * (agent-os-uttl item 2). Every other useTextFilter caller is held to the
 * `searchable` rule, so a new caller fails until it is classified: picking env
 * files by a type name let a list of inline-typed env rows pass.
 */
const NON_ENV: Record<string, string> = {
  '/src/components/dashboard/StacksTab.tsx': 'stacks: project name and status',
  '/src/components/dashboard/ContainersOverviewTab.tsx': 'containers: name, project, image',
  '/src/components/dashboard/ImagesTab.tsx': 'images: tags and id',
  '/src/components/dashboard/VolumesTab.tsx': 'volumes: name, driver, stack',
  '/src/components/dashboard/updates-tab/useUpdatesData.ts': 'image updates: container, image ref, project',
  '/src/components/dashboard/NetworksTab.tsx': 'networks: name, driver, scope, stack',
  '/src/components/dashboard/BackupHistoryTab.tsx': 'backup runs: id, kind, trigger, status',
  '/src/components/dashboard/UpdateLogTab.tsx': 'update history: container, stack, image, status',
  '/src/components/dashboard/BuildCacheTab.tsx': 'build cache entries: id, type, description',
  '/src/components/stack/ContainerList.tsx': 'a stack\'s containers: name, image, state, status',
  '/src/components/stack/BackupsTab.tsx': 'backup snapshots and runs: ids, times, tags, kind, trigger',
  '/src/components/stack/StackUpdatesTab.tsx': 'update history: container, image refs',
  '/src/components/settings/DirectoriesSettingsContent.tsx': 'stack directories: path and folder name',
}

const callsTextFilter = (t: string) => /\buseTextFilter\(/.test(t) && !/export function useTextFilter/.test(t)

/** The search lists held to the `searchable` rule: every useTextFilter call outside NON_ENV. */
function envSearchLists(files: Array<[string, string]>) {
  return files
    .filter(([p, t]) => callsTextFilter(t) && !(p in NON_ENV))
    .flatMap(([p, t]) => searchListHits(p, t).map((l) => ({ p, ...l })))
}

describe('guard 3: env-row search reads through searchable (agent-os-uzgo)', () => {
  it('every env-row search list uses the helper for every field', () => {
    const lists = envSearchLists(production)
    expect([...new Set(lists.map((l) => l.p))].sort()).toEqual([
      '/src/components/settings/global-env/useGlobalEnvVars.ts',
      '/src/components/stack/env-editor/EnvTableView.tsx',
    ])
    expect(lists).toHaveLength(2)
    expect(lists.filter((l) => !l.ok)).toEqual([])
  })

  it('every NON_ENV entry still calls useTextFilter, and still names no env row type', () => {
    for (const path of Object.keys(NON_ENV)) {
      expect(callsTextFilter(sources[path] ?? ''), path).toBe(true)
      expect(ENV_ROW_TYPE.test(sources[path]), path).toBe(false)
    }
  })

  it('flags a planted search over inline-typed env rows in a new file (agent-os-uttl item 2)', () => {
    const planted = `export function Zz(rows: Array<{ key: string; value: string }>) {\n  return useTextFilter(rows, [(r) => r.key, (r) => r.value])\n}`
    expect(ENV_ROW_TYPE.test(planted)).toBe(false) // the old file pick could not see it
    expect(envSearchLists([...production, ['/src/components/Zz.tsx', planted]]).filter((l) => !l.ok)).toEqual([
      { p: '/src/components/Zz.tsx', line: 2, ok: false },
    ])
  })

  it('flags a planted raw value accessor', () => {
    const path = '/src/components/stack/env-editor/EnvTableView.tsx'
    const planted = sources[path].replace('searchable(e.value, e.sensitive)', 'e.value')
    expect(planted).not.toBe(sources[path])
    expect(searchListHits(path, planted).filter((l) => !l.ok)).toHaveLength(1)
  })
})

// ---- shared JSX helpers ----------------------------------------------------

const strip = (e: ts.Expression): ts.Expression => (ts.isParenthesizedExpression(e) ? strip(e.expression) : e)

function attr(sf: ts.SourceFile, el: ts.JsxOpeningLikeElement, name: string): ts.JsxAttribute | undefined {
  return el.attributes.properties.find((a): a is ts.JsxAttribute => ts.isJsxAttribute(a) && a.name.getText(sf) === name)
}

const attrExpr = (a: ts.JsxAttribute | undefined) =>
  a?.initializer && ts.isJsxExpression(a.initializer) && a.initializer.expression ? strip(a.initializer.expression) : undefined

const lineOf = (sf: ts.SourceFile, n: ts.Node) => sf.getLineAndCharacterOfPosition(n.getStart(sf)).line + 1

const readsProperty = (node: ts.Node, name: string) => {
  let hit = false
  walk(node, (n) => {
    if (ts.isPropertyAccessExpression(n) && n.name.text === name) hit = true
  })
  return hit
}

const tsxProduction = () => production.filter(([p]) => p.endsWith('.tsx'))

// ---- 4. env-row key inputs follow the hidden-key rule (agent-os-uttl item 3) ----

/**
 * Every JSX `value`/`defaultValue` that reads a `.key` property is
 * `<hidden> ? '' : X.key`, where <hidden> is `X.keyHidden` or a `keyHidden(...)`
 * call: a key the lock blanked (a PEM body line read as a key, agent-os-f3am)
 * must not be shown in its input.
 */
function keyInputSites(path: string, text: string) {
  const sf = parse(path, text)
  const sites: Array<{ p: string; line: number; ok: boolean }> = []
  walk(sf, (n) => {
    if (!ts.isJsxAttribute(n) || !['value', 'defaultValue'].includes(n.name.getText(sf))) return
    const e = attrExpr(n)
    if (!e || !readsProperty(e, 'key')) return
    const cond = ts.isConditionalExpression(e) ? strip(e.condition) : undefined
    const ok =
      ts.isConditionalExpression(e) &&
      cond !== undefined &&
      ((ts.isPropertyAccessExpression(cond) && cond.name.text === 'keyHidden') ||
        (ts.isCallExpression(cond) && calleeName(cond) === 'keyHidden')) &&
      ts.isStringLiteral(strip(e.whenTrue)) &&
      (strip(e.whenTrue) as ts.StringLiteral).text === '' &&
      ts.isPropertyAccessExpression(strip(e.whenFalse)) &&
      (strip(e.whenFalse) as ts.PropertyAccessExpression).name.text === 'key'
    sites.push({ p: path, line: lineOf(sf, n), ok })
  })
  return sites
}

describe('guard 4: env-row key inputs hide a key the lock blanked (agent-os-uttl item 3)', () => {
  it('every input value that reads a .key follows the hidden-key rule', () => {
    const sites = tsxProduction().flatMap(([p, t]) => keyInputSites(p, t))
    expect([...new Set(sites.map((s) => s.p))].sort()).toEqual([
      '/src/components/settings/global-env/GlobalEnvCards.tsx',
      '/src/components/settings/global-env/GlobalEnvTable.tsx',
      '/src/components/stack/env-editor/EnvTableView.tsx',
    ])
    expect(sites).toHaveLength(4)
    expect(sites.filter((s) => !s.ok)).toEqual([])
  })

  it('flags a planted key input that shows the key raw', () => {
    const path = '/src/components/settings/global-env/GlobalEnvTable.tsx'
    const planted = sources[path].replace("value={entry.keyHidden ? '' : entry.key}", 'value={entry.key}')
    expect(planted).not.toBe(sources[path])
    expect(keyInputSites(path, planted).filter((s) => !s.ok)).toHaveLength(1)
  })

  it('control: a value keyed by a variable named key is not a key input', () => {
    expect(keyInputSites('/f.tsx', 'const a = <Input value={draft[key]} />; const b = <Input value={valueOf(key)} />')).toEqual([])
  })
})

// ---- 5. git diff queries stamp servedUnlocked (agent-os-uttl item 5) --------

/** A gitApi method whose payload the server masks for a locked session (git.go GetDiff, GetChangeDiff). */
const DIFF_METHOD = /diff/i
/** The other gitApi methods, each with why its payload holds no env value. */
const UNMASKED_GIT: Record<string, string> = {
  status: 'branch and file status, no file content',
  pull: 'a pull result: refs and output, no diff',
  log: 'commit metadata, no file content',
  changes: 'the list of changed paths, no content',
  commit: 'a commit request and its hash',
  push: 'a push result',
}

function gitApiMethods(): string[] {
  const sf = parse('/src/lib/api.ts', sources['/src/lib/api.ts'])
  const names: string[] = []
  walk(sf, (n) => {
    if (!ts.isVariableDeclaration(n) || n.name.getText(sf) !== 'gitApi' || !n.initializer || !ts.isObjectLiteralExpression(n.initializer)) return
    for (const p of n.initializer.properties) if (p.name) names.push(p.name.getText(sf))
  })
  return names
}

/** Every `gitApi.<diff method>` reference, and whether it sits inside a stampServed(...) call. */
function diffReadSites(path: string, text: string) {
  const sf = parse(path, text)
  const sites: Array<{ p: string; line: number; ok: boolean }> = []
  walk(sf, (n) => {
    if (!ts.isPropertyAccessExpression(n) || !ts.isIdentifier(n.expression) || n.expression.text !== 'gitApi') return
    if (!DIFF_METHOD.test(n.name.text)) return
    let ok = false
    for (let p = n.parent; p; p = p.parent) if (ts.isCallExpression(p) && calleeName(p) === 'stampServed') ok = true
    sites.push({ p: path, line: lineOf(sf, n), ok })
  })
  return sites
}

describe('guard 5: git diff reads go through stampServed (agent-os-uttl item 5)', () => {
  it('every gitApi method is a diff or is named in UNMASKED_GIT, and every entry still exists', () => {
    const methods = gitApiMethods()
    expect(methods.length).toBeGreaterThan(0)
    expect(methods.filter((m) => !DIFF_METHOD.test(m) && !(m in UNMASKED_GIT))).toEqual([])
    expect(Object.keys(UNMASKED_GIT).filter((m) => !methods.includes(m))).toEqual([])
  })

  it('every diff read in src is stamped', () => {
    const sites = production.flatMap(([p, t]) => diffReadSites(p, t))
    expect(sites.map((s) => s.p)).toEqual(['/src/hooks/useGit.ts', '/src/hooks/useGit.ts'])
    expect(sites.filter((s) => !s.ok)).toEqual([])
  })

  it('flags a planted diff query that skips the stamp', () => {
    const path = '/src/hooks/useGit.ts'
    const planted = sources[path].replace('stampServed(() => gitApi.diff(stackId, hash))', 'gitApi.diff(stackId, hash)')
    expect(planted).not.toBe(sources[path])
    expect(diffReadSites(path, planted).filter((s) => !s.ok)).toHaveLength(1)
  })
})

// ---- 6. reveal identity and the hidden-value placeholder (agent-os-uttl item 7) ----

const GLOBAL_ENV_DIR = '/src/components/settings/global-env/'
const ROW_ID_NAMES = new Set(['rowId', 'pendingRevealId'])
const isRowId = (e: ts.Expression) =>
  (ts.isPropertyAccessExpression(e) && e.name.text === '_rowId') || (ts.isIdentifier(e) && ROW_ID_NAMES.has(e.text))

/**
 * In global-env, reveal state is indexed by the row's `_rowId` only, never by its
 * position (agent-os-0b4m): `visible[...]`, and every element access or computed
 * key inside a setVisible(...) call.
 */
function revealIndexSites(path: string, text: string) {
  const sf = parse(path, text)
  const sites: Array<{ p: string; line: number; ok: boolean }> = []
  const check = (n: ts.Node, arg: ts.Expression) => sites.push({ p: path, line: lineOf(sf, n), ok: isRowId(strip(arg)) })
  walk(sf, (n) => {
    if (ts.isElementAccessExpression(n) && ts.isIdentifier(n.expression) && n.expression.text === 'visible') check(n, n.argumentExpression)
    if (!ts.isCallExpression(n) || calleeName(n) !== 'setVisible') return
    for (const a of n.arguments)
      walk(a, (c) => {
        if (ts.isElementAccessExpression(c)) check(c, c.argumentExpression)
        if (ts.isComputedPropertyName(c)) check(c, c.expression)
      })
  })
  return sites
}

/** EnvEditor's hydrate carries a reveal by the row's slot (line and key), never by key name alone (agent-os-0b4m). */
function revealCarry(text: string) {
  const sf = parse('/src/components/stack/EnvEditor.tsx', text)
  let built = false
  const reads: boolean[] = []
  walk(sf, (n) => {
    if (ts.isVariableDeclaration(n) && n.name.getText(sf) === 'revealedRows' && n.initializer)
      built = /\.map\(rowSlot\)/.test(n.initializer.getText(sf))
    if (ts.isCallExpression(n) && ts.isPropertyAccessExpression(n.expression) && n.expression.name.text === 'has' && n.expression.expression.getText(sf) === 'revealedRows') {
      const arg = n.arguments[0]
      reads.push(arg !== undefined && ts.isCallExpression(arg) && calleeName(arg) === 'rowSlot')
    }
  })
  return { built, reads }
}

/** Every value input that can be a password field takes its placeholder from valuePlaceholder (hiddenValue.ts). */
function maskedValueInputs(path: string, text: string) {
  const sf = parse(path, text)
  const sites: Array<{ p: string; line: number; ok: boolean }> = []
  walk(sf, (n) => {
    if (!ts.isJsxOpeningElement(n) && !ts.isJsxSelfClosingElement(n)) return
    const type = attr(sf, n, 'type')
    const value = attrExpr(attr(sf, n, 'value'))
    if (!type?.initializer || !/password/.test(type.initializer.getText(sf))) return
    if (!value || !ts.isPropertyAccessExpression(value) || value.name.text !== 'value') return
    const placeholder = attrExpr(attr(sf, n, 'placeholder'))
    sites.push({ p: path, line: lineOf(sf, n), ok: !!placeholder && ts.isCallExpression(placeholder) && calleeName(placeholder) === 'valuePlaceholder' })
  })
  return sites
}

describe('guard 6: reveal follows the row, and a masked value says it is hidden (agent-os-uttl item 7)', () => {
  const globalEnv = () => production.filter(([p]) => p.startsWith(GLOBAL_ENV_DIR))

  it('global-env reveal state is indexed by _rowId', () => {
    const sites = globalEnv().flatMap(([p, t]) => revealIndexSites(p, t))
    expect(sites.length).toBeGreaterThanOrEqual(8)
    expect(sites.filter((s) => !s.ok)).toEqual([])
  })

  it('flags a planted reveal read by position', () => {
    const path = `${GLOBAL_ENV_DIR}GlobalEnvTable.tsx`
    const planted = sources[path].replace('visible[entry._rowId]', 'visible[index]')
    expect(planted).not.toBe(sources[path])
    expect(revealIndexSites(path, planted).filter((s) => !s.ok)).toHaveLength(1)
  })

  it('EnvEditor hydrate carries reveals by row slot', () => {
    const { built, reads } = revealCarry(sources['/src/components/stack/EnvEditor.tsx'])
    expect(built).toBe(true)
    expect(reads.length).toBeGreaterThan(0)
    expect(reads.every(Boolean)).toBe(true)
  })

  it('flags a planted hydrate that carries reveals by key name', () => {
    const text = sources['/src/components/stack/EnvEditor.tsx']
    const planted = text.replace('.map(rowSlot)', '.map((e) => e.key)').replace('revealedRows.has(rowSlot(e))', 'revealedRows.has(e.key)')
    expect(planted).not.toBe(text)
    const { built, reads } = revealCarry(planted)
    expect(built && reads.every(Boolean)).toBe(false)
  })

  it('every value input that can be masked takes valuePlaceholder', () => {
    const sites = tsxProduction().flatMap(([p, t]) => maskedValueInputs(p, t))
    expect([...new Set(sites.map((s) => s.p))].sort()).toEqual([
      `${GLOBAL_ENV_DIR}GlobalEnvCards.tsx`,
      `${GLOBAL_ENV_DIR}GlobalEnvTable.tsx`,
      '/src/components/stack/env-editor/EnvTableView.tsx',
    ])
    expect(sites).toHaveLength(4)
    expect(sites.filter((s) => !s.ok)).toEqual([])
  })

  it('flags a planted masked value input with a fixed placeholder', () => {
    const path = `${GLOBAL_ENV_DIR}GlobalEnvCards.tsx`
    const planted = sources[path].replace('placeholder={valuePlaceholder(locked, sensitive && !reveal, entry.value)}', 'placeholder="value"')
    expect(planted).not.toBe(sources[path])
    expect(maskedValueInputs(path, planted).filter((s) => !s.ok)).toHaveLength(1)
  })
})
