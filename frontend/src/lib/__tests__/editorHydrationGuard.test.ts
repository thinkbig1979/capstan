/**
 * Guard: an editor that holds a copy of query data must take new data through
 * useExternalFileChange (agent-os-auj3, after agent-os-umig and agent-os-lqcw).
 *
 * The defect: a component copies a query result into editable state and a later
 * payload (a window-focus refetch, another tab's save, a git pull, the unlock
 * expiring) replaces what the user is typing, with no warning. Two shapes do it:
 *  - EFFECT: a useEffect/useLayoutEffect whose dependency list names query data
 *    and whose body calls a state setter (`setX(...)`);
 *  - RENDER: an `if (data !== prev)` block that calls a state setter, the
 *    render-time adjustment useExternalFileChange itself is built from.
 * A file that does either without importing useExternalFileChange fails unless
 * it is named in ALLOWED below, by exact path, with the reason it holds no
 * edits worth keeping. An ALLOWED entry that no longer matches a hit fails too,
 * so the list cannot go stale.
 *
 * "Query data" is derived, not listed: the `data` of any `use*()` call that is
 * destructured (`const { data: envData } = useX()`) or read as `.data` off a
 * hook result, plus any name initialised from an expression that mentions one.
 *
 * BLIND SPOTS, stated so a green run is not read as more than it is:
 *  - Syntax only, no type checker: a `use*` hook that is not a query but returns
 *    `data` is treated as one, and a query whose result is renamed through a
 *    prop or a context is not followed.
 *  - The check is per file: importing useExternalFileChange clears every hit in
 *    that file, including an unrelated effect added later. A new data-driven
 *    `useEffect(setX)` added to ComposeEditor, EnvEditor or useGlobalEnvVars, which
 *    already import it, is therefore invisible. Known limit, accepted: narrowing
 *    the exemption to the data name passed to the hook call is the way to close it.
 *  - A setter is any callee named `set[A-Z]...`; a state update spelled another
 *    way (a store action, `dispatch`) is not seen.
 *  - Initial state from data (`useState(data.x)`) and a `key={data.id}` remount
 *    are not hydration-after-mount and are not scanned.
 *  - Test files are not scanned.
 *  - A component that receives the data as a prop or a parameter (not from a
 *    `use*` call in the same file) is not seen; useBackupForm is one such.
 *  - OBSERVED: the file list comes from import.meta.glob, and a local run with
 *    the test file's text unchanged did not see a component added to disk after
 *    the previous run (exit 0 with the planted file present, exit 1 after a
 *    one-line edit to this file). After adding a file, edit this file or clear
 *    the vitest cache; a fresh CI checkout is not affected. The cache location
 *    was not identified.
 */
import { describe, it, expect } from 'vitest'
import ts from 'typescript'

const HOOK = 'useExternalFileChange'

// Exact path -> why the file holds no unsaved edit that a refetch could replace.
const ALLOWED: Record<string, string> = {
  '/src/components/dashboard/BackupToggle.tsx':
    'an on/off switch and a stop-policy select with optimistic local state, not typed text; the effect re-syncs them to the server policy after a refetch, and a click is saved at once, so nothing unsaved can be replaced',
  '/src/components/dashboard/PruneButton.tsx':
    'a render-time reset of the typed confirmation word whenever the impact check returns a new answer (the word consents to the answer on screen); it clears an input on purpose and holds no copy of query data',
}

// Editors known to be on the hook: a positive control for the "imports it" arm.
const ON_HOOK = [
  '/src/components/stack/ComposeEditor.tsx',
  '/src/components/stack/EnvEditor.tsx',
  '/src/components/settings/global-env/useGlobalEnvVars.ts',
]

const sources = import.meta.glob<string>('/src/**/*.{ts,tsx}', { query: '?raw', import: 'default', eager: true })

const isTestFile = (path: string) => /\/__tests__\/|\.test\.tsx?$|^\/src\/test\//.test(path)

const parse = (file: string, source: string) =>
  ts.createSourceFile(file, source, ts.ScriptTarget.Latest, true, file.endsWith('.tsx') ? ts.ScriptKind.TSX : ts.ScriptKind.TS)

const lineOf = (n: ts.Node) => n.getSourceFile().getLineAndCharacterOfPosition(n.getStart()).line + 1

const isSetterCall = (n: ts.Node) =>
  ts.isCallExpression(n) && ts.isIdentifier(n.expression) && /^set[A-Z]/.test(n.expression.text)

function containsSetterCall(n: ts.Node): boolean {
  let found = false
  const walk = (c: ts.Node) => {
    if (found) return
    if (isSetterCall(c)) found = true
    else ts.forEachChild(c, walk)
  }
  walk(n)
  return found
}

function mentions(n: ts.Node, names: Set<string>): boolean {
  let found = false
  const walk = (c: ts.Node) => {
    if (found) return
    if (ts.isIdentifier(c) && names.has(c.text)) found = true
    else ts.forEachChild(c, walk)
  }
  walk(n)
  return found
}

