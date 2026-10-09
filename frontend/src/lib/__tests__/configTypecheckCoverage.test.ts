/**
 * Guard: every typed tool config is inside some tsconfig include (agent-os-oava,
 * same class as agent-os-jzw6).
 *
 * A *.config.ts that no tsconfig lists is never type-checked, so an option that
 * does not exist in the tool it configures is never rejected:
 * playwright.config.ts carried `screenshotsPath` (jzw6) and frontend/vitest.config.ts
 * sat outside every include. `tsc -b` only checks what an include names.
 *
 * Rule. A "config" is a file named *.config.ts, *.config.mts or *.config.cts
 * anywhere in the repo, skipping node_modules, every dot-directory (.git,
 * .claude/worktrees, ...), dist, build and coverage. It is "covered" when it is
 * in the resolved file list of ANY tsconfig*.json in the repo. The list is what
 * TypeScript itself resolves (ts.parseJsonConfigFileContent), so include,
 * files, exclude and extends all count, and a tsconfig that only holds
 * `references` covers nothing of its own. The test fails naming every config
 * that is not covered.
 *
 * Self-tests, so a green run is not an instrument that never ran:
 *  - RED: a temp fixture whose tsconfig include misses a *.config.ts must be
 *    reported; the same fixture with the file included must not be.
 *  - RECALL: the real tsconfig.node.json with the include list it had before
 *    agent-os-oava (["vite.config.ts"]) must name frontend/vitest.config.ts.
 *  Fixtures live under os.tmpdir() and are removed; nothing is written to the repo.
 *
 * BLIND SPOTS, stated so a green run is not read as more than it is:
 *  1. "Any tsconfig" is the cover rule. A tsconfig that no script or CI step
 *     ever runs (not reachable from frontend/tsconfig.json references, not passed
 *     to `tsc -p`) still counts as covering, because this test cannot read
 *     package.json scripts or workflow files. A config listed only there is
 *     not actually checked in CI.
 *  2. Covered means "in the program", not "checked with the right options": a
 *     tsconfig with `checkJs` off, or `skipLibCheck` hiding a tool's own types,
 *     still counts.
 *  3. Only TypeScript configs are looked for. eslint.config.js and other .js
 *     configs have no type check to fall out of; configs with other names
 *     (knip.jsonc, components.json) are not TypeScript.
 *  4. Dot-directories are skipped on purpose (worktrees and tool caches hold
 *     copies), so a config placed inside one is not seen.
 */
