/**
 * Guard for safe-defaults rule 8 on every ActionResult route (agent-os-z91e.29,
 * widened to all routes by agent-os-qags.22).
 *
 * An api.ts function that resolves an ActionResult can resolve `partial` or
 * `no_change` on a 2xx. A consumer that discards it and toasts success announces
 * a green result for a run that was not one. The route set is DERIVED from
 * lib/api.ts (an apiClient call typed ActionResult or an alias of it) and pinned
 * below, so a new route is a visible edit to this file.
 *
 * Every reference to a route must land in a consumer scope that reads the
 * outcome (`.outcome`, isActionResult, toastForResult), and every toast.success
 * in that scope must come after an `.outcome` / isActionResult read. The scope:
 *  - the options object of useActionMutation, when the reference is (or sits
 *    directly in) its mutationFn. The wrapper reads the outcome itself, so only
 *    the toast.success order is checked there;
 *  - the sibling onSuccess of a raw useMutation, when the mutationFn returns the
 *    result;
 *  - otherwise the enclosing function.
 * A result RETURNED from a named function makes that function a derived route
 * (lib/stack-delete.ts), and one returned into a JSX attribute makes the prop a
 * derived route inside the component (PruneButton's pruneFn); their references
 * are checked by the same rules. A result returned from a callback argument
 * (`ids.map((id) => stacksApi[action](id))`) is followed to the outer call.
 * A shape that cannot be followed is a hit, never a pass.
 *
 * The per-route rows plant `toast.success('PLANTED')` at the start of each
 * consumer scope IN MEMORY (the source text is mutated before parsing; nothing
 * is written to disk) and require the guard to flag it.
 *
 * BLIND SPOTS, stated so a green run is not read as more than it is:
 *  - Syntax only, no type checker. `stacksApi[expr]` is checked whatever the key
 *    (stricter than needed), but an alias or destructure of an api object is a
 *    hit rather than followed.
 *  - A toast.success in a call-level `m.mutate(x, { onSuccess })` callback, or
 *    after `await m.mutateAsync()`, is not tied to its mutation: nothing here
 *    knows that `m` came from a route.
 *  - A wrapper that reads `.outcome` and then returns the result passes as the
 *    consumer; its own consumers are not followed.
 *  - Only toast.success counts as a success announcement; `toast(` and
 *    toast.message do not.
 *  - Test files and lib/api.ts (the producer) are not scanned.
 */
import { describe, it, expect } from 'vitest'
import ts from 'typescript'