const calleeName = (call: ts.CallExpression) => (ts.isIdentifier(call.expression) ? call.expression.text : '')

/** Names that hold query data in this file, derived as the header describes. */
function dataNames(sf: ts.SourceFile): Set<string> {
  const names = new Set<string>()
  const hookResults = new Set<string>()
  const decls: ts.VariableDeclaration[] = []
  const collect = (n: ts.Node) => {
    if (ts.isVariableDeclaration(n) && n.initializer) decls.push(n)
    ts.forEachChild(n, collect)
  }
  collect(sf)
  const hookCall = (e: ts.Expression): ts.CallExpression | undefined => {
    let x: ts.Expression = e
    while (ts.isAwaitExpression(x) || ts.isParenthesizedExpression(x)) x = x.expression
    return ts.isCallExpression(x) && /^use[A-Z]/.test(calleeName(x)) ? x : undefined
  }
  for (const d of decls) {
    if (!d.initializer || !hookCall(d.initializer)) continue
    if (ts.isIdentifier(d.name)) hookResults.add(d.name.text)
    else if (ts.isObjectBindingPattern(d.name)) {
      for (const el of d.name.elements) {
        const prop = el.propertyName ?? el.name
        if (ts.isIdentifier(prop) && prop.text === 'data' && ts.isIdentifier(el.name)) names.add(el.name.text)
      }
    }
  }
  // `.data` read off a hook result.
  const walkAccess = (n: ts.Node) => {
    if (
      ts.isPropertyAccessExpression(n) &&
      n.name.text === 'data' &&
      ts.isIdentifier(n.expression) &&
      hookResults.has(n.expression.text)
    ) {
      names.add(n.getText(sf))
    }
    ts.forEachChild(n, walkAccess)
  }
  walkAccess(sf)
  // Anything initialised from an expression that mentions data is data too.
  for (let grew = true; grew; ) {
    grew = false
    for (const d of decls) {
      if (!d.initializer || !ts.isIdentifier(d.name) || names.has(d.name.text)) continue
      if (hookResults.has(d.name.text)) continue
      const readsData =
        mentions(d.initializer, names) ||
        [...names].some((nm) => nm.includes('.') && d.initializer!.getText(sf).includes(nm))
      if (readsData) {
        names.add(d.name.text)
        grew = true
      }
    }
  }
  return names
}

interface Hit {
  file: string
  line: number
  shape: 'EFFECT' | 'RENDER'
}

function scanFile(file: string, source: string): { hits: Hit[]; onHook: boolean } {
  const sf = parse(file, source)
  const onHook = sf.statements.some(
    (s) =>
      ts.isImportDeclaration(s) &&
      s.importClause?.namedBindings !== undefined &&
      ts.isNamedImports(s.importClause.namedBindings) &&
      s.importClause.namedBindings.elements.some((e) => (e.propertyName ?? e.name).text === HOOK),
  )
  const names = dataNames(sf)
  const hits: Hit[] = []
  if (names.size === 0) return { hits, onHook }
  const nameList = [...names]
  // `data`, `policy?.enabled` and `q.data` all read query data.
  const dep = (e: ts.Expression) => mentions(e, names) || nameList.some((nm) => nm.includes('.') && e.getText(sf).includes(nm))
  const walk = (n: ts.Node) => {
    if (ts.isCallExpression(n) && (calleeName(n) === 'useEffect' || calleeName(n) === 'useLayoutEffect')) {
      const [fn, deps] = n.arguments
      if (fn && deps && ts.isArrayLiteralExpression(deps) && deps.elements.some(dep) && containsSetterCall(fn)) {
        hits.push({ file, line: lineOf(n), shape: 'EFFECT' })
      }
    }
    if (ts.isIfStatement(n) && ts.isBinaryExpression(n.expression)) {
      const op = n.expression.operatorToken.kind
      const cmp = op === ts.SyntaxKind.ExclamationEqualsEqualsToken || op === ts.SyntaxKind.ExclamationEqualsToken
      if (cmp && (dep(n.expression.left) || dep(n.expression.right)) && containsSetterCall(n.thenStatement)) {
        hits.push({ file, line: lineOf(n), shape: 'RENDER' })
      }
    }
    ts.forEachChild(n, walk)
  }
  walk(sf)
  return { hits, onHook }
}

/** Hits in files that do not import the hook, across a path -> source map. */
function scan(files: Record<string, string>): Hit[] {
  return Object.entries(files)
    .filter(([path]) => !isTestFile(path))
    .flatMap(([path, source]) => {
      const { hits, onHook } = scanFile(path, source)
      return onHook ? [] : hits
    })
}

const describeHits = (hits: Hit[]) => hits.map((h) => `${h.file}:${h.line} ${h.shape}`)

