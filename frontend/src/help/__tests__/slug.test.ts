/**
 * slugify() must produce the same anchor as slugify() in scripts/check-docs.sh,
 * because Docs CI validates every `#anchor` link with that function and the
 * in-app help resolves the same anchors with this one. If the two disagree, a
 * link passes Docs CI and then lands on "section not found" in the app.
 *
 * The parity arm runs the bash function itself (extracted from the script) over
 * every heading in docs/, so a change to either side fails here.
 */
import { spawnSync } from 'node:child_process'
import { readdirSync, readFileSync } from 'node:fs'
import path from 'node:path'
import { describe, expect, it } from 'vitest'
import { slugify } from '../slug'

const REPO_ROOT = path.resolve(import.meta.dirname, '../../../..')

describe('slugify', () => {
  it.each([
    ['Docker Socket & Security', 'docker-socket--security'],
    ['Cloud sync (optional)', 'cloud-sync-optional'],
    ['Backups & restic', 'backups--restic'],
  ])('%s -> %s', (heading, slug) => {
    expect(slugify(heading)).toBe(slug)
  })

  it('drops non-ASCII letters, as the bash version does', () => {
    expect(slugify('Café → Bar')).toBe('caf--bar')
  })

  it('matches check-docs.sh slugify on every heading in docs/', () => {
    const script = readFileSync(path.join(REPO_ROOT, 'scripts/check-docs.sh'), 'utf8')
    const fn = script.match(/^slugify\(\) \{\n[\s\S]*?\n\}$/m)?.[0]
    expect(fn, 'slugify() not found in scripts/check-docs.sh').toBeDefined()

    const docsDir = path.join(REPO_ROOT, 'docs')
    const headings = readdirSync(docsDir, { recursive: true, encoding: 'utf8' })
      .filter((f) => f.endsWith('.md'))
      .flatMap((f) => readFileSync(path.join(docsDir, f), 'utf8').split('\n'))
      .filter((line) => /^#+\s/.test(line))
      .map((line) => line.replace(/^#+\s*/, ''))
    expect(headings.length).toBeGreaterThan(50)

    const run = spawnSync('bash', ['-c', `${fn}\nwhile IFS= read -r h; do slugify "$h"; printf '\\n'; done`], {
      input: headings.join('\n') + '\n',
      encoding: 'utf8',
    })
    expect(run.status, run.stderr).toBe(0)
    const fromBash = run.stdout.split('\n').slice(0, headings.length)

    const mismatches = headings
      .map((h, i) => ({ heading: h, bash: fromBash[i], ts: slugify(h) }))
      .filter((m) => m.bash !== m.ts)
    expect(mismatches).toEqual([])
  })
})
