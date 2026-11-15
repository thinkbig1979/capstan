/**
 * Guard: an editor marks itself clean after a save only through
 * useStillHoldsSent (agent-os-k59u, follow-up of agent-os-3761, lqcw, k564).
 *
 * A save's callback closes over the render that clicked Save, so it cannot see
 * typing that landed while the save was in flight. useStillHoldsSent
 * (hooks/useStillHoldsSent.ts) reads the latest state through its own ref;
 * a callback that marks clean on its own throws that typing's dirty flag away.
 *
 * 1. CONSUMER arm: every call to a clean setter (CLEAN_SETTERS) with anything
 *    but `true` sits in the then-branch of an `if` whose condition calls
 *    `.stillHolds()`, or in a scope named in ALLOWED with the reason it holds
 *    no save in flight. The scope is the innermost variable the call sits in
 *    (`const hydrate = ...`, `const createEnvMutation = ...`). An ALLOWED entry
 *    that no longer holds a clean-setter call is a failure, so the list cannot
 *    go stale.
 * 2. PRODUCER arm: no file but useStillHoldsSent.ts makes a useRef of editor
 *    state (EDITOR_STATE): that is the hand-rolled shape the helper replaced.
 *
 * Planted violations are made IN MEMORY, so a green run cannot be a scanner
 * that matches nothing.
 *
 * BLIND SPOTS: syntax only. The setter names are a fixed list, so a clean flag
 * under another name is not seen. An editor whose dirtiness is DERIVED
 * (`content !== lastSaved`, the compose editors' setLastSaved) is not this
 * class and is not scanned. `.stillHolds()` is matched by name, so an `if` over
 * some other object's stillHolds passes. A ref of editor state under another
 * name, or built from an expression that names none of EDITOR_STATE, is not
 * seen by arm 2.
 */
import { describe, it, expect } from 'vitest'
import ts from 'typescript'
import { readSourceFiles } from '@/test/sourceFiles'

const sources = readSourceFiles()
const production = Object.entries(sources).filter(
  ([path]) => !path.includes('/__tests__/') && !/\.test\.tsx?$/.test(path) && !path.startsWith('/src/test/'),
)

const HELPER = '/src/hooks/useStillHoldsSent.ts'
const CLEAN_SETTERS = new Set(['setHasUnsavedChanges', 'setDirty'])
const EDITOR_STATE = new Set(['entries', 'vars', 'content', 'rawContent'])

// `${path}#${scope}` -> why a clean flag set there cannot discard typing.
const ALLOWED: Record<string, string> = {
  '/src/components/stack/EnvEditor.tsx#hydrate':
    'loads a payload as the new baseline: the rows on screen are replaced by it, so nothing typed is left to keep',
  '/src/components/settings/global-env/useGlobalEnvVars.ts#hydrate':
    'loads a payload as the new baseline: the rows on screen are replaced by it, so nothing typed is left to keep',
  '/src/components/stack/env-editor/useEnvMutations.ts#createEnvMutation':
    'creating the env file resets the editor to an empty file in the same callback, so nothing typed is kept',
}

function parse(path: string, text: string) {
  return ts.createSourceFile(path, text, ts.ScriptTarget.Latest, true, path.endsWith('.tsx') ? ts.ScriptKind.TSX : ts.ScriptKind.TS)
}

function walk(node: ts.Node, visit: (n: ts.Node) => void) {
  visit(node)
  node.forEachChild((c) => walk(c, visit))
}

const calleeName = (call: ts.CallExpression) =>
  ts.isIdentifier(call.expression) ? call.expression.text : ts.isPropertyAccessExpression(call.expression) ? call.expression.name.text : ''

const callsStillHolds = (node: ts.Node) => {
  let hit = false
  walk(node, (n) => {
    if (ts.isCallExpression(n) && ts.isPropertyAccessExpression(n.expression) && n.expression.name.text === 'stillHolds') hit = true
  })
  return hit
}