const effectEditor = `
  import { useEffect, useState } from 'react'
  import { useThing } from '@/hooks/useThing'
  export function Bad() {
    const { data } = useThing()
    const [name, setName] = useState('')
    useEffect(() => {
      if (data) setName(data.name)
    }, [data])
    return <input value={name} onChange={(e) => setName(e.target.value)} />
  }`

describe('editor hydration guard (agent-os-auj3)', () => {
  it('every file that copies query data into state is on useExternalFileChange or named in ALLOWED', () => {
    const unexplained = scan(sources).filter((h) => !(h.file in ALLOWED))
    expect(describeHits(unexplained)).toEqual([])
  })

  it('a planted editor added to the real tree is flagged beside the allowed files', () => {
    const planted = scan({ ...sources, '/src/components/PlantedEditor.tsx': effectEditor })
    expect(describeHits(planted.filter((h) => !(h.file in ALLOWED)))).toEqual([
      '/src/components/PlantedEditor.tsx:7 EFFECT',
    ])
  })

  it('ALLOWED holds only files that still hit, each with a reason', () => {
    const hitFiles = new Set(scan(sources).map((h) => h.file))
    for (const [file, reason] of Object.entries(ALLOWED)) {
      expect(reason.length, `${file} needs a reason`).toBeGreaterThan(20)
      expect(hitFiles.has(file), `${file} no longer hits; remove it from ALLOWED`).toBe(true)
    }
  })

  it('the editors known to use the hook import it (the arm that clears a file is live)', () => {
    for (const path of ON_HOOK) {
      expect(sources[path], `${path} missing from the glob`).toBeDefined()
      expect(scanFile(path, sources[path]).onHook, `${path} must import ${HOOK}`).toBe(true)
    }
  })

  describe('planted violations are flagged (in memory, nothing is written to disk)', () => {

    it('an effect on [data] that sets state, no hook', () => {
      const hits = scan({ '/src/components/Planted.tsx': effectEditor })
      expect(describeHits(hits)).toEqual(['/src/components/Planted.tsx:7 EFFECT'])
    })

    it('an aliased destructure and a derived name are still data', () => {
      const hits = scan({
        '/src/components/Planted.tsx': `
          export function Bad() {
            const { data: envData } = useEnv()
            const vars = envData?.vars ?? []
            useEffect(() => { setRows(vars) }, [vars])
          }`,
      })
      expect(hits).toHaveLength(1)
    })

    it('data read as .data off a hook result', () => {
      const hits = scan({
        '/src/components/Planted.tsx': `
          export function Bad() {
            const q = useEnv()
            useEffect(() => { setRows(q.data.vars) }, [q.data])
          }`,
      })
      expect(hits).toHaveLength(1)
    })

    it('the render-time shape: if (data !== prev) setX(data)', () => {
      const hits = scan({
        '/src/components/Planted.tsx': `
          export function Bad() {
            const { data } = useEnv()
            const [prev, setPrev] = useState(undefined)
            if (data !== prev) {
              setPrev(data)
              setRows(data.vars)
            }
          }`,
      })
      expect(describeHits(hits)).toEqual(['/src/components/Planted.tsx:5 RENDER'])
    })

    it('the shape agent-os-lqcw removed from useGlobalEnvVars (recall over the known pre-fix site)', () => {
      const hits = scan({
        '/src/components/settings/global-env/Prefix.ts': `
          export function useGlobalEnvVars(setVisible) {
            const { data } = useGlobalEnv()
            const [vars, setVars] = useState([])
            const [dirty, setDirty] = useState(false)
            useEffect(() => {
              if (!data) return
              setVars(data.vars ?? [])
              setVisible({})
              setDirty(false)
            }, [data, setVisible])
          }`,
      })
      expect(hits).toHaveLength(1)
    })
  })

  describe('shapes that are not hydration are not flagged', () => {
    it('importing useExternalFileChange clears the file', () => {
      const hits = scan({
        '/src/components/Ok.tsx': `
          import { useExternalFileChange } from '@/hooks/useExternalFileChange'
          export function Ok() {
            const { data } = useEnv()
            useEffect(() => { setRows(data.vars) }, [data])
          }`,
      })
      expect(hits).toEqual([])
    })

    it('an effect on data that sets no state', () => {
      const hits = scan({
        '/src/components/Ok.tsx': `
          export function Ok() {
            const { data } = useEnv()
            useEffect(() => { document.title = data.name }, [data])
          }`,
      })
      expect(hits).toEqual([])
    })

    it('an effect that sets state but does not depend on data', () => {
      const hits = scan({
        '/src/components/Ok.tsx': `
          export function Ok() {
            const { data } = useEnv()
            useEffect(() => { setOpen(false) }, [route])
          }`,
      })
      expect(hits).toEqual([])
    })

    it('a test file is not scanned', () => {
      const hits = scan({ '/src/components/__tests__/Planted.test.tsx': effectEditor })
      expect(hits).toEqual([])
    })
  })
})
