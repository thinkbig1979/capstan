import { readdirSync } from 'node:fs'
import path from 'node:path'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { buildHelpLibrary, loadHelpLibrary, parseHelpDoc } from '../content'

const DOCS_DIR = path.resolve(import.meta.dirname, '../../../../docs')

// The guides that existed when the loader was written, with their H1s. New docs
// (docs/help/*) may be added; these must stay loadable under these titles.
const KNOWN_DOCS: Record<string, string> = {
  'getting-started': 'Getting Started',
  'how-to/configure-backups': 'Configuring Backups',
  'how-to/deploy-production': 'Production Deployment',
  'how-to/migrate-from-dockge': 'Migration from Dockge',
  'how-to/recover-admin-access': 'Recovering Admin Access',
  'how-to/restore-a-backup': 'Restoring a Backup',
  'how-to/upgrade-and-roll-back': 'Upgrading and Rolling Back',
  'reference/api': 'API Reference',
  'reference/configuration': 'Configuration Reference',
  'explanation/architecture': 'Architecture',
  'explanation/security-model': 'Security Model',
}

describe('loadHelpLibrary (real bundled docs)', () => {
  it('bundles exactly the markdown files under docs/', async () => {
    const onDisk = readdirSync(DOCS_DIR, { recursive: true, encoding: 'utf8' })
      .filter((f) => f.endsWith('.md'))
      .map((f) => f.split(path.sep).join('/').replace(/\.md$/, ''))
      .sort()
    const docs = await loadHelpLibrary()
    expect(docs.map((d) => d.id).sort()).toEqual(onDisk)
  })

  it('parses each known guide with its H1 title', async () => {
    const byId = new Map((await loadHelpLibrary()).map((d) => [d.id, d]))
    for (const [id, title] of Object.entries(KNOWN_DOCS)) {
      expect(byId.get(id)?.title, id).toBe(title)
    }
  })

  it('groups docs by their top directory', async () => {
    const byId = new Map((await loadHelpLibrary()).map((d) => [d.id, d]))
    expect(byId.get('getting-started')?.group).toBe('start')
    expect(byId.get('how-to/configure-backups')?.group).toBe('how-to')
    expect(byId.get('reference/api')?.group).toBe('reference')
    expect(byId.get('explanation/architecture')?.group).toBe('explanation')
  })

  it('does not turn the fenced "# Via the UI" comment into a section', async () => {
    const doc = (await loadHelpLibrary()).find((d) => d.id === 'how-to/configure-backups')!
    const headings = doc.sections.map((s) => s.heading)
    expect(headings).toContain('Running a backup')
    expect(headings).toContain('Cloud sync (optional)')
    expect(headings.some((h) => h.startsWith('Via the'))).toBe(false)
    const cloud = doc.sections.find((s) => s.heading === 'Cloud sync (optional)')!
    expect(cloud).toMatchObject({ anchor: 'cloud-sync-optional', depth: 2, docId: 'how-to/configure-backups' })
  })

  it('memoises the library', () => {
    expect(loadHelpLibrary()).toBe(loadHelpLibrary())
  })
})

describe('parseHelpDoc', () => {
  const FENCED = [
    '# Title',
    '',
    'intro',
    '## Real one',
    '```bash',
    '## not a heading (backtick fence)',
    '# nor this',
    '```',
    '### Real two',
    '~~~',
    '### not a heading (tilde fence)',
    '```',
    '## still inside: a backtick line does not close a tilde fence',
    '~~~',
    '````md',
    '```',
    '## still inside: a shorter fence does not close a longer one',
    '````',
    '## Real three',
    'body',
  ].join('\n')

  it('splits at H2/H3 outside code fences only', () => {
    const doc = parseHelpDoc('../../../docs/how-to/x.md', FENCED)
    expect(doc.sections.map((s) => [s.depth, s.heading])).toEqual([
      [2, 'Real one'],
      [3, 'Real two'],
      [2, 'Real three'],
    ])
  })

  it('keeps fenced lines in the section text', () => {
    const doc = parseHelpDoc('../../../docs/how-to/x.md', FENCED)
    expect(doc.sections[0].text).toContain('## not a heading (backtick fence)')
    expect(doc.sections[2].text).toBe('body')
  })

  it('derives id, title and group from the path and H1', () => {
    expect(parseHelpDoc('../../../docs/how-to/x.md', FENCED)).toMatchObject({ id: 'how-to/x', title: 'Title', group: 'how-to' })
    expect(parseHelpDoc('../../../docs/help/settings.md', '# Settings help')).toMatchObject({ id: 'help/settings', group: 'help' })
    expect(parseHelpDoc('../../../docs/getting-started.md', '# Start')).toMatchObject({ id: 'getting-started', group: 'start' })
  })

  it('rejects a doc with no H1 or an unknown top directory', () => {
    expect(() => parseHelpDoc('../../../docs/how-to/x.md', '## no title')).toThrow(/no H1/)
    expect(() => parseHelpDoc('../../../docs/misc/x.md', '# T')).toThrow(/unknown group/)
  })
})

describe('buildHelpLibrary', () => {
  it('fails closed on an empty glob, so a build without docs/ cannot ship empty help', () => {
    expect(() => buildHelpLibrary({})).toThrow(/no docs bundled/)
  })
})

describe('loadHelpLibrary retry', () => {
  afterEach(() => {
    vi.doUnmock('../docs-raw')
    vi.resetModules()
  })

  it('forgets a failed load so the next call tries again', async () => {
    vi.resetModules()
    vi.doMock('../docs-raw', () => ({ RAW_DOCS: {} }))
    const first = await import('../content')
    await expect(first.loadHelpLibrary()).rejects.toThrow(/no docs bundled/)
    vi.doMock('../docs-raw', () => ({ RAW_DOCS: { '../../../docs/getting-started.md': '# Getting Started' } }))
    vi.resetModules()
    // Same module instance as `first`: a fresh import would hide a sticky rejection.
    await expect(first.loadHelpLibrary()).resolves.toHaveLength(1)
  })
})