/// <reference types="node" />
import { mkdirSync, mkdtempSync, readdirSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { describe, it, expect } from 'vitest'
import ts from 'typescript'

// frontend/src/lib/__tests__ -> repo root
const REPO_ROOT = path.resolve(import.meta.dirname, '../../../..')

const CONFIG_FILE = /\.config\.(ts|mts|cts)$/
const TSCONFIG_FILE = /^tsconfig(\..+)?\.json$/
const SKIPPED_DIRS = new Set(['node_modules', 'dist', 'build', 'coverage'])

// Include list frontend/tsconfig.node.json had before agent-os-oava.
const PRE_FIX_NODE_INCLUDE = ['vite.config.ts']

/** Files matching `pattern` under root; symlinks are not followed. */
function walk(root: string, pattern: RegExp): string[] {
  const found: string[] = []
  const visit = (dir: string) => {
    for (const entry of readdirSync(dir, { withFileTypes: true })) {
      if (entry.isDirectory()) {
        if (!entry.name.startsWith('.') && !SKIPPED_DIRS.has(entry.name)) visit(path.join(dir, entry.name))
      } else if (entry.isFile() && pattern.test(entry.name)) {
        found.push(path.join(dir, entry.name))
      }
    }
  }
  visit(root)
  return found.sort()
}

/** Absolute files one tsconfig resolves, with an optional replacement include list. */
function resolvedFiles(tsconfigPath: string, include?: string[]): string[] {
  const read = ts.readConfigFile(tsconfigPath, ts.sys.readFile)
  if (read.error) throw new Error(`cannot read ${tsconfigPath}: ${ts.flattenDiagnosticMessageText(read.error.messageText, '\n')}`)
  const config = include ? { ...read.config, include } : read.config
  return ts.parseJsonConfigFileContent(config, ts.sys, path.dirname(tsconfigPath), undefined, tsconfigPath).fileNames.map((f) => path.resolve(f))
}

/** Config files under root that no tsconfig under root resolves. */
function uncoveredConfigs(root: string, overrides: Record<string, string[]> = {}): string[] {
  const covered = new Set<string>()
  for (const tsconfig of walk(root, TSCONFIG_FILE)) {
    for (const f of resolvedFiles(tsconfig, overrides[tsconfig])) covered.add(f)
  }
  return walk(root, CONFIG_FILE).filter((f) => !covered.has(path.resolve(f)))
}

function withFixture(files: Record<string, string>, run: (root: string) => void) {
  const root = mkdtempSync(path.join(tmpdir(), 'config-coverage-'))
  try {
    for (const [name, text] of Object.entries(files)) {
      mkdirSync(path.dirname(path.join(root, name)), { recursive: true })
      writeFileSync(path.join(root, name), text)
    }
    run(root)
  } finally {
    rmSync(root, { recursive: true, force: true })
  }
}

const rel = (files: string[], root: string) => files.map((f) => path.relative(root, f))

describe('every *.config.ts is in a tsconfig include (agent-os-oava)', () => {
  it('finds at least the configs this guard was written for', () => {
    // A walk that finds nothing would make every other assertion vacuous.
    const found = rel(walk(REPO_ROOT, CONFIG_FILE), REPO_ROOT)
    expect(found).toEqual(expect.arrayContaining(['frontend/vite.config.ts', 'frontend/vitest.config.ts', 'playwright.config.ts']))
    expect(rel(walk(REPO_ROOT, TSCONFIG_FILE), REPO_ROOT)).toEqual(expect.arrayContaining(['frontend/tsconfig.node.json']))
  })

  it('covers every config on the real tree', () => {
    expect(rel(uncoveredConfigs(REPO_ROOT), REPO_ROOT)).toEqual([])
  })

  it('RED control: a config outside every include is reported, and cleared once included', () => {
    const files = {
      'a/tsconfig.json': JSON.stringify({ include: ['b.ts'] }),
      'a/b.ts': 'export const b = 1\n',
      'a/tool.config.ts': 'export default {}\n',
      'a/tool2.config.mts': 'export default {}\n',
    }
    withFixture(files, (root) => {
      expect(rel(uncoveredConfigs(root), root)).toEqual(['a/tool.config.ts', 'a/tool2.config.mts'])
    })
    withFixture({ ...files, 'a/tsconfig.json': JSON.stringify({ include: ['b.ts', '*.config.ts', '*.config.mts'] }) }, (root) => {
      expect(rel(uncoveredConfigs(root), root)).toEqual([])
    })
  })

  it('RED control: exclude, references-only tsconfigs and node_modules / dot-dirs behave as documented', () => {
    withFixture(
      {
        'tsconfig.json': JSON.stringify({ files: [], references: [{ path: './pkg' }] }),
        'pkg/tsconfig.json': JSON.stringify({ include: ['.'], exclude: ['excluded.config.ts'] }),
        'pkg/excluded.config.ts': 'export default {}\n',
        'pkg/covered.config.ts': 'export default {}\n',
        'pkg/node_modules/dep/dep.config.ts': 'export default {}\n',
        '.hidden/hidden.config.ts': 'export default {}\n',
        'loose.config.ts': 'export default {}\n',
      },
      (root) => {
        // Only the exclude and the file the root tsconfig never lists are reported.
        expect(rel(uncoveredConfigs(root), root)).toEqual(['loose.config.ts', 'pkg/excluded.config.ts'])
      },
    )
  })

  it('RECALL: the include list before agent-os-oava leaves frontend/vitest.config.ts uncovered', () => {
    const nodeConfig = path.join(REPO_ROOT, 'frontend/tsconfig.node.json')
    const named = rel(uncoveredConfigs(REPO_ROOT, { [nodeConfig]: PRE_FIX_NODE_INCLUDE }), REPO_ROOT)
    expect(named).toContain('frontend/vitest.config.ts')
    expect(named).not.toContain('frontend/vite.config.ts')
  })
})
