/**
 * Guard for the container-output sockets (agent-os-2oxy item 2, after
 * agent-os-rbmi): a dial of /ws/terminal or /ws/logs must pass
 * `sendUnlockToken: true`, or an unlocked session is served the locked view
 * (masked logs, a refused terminal) with nothing to say why.
 *
 * Every call or `new` expression in src whose first argument is a string or
 * template naming /ws/terminal or /ws/logs is a dial. Its options (any later
 * object-literal argument) must hold `sendUnlockToken: true`. The dials found
 * are pinned, so a new one is a visible edit here, and each is re-checked with
 * the flag removed IN MEMORY to prove the guard sees it.
 *
 * BLIND SPOTS: a path built in a variable before the call, or options passed
 * as a variable, is not followed; such a dial is not found, and the pin below
 * then fails because it no longer lists the site. Test files are not scanned.
 */
import { describe, it, expect } from 'vitest'
import ts from 'typescript'
import { readSourceFiles } from '@/test/sourceFiles'

const CONTAINER_WS = /\/ws\/(terminal|logs)\//

interface Dial {
  file: string
  path: string
  hasFlag: boolean
}

function dialsIn(file: string, text: string): Dial[] {
  const sf = ts.createSourceFile(file, text, ts.ScriptTarget.Latest, true, file.endsWith('.tsx') ? ts.ScriptKind.TSX : ts.ScriptKind.TS)
  const out: Dial[] = []
  const visit = (node: ts.Node) => {
    if ((ts.isCallExpression(node) || ts.isNewExpression(node)) && node.arguments && node.arguments.length > 0) {
      const args = node.arguments
      const first = args[0]
      // A ternary path (`sel ? \`/ws/terminal/...\` : ''`) names the route in a branch.
      const pathText = first.getText(sf)
      if ((ts.isStringLiteral(first) || ts.isNoSubstitutionTemplateLiteral(first) || ts.isTemplateExpression(first) || ts.isConditionalExpression(first)) && CONTAINER_WS.test(pathText)) {
        const hasFlag = args.slice(1).some(
          (a) =>
            ts.isObjectLiteralExpression(a) &&
            a.properties.some(
              (p) =>
                ts.isPropertyAssignment(p) &&
                p.name.getText(sf) === 'sendUnlockToken' &&
                p.initializer.kind === ts.SyntaxKind.TrueKeyword,
            ),
        )
        out.push({ file, path: pathText, hasFlag })
      }
    }
    ts.forEachChild(node, visit)
  }
  visit(sf)
  return out
}

function scan(files: Record<string, string>): Dial[] {
  return Object.entries(files)
    .filter(([f]) => !/\.test\.tsx?$/.test(f) && !f.includes('/__tests__/') && !f.startsWith('/src/test/'))
    .flatMap(([f, text]) => dialsIn(f, text))
}

// Measured on 999de7d.
const PINNED = [
  '/src/components/stack/logviewer/useLogStream.ts',
  '/src/components/stack/terminal/useTerminalSession.ts',
]

describe('container-output sockets send the unlock token (agent-os-2oxy)', () => {
  const files = readSourceFiles()
  const dials = scan(files)

  it('finds exactly the pinned dials', () => {
    expect(dials.map((d) => d.file).sort()).toEqual(PINNED)
  })

  it('every dial passes sendUnlockToken: true', () => {
    expect(dials.filter((d) => !d.hasFlag)).toEqual([])
  })

  it.each(PINNED)('flags %s with the flag removed', (file) => {
    const mutated = files[file].replace(/sendUnlockToken:\s*true,?/g, '')
    expect(mutated).not.toEqual(files[file])
    const found = dialsIn(file, mutated)
    expect(found.length).toBeGreaterThan(0)
    expect(found.every((d) => !d.hasFlag)).toBe(true)
  })
})