/** Inside the then-branch of an `if` whose condition calls `.stillHolds()`. */
function underStillHolds(node: ts.Node): boolean {
  for (let child = node, p = node.parent; p; child = p, p = p.parent) {
    if (ts.isIfStatement(p) && p.thenStatement === child && callsStillHolds(p.expression)) return true
  }
  return false
}

function innermostVariable(sf: ts.SourceFile, node: ts.Node): string {
  for (let p = node.parent; p; p = p.parent) if (ts.isVariableDeclaration(p)) return p.name.getText(sf)
  return ''
}

/** Every clean-setter call: where it is, and whether it is guarded or allowed. */
function cleanSites(path: string, text: string) {
  const sf = parse(path, text)
  const sites: Array<{ at: string; scope: string; ok: boolean }> = []
  walk(sf, (n) => {
    if (!ts.isCallExpression(n) || !CLEAN_SETTERS.has(calleeName(n))) return
    if (n.arguments[0]?.kind === ts.SyntaxKind.TrueKeyword) return
    const scope = `${path}#${innermostVariable(sf, n)}`
    sites.push({
      at: `${path}:${sf.getLineAndCharacterOfPosition(n.getStart(sf)).line + 1}`,
      scope,
      ok: underStillHolds(n) || scope in ALLOWED,
    })
  })
  return sites
}

/** Every useRef whose initial value names editor state, outside the helper. */
function editorStateRefs(path: string, text: string) {
  if (path === HELPER) return []
  const sf = parse(path, text)
  const hits: string[] = []
  walk(sf, (n) => {
    if (!ts.isCallExpression(n) || calleeName(n) !== 'useRef' || !n.arguments[0]) return
    let names = false
    walk(n.arguments[0], (c) => {
      if (ts.isIdentifier(c) && EDITOR_STATE.has(c.text)) names = true
    })
    if (names) hits.push(`${path}:${sf.getLineAndCharacterOfPosition(n.getStart(sf)).line + 1}`)
  })
  return hits
}

const HAND_ROLLED = `export function useZz(entries: Row[]) {
  const latest = useRef(entries)
  const m = useMutation({
    mutationFn,
    onSuccess: (_d, sent) => {
      if (latest.current === sent) setHasUnsavedChanges(false)
    },
  })
  return m
}`

describe('mark clean goes through useStillHoldsSent (agent-os-k59u)', () => {
  it('every clean-setter call in src is under stillHolds() or ALLOWED', () => {
    const sites = production.flatMap(([p, t]) => cleanSites(p, t))
    expect(sites.length).toBeGreaterThanOrEqual(5) // the scanner found the known sites
    expect(sites.filter((s) => !s.ok)).toEqual([])
    expect(sites.filter((s) => !(s.scope in ALLOWED)).length).toBeGreaterThanOrEqual(2) // the two save callbacks
  })

  it('every ALLOWED entry still holds a clean-setter call', () => {
    const scopes = new Set(production.flatMap(([p, t]) => cleanSites(p, t)).map((s) => s.scope))
    expect(Object.keys(ALLOWED).filter((k) => !scopes.has(k))).toEqual([])
  })

  it('no file but the helper makes a useRef of editor state', () => {
    expect(production.flatMap(([p, t]) => editorStateRefs(p, t))).toEqual([])
  })

  it('flags a planted save callback that marks clean without stillHolds()', () => {
    const path = '/src/components/stack/env-editor/useEnvMutations.ts'
    const planted = sources[path].replace('if (snapshot.stillHolds()) {', '{')
    expect(planted).not.toBe(sources[path])
    expect(cleanSites(path, planted).filter((s) => !s.ok)).toEqual([
      expect.objectContaining({ scope: `${path}#saveMutation`, ok: false }),
    ])
  })

  it('flags the old hand-rolled shape on both arms', () => {
    const path = '/src/components/Zz.tsx'
    expect(cleanSites(path, HAND_ROLLED)).toEqual([{ at: `${path}:6`, scope: `${path}#m`, ok: false }])
    expect(editorStateRefs(path, HAND_ROLLED)).toEqual([`${path}:2`])
  })
})
