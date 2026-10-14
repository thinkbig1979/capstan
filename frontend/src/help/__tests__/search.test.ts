import { describe, it, expect } from 'vitest'
import { loadHelpLibrary, parseHelpDoc, type HelpDoc } from '../content'
import { DEVELOPER_DOCS, searchHelp } from '../search'

const doc = (id: string, md: string) => parseHelpDoc(`../../../docs/${id}.md`, md)

const FIXTURE: HelpDoc[] = [
  doc(
    'how-to/alpha',
    [
      '# Alpha guide',
      '## Body only',
      'The zebra crosses the road.',
      '## Zebra crossing',
      'Nothing about the animal here.',
      '## Two words',
      'The kiwi is green and the plum is purple.',
      '## Prefix',
      'Configuring things.',
      '## Fenced',
      '```sh',
      '## not-a-heading zebra',
      '```',
    ].join('\n'),
  ),
]

describe('searchHelp', () => {
  it('returns nothing for an empty or whitespace query', () => {
    expect(searchHelp(FIXTURE, '')).toEqual([])
    expect(searchHelp(FIXTURE, '   ')).toEqual([])
  })

  it('ANDs the query tokens: every token must match the section', () => {
    expect(searchHelp(FIXTURE, 'kiwi plum').map((r) => r.heading)).toEqual(['Two words'])
    expect(searchHelp(FIXTURE, 'kiwi zebra')).toEqual([])
  })

  it('matches a token as a prefix of a word, not as a substring', () => {
    expect(searchHelp(FIXTURE, 'configur').map((r) => r.heading)).toEqual(['Prefix'])
    expect(searchHelp(FIXTURE, 'figuring')).toEqual([])
  })

  it('ranks a heading hit above a body-only hit', () => {
    expect(searchHelp(FIXTURE, 'zebra').map((r) => r.heading)).toEqual(['Zebra crossing', 'Body only', 'Fenced'])
  })

  it('does not turn a # line inside a code fence into a result of its own', () => {
    const headings = searchHelp(FIXTURE, 'zebra').map((r) => r.heading)
    expect(headings).not.toContain('not-a-heading zebra')
    // The fenced line is code text of its section, so the section still matches.
    expect(headings).toContain('Fenced')
  })

  it('bounds the snippet and centres it on the first body hit', () => {
    const long = `${'lorem ipsum '.repeat(40)}needle ${'dolor sit '.repeat(40)}`
    const [hit] = searchHelp([doc('how-to/long', `# Long\n## Section\n${long}`)], 'needle')
    expect(hit.snippet.length).toBeLessThanOrEqual(120)
    expect(hit.snippet).toContain('needle')
  })

  it('strips markdown syntax from the snippet and keeps code text', () => {
    const [hit] = searchHelp(
      [doc('how-to/md', '# Md\n## Sec\nSee [the **guide**](https://x.example/y) and run `restic check`.')],
      'restic',
    )
    expect(hit.snippet).toBe('See the guide and run restic check.')
  })

  it('returns at most 20 results', () => {
    const many = doc('how-to/many', ['# Many', ...Array.from({ length: 30 }, (_, i) => `## Item ${i}\nshared`)].join('\n'))
    expect(searchHelp([many], 'shared')).toHaveLength(20)
  })

  it('carries the doc and anchor needed to open the section', () => {
    const [hit] = searchHelp(FIXTURE, 'kiwi')
    expect(hit).toMatchObject({ docId: 'how-to/alpha', docTitle: 'Alpha guide', anchor: 'two-words', heading: 'Two words' })
  })
})

describe('searchHelp over the bundled docs', () => {
  it('rclone lists "Cloud sync (optional)" in Configuring Backups', async () => {
    const results = searchHelp(await loadHelpLibrary(), 'rclone')
    expect(results.some((r) => r.heading === 'Cloud sync (optional)' && r.docTitle === 'Configuring Backups')).toBe(true)
  })

  // D93-1: operators meet developer docs last, whatever the score.
  it('backup: every reference/api and explanation/architecture result comes after every other result', async () => {
    const results = searchHelp(await loadHelpLibrary(), 'backup', Infinity)
    const isDev = results.map((r) => DEVELOPER_DOCS.has(r.docId))
    expect(isDev.some(Boolean)).toBe(true)
    expect(isDev.some((d) => !d)).toBe(true)
    expect(isDev.lastIndexOf(false)).toBeLessThan(isDev.indexOf(true))
  })

  it('a developer-doc heading hit still ranks after an operator body-only hit (control)', () => {
    const results = searchHelp(
      [
        doc('reference/api', '# API Reference\n## Backup\nbackup backup backup'),
        doc('how-to/ops', '# Ops\n## Cleanup\nOne mention of backup.'),
      ],
      'backup',
    )
    expect(results.map((r) => r.docId)).toEqual(['how-to/ops', 'reference/api'])
  })
})
