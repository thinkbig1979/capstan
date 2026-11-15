import { readdirSync, readFileSync } from 'node:fs'
import { join, relative, resolve, sep } from 'node:path'

const SRC = resolve(import.meta.dirname, '..')

/**
 * Every `.ts`/`.tsx` file under src, keyed `/src/...`, read from disk when called
 * (agent-os-3j4i). Source guards take their file list from here, not from
 * `import.meta.glob`: the glob is expanded when the test module is transformed,
 * and vitest's transform cache reuses that expansion, so a file added since the
 * last run was invisible to a guard until the guard file itself changed.
 */
export function readSourceFiles(): Record<string, string> {
  const out: Record<string, string> = {}
  for (const entry of readdirSync(SRC, { recursive: true, withFileTypes: true })) {
    if (!entry.isFile() || !/\.tsx?$/.test(entry.name)) continue
    const abs = join(entry.parentPath, entry.name)
    out[`/src/${relative(SRC, abs).split(sep).join('/')}`] = readFileSync(abs, 'utf8')
  }
  return out
}
