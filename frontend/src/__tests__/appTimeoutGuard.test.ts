/**
 * Guard for agent-os-fczk: every async wait in a test that renders <App /> names
 * its own timeout.
 *
 * Testing Library's findBy and waitFor default is 1000ms. The first full render of
 * <App /> (boot probes, route tree, redirect) took 1798ms on a loaded CI runner,
 * so an App-level wait that leans on the default is a flake waiting for a busy
 * runner (agent-os-g0yl; the budget is APP_FIND_TIMEOUT in src/test/timeouts.ts).
 * Nothing kept a NEW App test off the default, so this does.
 *
 * Scope: every *.test.tsx under src whose text contains `<App` followed by a
 * space, `/` or `>` (the bead's own discovery grep). In those files:
 *  - findBy and findAllBy calls need an object literal with a `timeout` key as the THIRD
 *    argument (query, queryOptions, waitForOptions);
 *  - waitFor/waitForElementToBeRemoved need one as the SECOND.
 * A timeout in the wrong slot (`findByText(x, { timeout: 5000 })`) is ignored by
 * Testing Library, so it is a hit. Any literal value passes, not only
 * APP_FIND_TIMEOUT: the invariant is "a budget was chosen", not which one. An
 * options argument that is not an object literal (a variable, a spread) cannot
 * be followed, so it is a hit, never a pass.
 *
 * Finding no App test file, or no wait inside them, is a failure: it means the
 * discovery stopped matching, not that the tree is clean.
 *
 * BLIND SPOTS: syntax only. A wait imported under another name
 * (`import { waitFor as w }`) or reached through a wrapper helper is not seen.
 * Only the names above are checked; `act`, `expect.poll` and userEvent's own
 * waits are not.
 */
import { describe, it, expect } from 'vitest'
import ts from 'typescript'

// Callee name -> index of the argument that carries the wait options.
function optionsSlot(name: string): number | undefined {
  if (/^find(All)?By[A-Z]\w*$/.test(name)) return 2
  if (name === 'waitFor' || name === 'waitForElementToBeRemoved') return 1
  return undefined
}

function calleeName(call: ts.CallExpression): string | undefined {
  const e = call.expression
  if (ts.isIdentifier(e)) return e.text
  if (ts.isPropertyAccessExpression(e)) return e.name.text
  return undefined
}

function hasTimeoutKey(obj: ts.ObjectLiteralExpression): boolean {
  return obj.properties.some(
    (p) =>
      (ts.isPropertyAssignment(p) || ts.isShorthandPropertyAssignment(p)) &&
      (ts.isIdentifier(p.name) || ts.isStringLiteral(p.name)) &&
      p.name.text === 'timeout',
  )
}

interface FileScan {
  calls: number
  hits: string[]
}

function scanSource(file: string, source: string): FileScan {
  const sf = ts.createSourceFile(file, source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX)
  const out: FileScan = { calls: 0, hits: [] }
  const walk = (n: ts.Node) => {
    if (ts.isCallExpression(n)) {
      const name = calleeName(n)
      const slot = name === undefined ? undefined : optionsSlot(name)
      if (name !== undefined && slot !== undefined) {
        out.calls++
        const at = `${file}:${sf.getLineAndCharacterOfPosition(n.getStart(sf)).line + 1}`
        const arg = n.arguments[slot]
        if (!arg) {
          out.hits.push(`${at} ${name}() has no argument ${slot + 1} (its wait options), so it runs on the 1000ms default`)
        } else if (!ts.isObjectLiteralExpression(arg)) {
          out.hits.push(`${at} ${name}() argument ${slot + 1} is not an object literal, so the guard cannot see its timeout`)
        } else if (!hasTimeoutKey(arg)) {
          out.hits.push(`${at} ${name}() argument ${slot + 1} has no timeout key, so it runs on the 1000ms default`)
        }
      }
    }
    ts.forEachChild(n, walk)
  }
  walk(sf)
  return out
}

// Scans the App-level files among `sources` (path -> text). Throws when
// discovery finds nothing, so an empty guard is an error and not a pass.
function checkAppTests(sources: Record<string, string>): { files: string[]; calls: number; hits: string[] } {
  const files = Object.keys(sources)
    .filter((f) => /<App[ />]/.test(sources[f]))
    .sort()
  if (files.length === 0) {
    throw new Error('no App-level test files found (a *.test.tsx containing <App />): the discovery pattern stopped matching or the tests moved')
  }
  let calls = 0
  const hits: string[] = []
  for (const f of files) {
    const s = scanSource(f, sources[f])
    calls += s.calls
    hits.push(...s.hits)
  }
  if (calls === 0) {
    throw new Error(`found ${files.length} App-level test file(s) but no findBy*/waitFor call in any: the scanner stopped matching`)
  }
  return { files, calls, hits }
}

const realSources = import.meta.glob<string>('/src/**/*.test.tsx', { query: '?raw', import: 'default', eager: true })

// A minimal App test around `body`, so each control stays one line of intent.
const appTest = (body: string) => `import { render, screen, waitFor } from '@testing-library/react'
import { APP_FIND_TIMEOUT } from '@/test/timeouts'
it('x', async () => {
  render(<App />)
${body}
})
`
const hitsFor = (body: string) => checkAppTests({ '/src/fixture.test.tsx': appTest(body) }).hits