const ACTIONS = new Set(['start', 'stop', 'restart', 'pull'])
const ROUTE = /\/stacks\/[^'"`]*\/(start|stop|restart|pull)\b/
const READS = new Set(['isActionResult', 'toastForResult'])

// The 20 routes measured on b01032d. Derived below; pinned so a change is seen.
const PINNED_ROUTES = [
  'gitApi.pull',
  'resourcesApi.createNetwork', 'resourcesApi.deleteContainer', 'resourcesApi.deleteImage',
  'resourcesApi.deleteNetwork', 'resourcesApi.deleteVolume', 'resourcesApi.pruneBuildCache',
  'resourcesApi.pruneContainers', 'resourcesApi.pruneImages', 'resourcesApi.pruneNetworks',
  'resourcesApi.pruneVolumes',
  'stacksApi.create', 'stacksApi.createEnv', 'stacksApi.delete', 'stacksApi.pull', 'stacksApi.restart',
  'stacksApi.start', 'stacksApi.stop', 'stacksApi.updateComposeAndEnv', 'stacksApi.updateEnv',
]

type FnLike = ts.ArrowFunction | ts.FunctionExpression | ts.FunctionDeclaration | ts.MethodDeclaration

const isFn = (n: ts.Node): n is FnLike =>
  ts.isArrowFunction(n) || ts.isFunctionExpression(n) || ts.isFunctionDeclaration(n) || ts.isMethodDeclaration(n)

const sources = import.meta.glob<string>('/src/**/*.{ts,tsx}', { query: '?raw', import: 'default', eager: true })
const API_PATH = '/src/lib/api.ts'

function parse(file: string, source: string): ts.SourceFile {
  return ts.createSourceFile(file, source, ts.ScriptTarget.Latest, true, file.endsWith('.tsx') ? ts.ScriptKind.TSX : ts.ScriptKind.TS)
}

// api object name -> its ActionResult members.
function deriveRoutes(apiSource: string): Map<string, Set<string>> {
  const sf = parse(API_PATH, apiSource)
  const resultTypes = new Set(['ActionResult'])
  sf.statements.forEach((s) => {
    if (ts.isTypeAliasDeclaration(s) && ts.isTypeReferenceNode(s.type) && s.type.typeName.getText(sf) === 'ActionResult') {
      resultTypes.add(s.name.text)
    }
  })
  const routes = new Map<string, Set<string>>()
  sf.statements.forEach((s) => {
    if (!ts.isVariableStatement(s)) return
    for (const d of s.declarationList.declarations) {
      if (!ts.isIdentifier(d.name) || !/Api$/.test(d.name.text) || !d.initializer || !ts.isObjectLiteralExpression(d.initializer)) continue
      for (const p of d.initializer.properties) {
        if (!ts.isPropertyAssignment(p) || !ts.isIdentifier(p.name)) continue
        let typed = false
        const walk = (n: ts.Node) => {
          if (ts.isCallExpression(n) && ts.isPropertyAccessExpression(n.expression) && n.expression.expression.getText(sf) === 'apiClient') {
            const t = n.typeArguments?.[0]
            if (t && ts.isTypeReferenceNode(t) && resultTypes.has(t.typeName.getText(sf))) typed = true
          }
          if (!typed) ts.forEachChild(n, walk)
        }
        walk(p.initializer)
        if (typed) {
          if (!routes.has(d.name.text)) routes.set(d.name.text, new Set())
          routes.get(d.name.text)!.add(p.name.text)
        }
      }
    }
  })
  return routes
}

const ROUTES = deriveRoutes(sources[API_PATH])
const RESULT_TYPES = (() => {
  const set = new Set(['ActionResult'])
  parse(API_PATH, sources[API_PATH]).statements.forEach((s) => {
    if (ts.isTypeAliasDeclaration(s) && ts.isTypeReferenceNode(s.type) && s.type.typeName.getText() === 'ActionResult') set.add(s.name.text)
  })
  return set
})()

/** A consumer scope one or more routes reach, kept so the per-route rows can plant into it. */
interface Scope {
  file: string
  node: ts.Node
  kind: 'useActionMutation' | 'onSuccess' | 'enclosing function'
  routes: Set<string>
}

interface Ref {
  file: string
  node: ts.Node
  route: string
  // The node is a call whose result carries the route (followed out of a callback).
  followed?: boolean
}

const lineOf = (n: ts.Node) => n.getSourceFile().getLineAndCharacterOfPosition(n.getStart()).line + 1
const at = (file: string, n: ts.Node) => `${file}:${lineOf(n)}`

// Climb through wrappers that pass a value on unchanged.
function climbValue(e: ts.Node): ts.Node {
  for (;;) {
    const p = e.parent
    if (!p) return e
    if (ts.isAwaitExpression(p) || ts.isParenthesizedExpression(p) || ts.isAsExpression(p) || ts.isNonNullExpression(p) || ts.isSatisfiesExpression(p)) e = p
    else if (ts.isConditionalExpression(p) && p.condition !== e) e = p
    else return e
  }
}

// The function the value is returned from, or undefined when it is not returned.
function returnedFrom(value: ts.Node): FnLike | undefined {
  const p = value.parent
  if (p && isFn(p) && ts.isArrowFunction(p) && p.body === value) return p
  if (p && ts.isReturnStatement(p)) {
    let f: ts.Node | undefined = p.parent
    while (f && !isFn(f)) f = f.parent
    return f as FnLike | undefined
  }
  return undefined
}

// The useMutation / useActionMutation options object whose mutationFn is `n`.
function mutationOptionsOf(n: ts.Node): { hook: string; options: ts.ObjectLiteralExpression } | undefined {
  const prop = n.parent
  if (!prop || !(ts.isPropertyAssignment(prop) || ts.isMethodDeclaration(prop))) return undefined
  if (ts.isPropertyAssignment(prop) && prop.initializer !== n) return undefined
  if (!ts.isIdentifier(prop.name) || prop.name.text !== 'mutationFn') return undefined
  const options = prop.parent
  const call = options?.parent
  if (!ts.isObjectLiteralExpression(options) || !call || !ts.isCallExpression(call) || !ts.isIdentifier(call.expression)) return undefined
  const hook = call.expression.text
  return hook === 'useMutation' || hook === 'useActionMutation' ? { hook, options } : undefined
}

function propertyValue(o: ts.ObjectLiteralExpression, name: string): ts.Node | undefined {
  for (const p of o.properties) {
    if (!p.name || !ts.isIdentifier(p.name) || p.name.text !== name) continue
    if (ts.isPropertyAssignment(p)) return p.initializer
    if (ts.isMethodDeclaration(p)) return p
  }
  return undefined
}

const isOutcomeRead = (n: ts.Node) =>
  (ts.isPropertyAccessExpression(n) && n.name.text === 'outcome') ||
  (ts.isCallExpression(n) && ts.isIdentifier(n.expression) && n.expression.text === 'isActionResult')

const isAnyRead = (n: ts.Node) =>
  isOutcomeRead(n) || (ts.isCallExpression(n) && ts.isIdentifier(n.expression) && READS.has(n.expression.text))

const isToastSuccess = (n: ts.Node) =>
  ts.isCallExpression(n) && ts.isPropertyAccessExpression(n.expression) && n.expression.name.text === 'success' &&
  ts.isIdentifier(n.expression.expression) && n.expression.expression.text === 'toast'

function nodesIn(scope: ts.Node, pred: (n: ts.Node) => boolean): ts.Node[] {
  const out: ts.Node[] = []
  const walk = (n: ts.Node) => {
    if (pred(n)) out.push(n)
    ts.forEachChild(n, walk)
  }
  walk(scope)
  return out
}

// Declaration positions a name lookup must not count as a reference.
function isDeclarationName(id: ts.Identifier): boolean {
  const p = id.parent
  return (
    ((ts.isFunctionDeclaration(p) || ts.isVariableDeclaration(p) || ts.isBindingElement(p) || ts.isParameter(p) ||
      ts.isPropertyAssignment(p) || ts.isPropertySignature(p) || ts.isMethodDeclaration(p) || ts.isJsxAttribute(p)) && p.name === id) ||
    (ts.isBindingElement(p) && p.propertyName === id) ||
    ts.isImportSpecifier(p) || ts.isImportClause(p) || ts.isExportSpecifier(p) ||
    (ts.isPropertyAccessExpression(p) && p.name === id)
  )
}

function scan(files: Record<string, string>): { hits: string[]; scopes: Scope[] } {
  const parsed = Object.entries(files).map(([file, src]) => ({ file, sf: parse(file, src) }))
  const hits: string[] = []
  const scopes = new Map<ts.Node, Scope>()
  const work: Ref[] = []
  const seen = new Set<string>()
  const notRead = (r: Ref, n: ts.Node, rule: string) => hits.push(`${at(r.file, n)} ${r.route} is not read as an ActionResult: ${rule}`)

  for (const { file, sf } of parsed) {
    const visit = (n: ts.Node) => {
      if (ts.isPropertyAccessExpression(n) && ts.isIdentifier(n.expression) && ROUTES.get(n.expression.text)?.has(n.name.text)) {
        work.push({ file, node: n, route: `${n.expression.text}.${n.name.text}` })
      } else if (ts.isElementAccessExpression(n) && ts.isIdentifier(n.expression) && ROUTES.has(n.expression.text)) {
        work.push({ file, node: n, route: `${n.expression.text}[*]` })
      } else if (ts.isIdentifier(n) && ROUTES.has(n.text) && !isDeclarationName(n)) {
        const p = n.parent
        const accessed = (ts.isPropertyAccessExpression(p) || ts.isElementAccessExpression(p)) && p.expression === n
        if (!accessed) hits.push(`${at(file, n)} ${n.text} is aliased or destructured, so its ActionResult routes cannot be followed`)
      }
      if (ts.isCallExpression(n) && ts.isPropertyAccessExpression(n.expression) && n.expression.expression.getText(sf) === 'apiClient') {
        const t = n.typeArguments?.[0]
        if (t && ts.isTypeReferenceNode(t) && RESULT_TYPES.has(t.typeName.getText(sf))) {
          hits.push(`${at(file, n)} apiClient call typed ${t.typeName.getText(sf)} outside lib/api.ts: add it to an api object instead`)
        }
      }
      if (ts.isStringLiteralLike(n) || ts.isTemplateExpression(n)) {
        if (ROUTE.test(n.getText(sf))) hits.push(`${at(file, n)} direct call to a stack lifecycle route outside lib/api.ts`)
      }
      ts.forEachChild(n, visit)
    }
    visit(sf)
  }

  const addScope = (r: Ref, node: ts.Node, kind: Scope['kind']) => {
    const s = scopes.get(node) ?? { file: r.file, node, kind, routes: new Set<string>() }
    s.routes.add(r.route.replace(/ \(via .*\)$/, ''))
    scopes.set(node, s)
    if (kind !== 'useActionMutation' && !nodesIn(node, isAnyRead).length) {
      notRead(r, r.node, `its ${kind} (${at(r.file, node)}) never reads the outcome`)
      return
    }
    const reads = nodesIn(node, isOutcomeRead).map((n) => n.getStart())
    for (const t of nodesIn(node, isToastSuccess)) {
      if (!reads.some((p) => p < t.getStart())) notRead(r, t, `toast.success before any .outcome / isActionResult read in its ${kind}`)
    }
  }

  const followName = (r: Ref, name: string) => {
    for (const { file, sf } of parsed) {
      const visit = (n: ts.Node) => {
        if (ts.isIdentifier(n) && n.text === name && !isDeclarationName(n)) work.push({ file, node: n, route: `${r.route.replace(/ \(via .*\)$/, '')} (via ${name})` })
        ts.forEachChild(n, visit)
      }
      visit(sf)
    }
  }

  const followProp = (r: Ref, attr: ts.JsxAttribute) => {
    const element = attr.parent.parent
    const comp = element.tagName.getText()
    const name = attr.name.getText()
    const via = `${r.route.replace(/ \(via .*\)$/, '')} (via <${comp} ${name}>)`
    let found = 0
    for (const { file, sf } of parsed) {
      const visit = (n: ts.Node) => {
        const body =
          ts.isFunctionDeclaration(n) && n.name?.text === comp ? n
            : ts.isVariableDeclaration(n) && ts.isIdentifier(n.name) && n.name.text === comp && n.initializer && isFn(n.initializer) ? n.initializer
              : undefined
        if (body) {
          nodesIn(body, (m) => ts.isIdentifier(m) && m.text === name && !isDeclarationName(m)).forEach((m) => {
            found++
            work.push({ file, node: m, route: via })
          })
          nodesIn(body, (m) => ts.isPropertyAccessExpression(m) && m.name.text === name).forEach((m) => {
            found++
            work.push({ file, node: m, route: via })
          })
        }
        ts.forEachChild(n, visit)
      }
      visit(sf)
    }
    if (!found) notRead(r, r.node, `returned into <${comp} ${name}> and no reference to ${name} was found inside ${comp}`)
  }

  while (work.length) {
    const r = work.shift()!
    const key = `${r.file}:${r.node.getStart()}:${r.route}`
    if (seen.has(key)) continue
    seen.add(key)

    // The value this reference produces: the call when it is called, else the reference.
    const call = r.followed ? r.node
      : r.node.parent && ts.isCallExpression(r.node.parent) && r.node.parent.expression === r.node ? r.node.parent : undefined
    const value = climbValue(call ?? r.node)
    let fn: ts.Node | undefined = r.node.parent
    while (fn && !isFn(fn)) fn = fn.parent

    // Directly the mutationFn (`mutationFn: stacksApi.stop`), or inside it.
    const direct = mutationOptionsOf(value)
    const inside = fn ? mutationOptionsOf(fn) : undefined
    const m = direct ?? inside
    if (m?.hook === 'useActionMutation') {
      addScope(r, m.options, 'useActionMutation')
      continue
    }
    const from = returnedFrom(value)
    if (m?.hook === 'useMutation' && (direct || from === fn)) {
      const onSuccess = propertyValue(m.options, 'onSuccess')
      if (onSuccess) addScope(r, onSuccess, 'onSuccess')
      else notRead(r, r.node, 'raw useMutation with no onSuccess; use useActionMutation')
      continue
    }

    // A pass-through: follow the value to whoever receives it.
    const jsx = value.parent && ts.isJsxExpression(value.parent) && ts.isJsxAttribute(value.parent.parent) ? value.parent.parent : undefined
    if (jsx) {
      followProp(r, jsx)
      continue
    }
    if (from) {
      const fp = from.parent
      if (ts.isFunctionDeclaration(from) && from.name) followName(r, from.name.text)
      else if (fp && ts.isVariableDeclaration(fp) && ts.isIdentifier(fp.name)) followName(r, fp.name.text)
      else if (fp && ts.isJsxExpression(fp) && ts.isJsxAttribute(fp.parent)) followProp(r, fp.parent)
      else if (fp && ts.isCallExpression(fp) && fp.arguments.includes(from as ts.Expression)) work.push({ ...r, node: fp, followed: true })
      else if (!mutationOptionsOf(from)) notRead(r, r.node, 'returned from a function this guard cannot follow')
      else notRead(r, r.node, 'returned from a mutationFn this guard cannot follow')
      continue
    }

    if (!call) {
      notRead(r, r.node, 'referenced without being called, and this guard cannot follow the reference')
      continue
    }
    // A result passed as an argument (`Promise.allSettled(ids.map(...))`) flows into that call's result.
    if (value.parent && ts.isCallExpression(value.parent) && value.parent.arguments.includes(value as ts.Expression)) {
      work.push({ ...r, node: value.parent, followed: true })
      continue
    }
    if (fn) addScope(r, fn, 'enclosing function')
    else notRead(r, r.node, 'called at module level')
  }

  return { hits, scopes: [...scopes.values()] }
}

function findUnguarded(source: string, file = 'fixture.tsx'): string[] {
  return scan({ [file]: source }).hits
}

const scanHits = (files: Record<string, string>) => scan(files).hits

// Plant `toast.success('PLANTED')` at the start of a scope, in memory only.
function plant(source: string, s: Scope): { source: string; line: number } {
  const n = s.node
  const insert = (pos: number, text: string, src = source) => src.slice(0, pos) + text + src.slice(pos)
  const lineAt = (pos: number) => source.slice(0, pos).split('\n').length
  if (ts.isObjectLiteralExpression(n)) {
    const pos = n.getStart() + 1
    return { source: insert(pos, ` onResult: () => { toast.success('PLANTED') },`), line: lineAt(pos) }
  }
  const body = (n as FnLike).body!
  if (ts.isBlock(body)) {
    const pos = body.getStart() + 1
    return { source: insert(pos, ` toast.success('PLANTED');`), line: lineAt(pos) }
  }
  const withEnd = insert(body.getEnd(), ')')
  return { source: insert(body.getStart(), `(toast.success('PLANTED'), `, withEnd), line: lineAt(body.getStart()) }
}

const tree = Object.fromEntries(
  Object.entries(sources).filter(([p]) => !p.includes('/__tests__/') && !/\.test\.tsx?$/.test(p) && p !== API_PATH),
)
const treeScan = scan(tree)

describe('stack lifecycle consumers read the ActionResult outcome (agent-os-z91e.29)', () => {
  // The pre-z91e.29 shape of ContainersOverviewTab's pullMutation, trimmed.
  it('CONTROL: flags a bare useMutation that discards the pull result', () => {
    const bad = `const m = useMutation({ mutationFn: async () => { await stacksApi.pull(id) }, onSuccess: () => toast.success('Images pulled') })`
    expect(findUnguarded(bad)).toEqual([expect.stringMatching(/^fixture\.tsx:1 stacksApi\.pull is not read as an ActionResult: /)])
  })

  it('CONTROL: flags a bare reference and a direct route call', () => {
    expect(findUnguarded(`useMutation({ mutationFn: stacksApi.restart })`)).toHaveLength(1)
    expect(findUnguarded(`apiClient.post(\`/stacks/\${id}/stop\`)`)).toHaveLength(1)
  })

  it('CONTROL: passes useActionMutation, a bare mutationFn reference, and a consumer that reads .outcome', () => {
    expect(findUnguarded(`useActionMutation({ mutationFn: async () => stacksApi.start(id) })`)).toEqual([])
    expect(findUnguarded(`useActionMutation({ mutationFn: stacksApi.stop })`)).toEqual([])
    expect(findUnguarded(`async function f() { const r = await stacksApi.pull(id); if (r.outcome === 'partial') warn() }`)).toEqual([])
  })

  it('CONTROL: an unrelated stacksApi method and an unrelated route are not flagged', () => {
    expect(findUnguarded(`stacksApi.list(); apiClient.post('/stacks/x/env')`)).toEqual([])
  })

  it('every non-test consumer in src is guarded', () => {
    // A glob that matched nothing would make this pass vacuously.
    expect(Object.keys(tree).length).toBeGreaterThan(100)
    expect(treeScan.hits).toEqual([])
  })
})

describe('controls: shapes the 4-route guard could not see (agent-os-qags.22)', () => {
  it('flags a raw useMutation whose sibling onSuccess toasts without reading the outcome', () => {
    const src = `useMutation({ mutationFn: (n) => resourcesApi.deleteVolume(n), onSuccess: () => toast.success('Volume removed') })`
    expect(scanHits({ '/f.ts': src })).toEqual([expect.stringMatching(/^\/f\.ts:1 resourcesApi\.deleteVolume /)])
  })

  it('passes a raw useMutation whose sibling onSuccess reads the outcome', () => {
    const src = `useMutation({ mutationFn: (n) => resourcesApi.deleteVolume(n), onSuccess: (d) => toastForResult(d) })`
    expect(scanHits({ '/f.ts': src })).toEqual([])
  })

  it('flags a toast.success that runs before the outcome is read', () => {
    const src = `useMutation({ mutationFn: (i) => stacksApi.create(i), onSuccess: (d) => { toast.success('Created'); if (d.outcome === 'partial') warn() } })`
    expect(scanHits({ '/f.ts': src })).toEqual([expect.stringMatching(/^\/f\.ts:1 stacksApi\.create .*toast\.success/)])
  })

  it('flags an unread element access and passes one read through a callback', () => {
    expect(scanHits({ '/f.ts': `async function f(a) { await stacksApi[a](id); toast.success('done') }` }))
      .toEqual([expect.stringMatching(/^\/f\.ts:1 stacksApi\[\*\] /)])
    const read = `async function f(a) { const rs = await Promise.all(ids.map((id) => stacksApi[a](id))); if (rs.every((r) => r.outcome === 'success')) toast.success('done') }`
    expect(scanHits({ '/f.ts': read })).toEqual([])
  })

  it('follows a pass-through wrapper to the consumer that toasts', () => {
    const files = {
      '/w.ts': `export async function wrap(id) { return await stacksApi.delete(id) }`,
      '/c.tsx': `useMutation({ mutationFn: (id) => wrap(id), onSuccess: () => toast.success('Deleted') })`,
    }
    expect(scanHits(files)).toEqual([expect.stringMatching(/^\/c\.tsx:1 stacksApi\.delete \(via wrap\) /)])
    expect(scanHits({ ...files, '/c.tsx': `useActionMutation({ mutationFn: (id) => wrap(id) })` })).toEqual([])
  })

  it('follows a JSX prop to the component that consumes it', () => {
    const files = {
      '/tab.tsx': `const x = <Prune pruneFn={(o) => resourcesApi.pruneImages(o)} />`,
      '/p.tsx': `export function Prune({ pruneFn }) { const m = useMutation({ mutationFn: pruneFn, onSuccess: () => toast.success('Pruned') }) }`,
    }
    expect(scanHits(files)).toEqual([expect.stringMatching(/^\/p\.tsx:1 resourcesApi\.pruneImages \(via <Prune pruneFn>\) /)])
    expect(scanHits({ '/tab.tsx': files['/tab.tsx'] })).toEqual([expect.stringMatching(/^\/tab\.tsx:1 .*no reference to pruneFn/)])
  })

  it('flags an unread toast.success inside useActionMutation options', () => {
    const src = `useActionMutation({ mutationFn: () => stacksApi.updateEnv(id, b), onResult: () => toast.success('Saved') })`
    expect(scanHits({ '/f.ts': src })).toEqual([expect.stringMatching(/^\/f\.ts:1 stacksApi\.updateEnv .*toast\.success/)])
  })

  it('flags an alias, a destructure, and an ActionResult apiClient call outside api.ts', () => {
    const src = `const api = stacksApi\nconst { pull } = gitApi\napiClient.post<LifecycleResult>(u)`
    expect(scanHits({ '/f.ts': src })).toHaveLength(3)
  })
})

describe('every ActionResult route is guarded at each of its consumers (agent-os-qags.22)', () => {
  it('derives exactly the pinned routes from lib/api.ts', () => {
    const derived = [...ROUTES].flatMap(([api, members]) => [...members].map((m) => `${api}.${m}`)).sort()
    expect(derived).toEqual([...PINNED_ROUTES].sort())
    // The 4 routes the z91e.29 guard covered are still in the set.
    for (const a of ACTIONS) expect(derived).toContain(`stacksApi.${a}`)
  })

  const rows = [...PINNED_ROUTES, 'stacksApi[*]'].map((route) => {
    const scopes = treeScan.scopes.filter((s) => s.routes.has(route))
    return [route, scopes.map((s) => `${s.file}:${lineOf(s.node)} ${s.kind}`).join(', '), scopes] as const
  })

  it.each(rows)('a planted unread toast.success on %s is flagged at: %s', (route, _where, scopes) => {
    expect(scopes.length).toBeGreaterThan(0)
    for (const s of scopes) {
      const planted = plant(tree[s.file], s)
      const hits = scan({ ...tree, [s.file]: planted.source }).hits
      expect(hits, `${route} at ${s.file}:${lineOf(s.node)}`).toContainEqual(
        expect.stringMatching(new RegExp(`^${s.file.replace(/[.*+?^${}()|[\]\\/]/g, '\\$&')}:${planted.line} .*toast\\.success before`)),
      )
    }
  })
})
