/**
 * Guard (agent-os-fgwm): no masking or visibility decision rests on the key name
 * alone. The server also flags a row for its VALUE (a private key under a
 * harmless name like TLS), and that flag lives only on `entry.sensitive`. A
 * decision made from `isSensitiveKey(key)` by itself drops it: renaming the key
 * unmasked the row, and the Visible checkbox revealed it without unlocking.
 *
 * Rule: every `isSensitiveKey(...)` call in a non-test file under src/ must be
 * one operand of a `||` or `&&`, after looking through parentheses and `!`.
 * A call whose first real parent is anything else (an assignment, a JSX
 * attribute, a return, a ternary branch or condition, a bare `if`, a variable
 * initialiser, a call argument) is the only input to that decision, and fails.
 *
 * BLIND SPOTS, stated so a green run is not read as more than it is:
 *  - Syntax only. `isSensitiveKey(k) || false` passes, and so does an operand
 *    that never reads the row's flag; the rule proves another input exists,
 *    not that it is the right one.
 *  - Only the name `isSensitiveKey` is seen: an alias (`import { isSensitiveKey
 *    as f }`) or a copy of the rule under another name is not.
 *  - Test files are not scanned.
 *  - The file list comes from import.meta.glob; see editorHydrationGuard.test.ts
 *    for the vitest cache note on files added after a previous run.
 */
import { describe, it, expect } from 'vitest'
import ts from 'typescript'

const FN = 'isSensitiveKey'

const sources = import.meta.glob<string>('/src/**/*.{ts,tsx}', { query: '?raw', import: 'default', eager: true })

const isTestFile = (file: string) => /\/__tests__\/|\.test\.tsx?$/.test(file)

function soleInputCalls(file: string, source: string): string[] {
  const sf = ts.createSourceFile(
    file,
    source,
    ts.ScriptTarget.Latest,
    true,
    file.endsWith('.tsx') ? ts.ScriptKind.TSX : ts.ScriptKind.TS,
  )
  const hits: string[] = []
  const walk = (n: ts.Node) => {
    if (ts.isCallExpression(n) && ts.isIdentifier(n.expression) && n.expression.text === FN) {
      let child: ts.Node = n
      let parent = n.parent
      while (
        ts.isParenthesizedExpression(parent) ||
        (ts.isPrefixUnaryExpression(parent) && parent.operator === ts.SyntaxKind.ExclamationToken)
      ) {
        child = parent
        parent = parent.parent
      }
      const logical =
        ts.isBinaryExpression(parent) &&
        (parent.operatorToken.kind === ts.SyntaxKind.BarBarToken ||
          parent.operatorToken.kind === ts.SyntaxKind.AmpersandAmpersandToken) &&
        (parent.left === child || parent.right === child)
      if (!logical) {
        hits.push(`${file}:${sf.getLineAndCharacterOfPosition(n.getStart()).line + 1}`)
      }
    }
    ts.forEachChild(n, walk)
  }
  walk(sf)
  return hits
}

function scan(files: Record<string, string>): string[] {
  return Object.entries(files)
    .filter(([file]) => !isTestFile(file))
    .flatMap(([file, source]) => soleInputCalls(file, source))
}

describe('isSensitiveKey is never the only input to a mask decision (agent-os-fgwm)', () => {
  it('controls: the scan flags a sole-input call and passes one operand of || or &&', () => {
    const flagged = scan({
      '/src/a.tsx': `
        export const A = ({ entry }) => <input disabled={isSensitiveKey(entry.key)} />
        export function b(row, value) { row.sensitive = isSensitiveKey(value) }
        export const c = (k) => (isSensitiveKey(k) ? 1 : 2)
        export const d = (k) => { const s = !isSensitiveKey(k); return s }
      `,
      '/src/b.ts': `
        export const ok1 = (e) => e.sensitive === true || isSensitiveKey(e.key)
        export const ok2 = (e) => !e.sensitive && (isSensitiveKey(e.key))
        export const ok3 = (e, seen) => !e.sensitive && (!isSensitiveKey(e.key) || seen)
      `,
      '/src/__tests__/c.test.ts': `export const t = isSensitiveKey('X')`,
    })
    expect(flagged).toEqual(['/src/a.tsx:2', '/src/a.tsx:3', '/src/a.tsx:4', '/src/a.tsx:5'])
  })

  it('the glob reaches the known call sites', () => {
    for (const path of [
      '/src/components/stack/env-editor/useEnvEntryActions.ts',
      '/src/components/stack/env-editor/useEnvUnlockRemask.ts',
      '/src/components/settings/global-env/GlobalEnvTable.tsx',
    ]) {
      expect(sources[path], `${path} missing from the glob`).toContain(`${FN}(`)
    }
  })

  it('no source file under src/ makes a mask decision from the key name alone', () => {
    expect(scan(sources)).toEqual([])
  })
})