describe('App-level waits name their own timeout (agent-os-fczk)', () => {
  it('holds for every App test file in the repo', () => {
    const { files, calls, hits } = checkAppTests(realSources)
    expect(hits).toEqual([])
    // Discovery and scanner both fired on the real tree.
    expect(files.length).toBeGreaterThan(0)
    expect(calls).toBeGreaterThan(0)
  })

  describe('RED controls: the guard flags each shape', () => {
    it('single-line findBy* with no options', () => {
      expect(hitsFor(`  await screen.findByText('hi')`)).toHaveLength(1)
    })

    it('multi-line findBy* with no options', () => {
      expect(
        hitsFor(`  expect(
    await screen.findByText(
      'a long message that gets wrapped',
    ),
  ).toBeInTheDocument()`),
      ).toHaveLength(1)
    })

    it('findBy* with an empty query-options object and no wait options', () => {
      expect(hitsFor(`  await screen.findByTestId('x', {})`)).toHaveLength(1)
    })

    it('findBy* with wait options that lack a timeout', () => {
      expect(hitsFor(`  await screen.findByTestId('x', {}, {})`)).toHaveLength(1)
      expect(hitsFor(`  await screen.findByTestId('x', {}, { interval: 50 })`)).toHaveLength(1)
    })

    it('findBy* with the timeout in the query-options slot, where Testing Library ignores it', () => {
      expect(hitsFor(`  await screen.findByText('hi', { timeout: 5000 })`)).toHaveLength(1)
    })

    it('findAllBy* with no options', () => {
      expect(hitsFor(`  await screen.findAllByRole('row')`)).toHaveLength(1)
    })

    it('waitFor with no options, and with options that lack a timeout', () => {
      expect(hitsFor(`  await waitFor(() => expect(1).toBe(1))`)).toHaveLength(1)
      expect(hitsFor(`  await waitFor(() => expect(1).toBe(1), {})`)).toHaveLength(1)
      expect(hitsFor(`  await waitFor(() => expect(1).toBe(1), { interval: 10 })`)).toHaveLength(1)
    })

    it('waitFor with a multi-line callback and no options', () => {
      expect(
        hitsFor(`  await waitFor(() => {
    expect(mock).toHaveBeenCalled()
  })`),
      ).toHaveLength(1)
    })

    it('options passed as a variable or a spread, which cannot be followed', () => {
      expect(hitsFor(`  await screen.findByText('hi', {}, opts)`)).toHaveLength(1)
      expect(hitsFor(`  await waitFor(() => expect(1).toBe(1), opts)`)).toHaveLength(1)
      expect(hitsFor(`  await waitFor(() => expect(1).toBe(1), { ...opts })`)).toHaveLength(1)
    })

    it('reports each bad call, not only the first', () => {
      const hits = hitsFor(`  await screen.findByText('a')
  await screen.findByText('b', {}, { timeout: 5000 })
  await screen.findByText('c')`)
      expect(hits).toHaveLength(2)
      expect(hits[0]).toMatch(/fixture\.test\.tsx:\d+ findByText/)
    })
  })

  describe('GREEN controls: the guard lets a named timeout through', () => {
    it('single-line and multi-line findBy* with a literal or the shared constant', () => {
      expect(hitsFor(`  await screen.findByText('hi', {}, { timeout: 5000 })`)).toEqual([])
      expect(hitsFor(`  await screen.findByTestId('x', {}, { timeout: APP_FIND_TIMEOUT })`)).toEqual([])
      expect(
        hitsFor(`  expect(
    await screen.findByText(
      'a long message that gets wrapped',
      {},
      { timeout: APP_FIND_TIMEOUT },
    ),
  ).toBeInTheDocument()`),
      ).toEqual([])
    })

    it('findBy* with a name option and a timeout, and a shorthand timeout', () => {
      expect(hitsFor(`  await screen.findByRole('button', { name: 'Go' }, { timeout: 3000 })`)).toEqual([])
      expect(hitsFor(`  const timeout = 3000\n  await screen.findByText('hi', {}, { timeout })`)).toEqual([])
    })

    it('waitFor with a timeout, one-line and multi-line', () => {
      expect(hitsFor(`  await waitFor(() => expect(1).toBe(1), { timeout: APP_FIND_TIMEOUT })`)).toEqual([])
      expect(
        hitsFor(`  await waitFor(
    () => {
      expect(mock).toHaveBeenCalled()
    },
    { timeout: 5000 },
  )`),
      ).toEqual([])
    })

    it('a bad-looking call that only appears in a comment or a string is not a call', () => {
      expect(
        hitsFor(`  // await screen.findByText('hi')
  const s = "await screen.findByText('hi')"
  await screen.findByText('ok', {}, { timeout: 5000 })`),
      ).toEqual([])
    })

    it('waits in a file that does not render <App /> are out of scope', () => {
      const r = checkAppTests({
        '/src/other.test.tsx': `it('x', async () => { await screen.findByText('hi') })`,
        '/src/app.test.tsx': appTest(`  await screen.findByText('ok', {}, { timeout: 5000 })`),
      })
      expect(r.files).toEqual(['/src/app.test.tsx'])
      expect(r.hits).toEqual([])
    })
  })

  describe('ERROR controls: an empty guard is not a pass', () => {
    it('throws when discovery finds no App test file', () => {
      expect(() => checkAppTests({})).toThrow(/no App-level test files found/)
      expect(() => checkAppTests({ '/src/other.test.tsx': `it('x', () => { render(<Dashboard />) })` })).toThrow(
        /no App-level test files found/,
      )
    })

    it('throws when App test files hold no wait at all', () => {
      expect(() => checkAppTests({ '/src/a.test.tsx': `it('x', () => { render(<App />) })` })).toThrow(/no findBy\*\/waitFor call/)
    })
  })
})
