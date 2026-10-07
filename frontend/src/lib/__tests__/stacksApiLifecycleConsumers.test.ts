/**
 * Guard for safe-defaults rule 8 on the stack lifecycle routes (agent-os-z91e.29).
 *
 * stacksApi.start/stop/restart/pull resolve an ActionResult, and a 207 `partial`
 * RESOLVES. A consumer that discards it and toasts success announces a green
 * result for a run that was not one. Every consumer in src must either hand the
 * call to useActionMutation (its mutationFn) or read the result in the same
 * function (`.outcome`, isActionResult, toastForResult).
 *
 * BLIND SPOTS, stated so a green run is not read as more than it is:
 *  - Per call site, by syntax. A helper that wraps stacksApi.pull in its own
 *    function that reads `.outcome` passes, and so does its consumer; a wrapper
 *    that reads the result and then ignores it also passes.
 *  - Only the name `stacksApi`. An alias (`const api = stacksApi`) or a
 *    destructure is not followed; the string-literal arm below catches a direct
 *    apiClient.post('/stacks/<id>/pull') outside lib/api.ts, nothing else does.
 *  - Test files and lib/api.ts (the producer) are not scanned.
 */
import { describe, it, expect } from 'vitest'
import ts from 'typescript'

const ACTIONS = new Set(['start', 'stop', 'restart', 'pull'])
const ROUTE = /\/stacks\/[^'"`]*\/(start|stop|restart|pull)\b/
const READS = new Set(['isActionResult', 'toastForResult'])

type FnLike = ts.ArrowFunction | ts.FunctionExpression | ts.FunctionDeclaration | ts.MethodDeclaration

const isFn = (n: ts.Node): n is FnLike =>
  ts.isArrowFunction(n) || ts.isFunctionExpression(n) || ts.isFunctionDeclaration(n) || ts.isMethodDeclaration(n)

// True when `node` is the value of a `mutationFn` property in an object literal
// passed straight to useActionMutation(...).
function isActionMutationFn(node: ts.Node): boolean {
  const prop = node.parent
  if (!prop || !(ts.isPropertyAssignment(prop) || ts.isMethodDeclaration(prop))) return false
  if (!ts.isIdentifier(prop.name) || prop.name.text !== 'mutationFn') return false
  const call = prop.parent?.parent
  return !!call && ts.isCallExpression(call) && ts.isIdentifier(call.expression) && call.expression.text === 'useActionMutation'
}

function readsOutcome(fn: ts.Node): boolean {
  let found = false
  const walk = (n: ts.Node) => {
    if (ts.isPropertyAccessExpression(n) && n.name.text === 'outcome') found = true
    if (ts.isCallExpression(n) && ts.isIdentifier(n.expression) && READS.has(n.expression.text)) found = true
    if (!found) ts.forEachChild(n, walk)
  }
  walk(fn)
  return found
}

function findUnguarded(source: string, file = 'fixture.tsx'): string[] {
  const sf = ts.createSourceFile(file, source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX)
  const hits: string[] = []
  const at = (n: ts.Node) => `${file}:${sf.getLineAndCharacterOfPosition(n.getStart()).line + 1}`
  const visit = (n: ts.Node) => {
    if (ts.isPropertyAccessExpression(n) && ts.isIdentifier(n.expression) && n.expression.text === 'stacksApi' && ACTIONS.has(n.name.text)) {
      let fn: ts.Node | undefined = n.parent
      while (fn && !isFn(fn)) fn = fn.parent
      const guarded = isActionMutationFn(n) || (fn && (isActionMutationFn(fn) || readsOutcome(fn)))
      if (!guarded) hits.push(`${at(n)} stacksApi.${n.name.text} is not read as an ActionResult`)
    }
    if (ts.isStringLiteralLike(n) || ts.isTemplateExpression(n)) {
      if (ROUTE.test(n.getText(sf))) hits.push(`${at(n)} direct call to a stack lifecycle route outside lib/api.ts`)
    }
    ts.forEachChild(n, visit)
  }
  visit(sf)
  return hits
}

describe('stack lifecycle consumers read the ActionResult outcome (agent-os-z91e.29)', () => {
  // The pre-z91e.29 shape of ContainersOverviewTab's pullMutation, trimmed.
  it('CONTROL: flags a bare useMutation that discards the pull result', () => {
    const bad = `const m = useMutation({ mutationFn: async () => { await stacksApi.pull(id) }, onSuccess: () => toast.success('Images pulled') })`
    expect(findUnguarded(bad)).toEqual(['fixture.tsx:1 stacksApi.pull is not read as an ActionResult'])
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
    const sources = import.meta.glob<string>('/src/**/*.{ts,tsx}', { query: '?raw', import: 'default', eager: true })
    const files = Object.entries(sources).filter(([p]) => !p.includes('/__tests__/') && !/\.test\.tsx?$/.test(p) && p !== '/src/lib/api.ts')
    // A glob that matched nothing would make this pass vacuously.
    expect(files.length).toBeGreaterThan(100)
    expect(files.flatMap(([p, src]) => findUnguarded(src, p))).toEqual([])
  })
})
