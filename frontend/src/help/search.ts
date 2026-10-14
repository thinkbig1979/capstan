import type { HelpDoc } from './content'

/**
 * Developer-facing docs: operators meet them last, in the guide index and in
 * search results, whatever the score (D93-1).
 */
export const DEVELOPER_DOCS: ReadonlySet<string> = new Set(['reference/api', 'explanation/architecture'])

export interface SearchResult {
  docId: string
  docTitle: string
  anchor: string
  heading: string
  /** About 120 characters of plain text around the first body hit. */
  snippet: string
}

const MAX_RESULTS = 20
const SNIPPET_LENGTH = 120
const SNIPPET_LEAD = 40

interface Entry extends Omit<SearchResult, 'snippet'> {
  titleWords: string[]
  headingWords: string[]
  bodyWords: string[]
  /** Plain body text, whitespace collapsed, for the snippet. */
  body: string
}

const WORD = /[\p{L}\p{N}]+/gu

function words(text: string): string[] {
  return text.toLowerCase().match(WORD) ?? []
}

/**
 * Markdown to plain text with regexes, no parser: links and images keep their
 * text, code keeps its text (without the fence lines and backticks), emphasis
 * markers and block syntax go. Underscores stay: they are inside identifiers
 * such as RESTIC_PASSWORD.
 */
function plain(markdown: string): string {
  return markdown
    .replace(/^ {0,3}(`{3,}|~{3,}).*$/gm, '')
    .replace(/!\[([^\]]*)\]\([^)]*\)/g, '$1')
    .replace(/\[([^\]]*)\]\([^)]*\)/g, '$1')
    .replace(/<[^>]+>/g, '')
    .replace(/^\s*\|?[\s:|-]+\|?\s*$/gm, '')
    .replace(/^#{1,6}[ \t]+/gm, '')
    .replace(/^\s*>[ \t]?/gm, '')
    .replace(/^\s*(?:[-*+]|\d+\.)[ \t]+/gm, '')
    .replace(/\*{1,2}(?=\S)([^*\n]*?\S)\*{1,2}/g, '$1')
    .replace(/`/g, '')
    .replace(/\|/g, ' ')
    .replace(/\s+/g, ' ')
    .trim()
}

// Built once per loaded library: loadHelpLibrary() hands every caller the same array.
const indexes = new WeakMap<HelpDoc[], Entry[]>()

function indexOf(docs: HelpDoc[]): Entry[] {
  let index = indexes.get(docs)
  if (!index) {
    index = docs.flatMap((doc) =>
      doc.sections.map((s) => {
        const body = plain(s.text)
        return {
          docId: doc.id,
          docTitle: doc.title,
          anchor: s.anchor,
          heading: s.heading,
          titleWords: words(doc.title),
          headingWords: words(s.heading),
          bodyWords: words(body),
          body,
        }
      }),
    )
    indexes.set(docs, index)
  }
  return index
}

const startsWithAny = (word: string, tokens: string[]) => tokens.some((t) => word.startsWith(t))

/** Distinct query tokens that prefix-match some word of the field. */
const tokensHit = (field: string[], tokens: string[]) => tokens.filter((t) => field.some((w) => w.startsWith(t))).length

function snippetFor(body: string, tokens: string[]): string {
  if (body.length <= SNIPPET_LENGTH) return body
  let hit = 0
  for (const m of body.toLowerCase().matchAll(WORD)) {
    if (startsWithAny(m[0], tokens)) {
      hit = m.index
      break
    }
  }
  let start = Math.max(0, hit - SNIPPET_LEAD)
  const end = Math.min(body.length, start + SNIPPET_LENGTH)
  start = Math.max(0, end - SNIPPET_LENGTH)
  let out = body.slice(start, end)
  if (start > 0) out = `…${out.slice(1)}`
  if (end < body.length) out = `${out.slice(0, -1)}…`
  return out
}

/**
 * Full-text search over the sections of the bundled docs. Query words are
 * ANDed and prefix-match; score = 3 x doc-title + 2 x heading + 1 x body hits,
 * counted as distinct query words so a heading hit always beats a body-only
 * hit. Developer docs sort after every other result (D93-1). `limit` is the
 * result cap; only tests lift it, to see the whole ordering.
 */
export function searchHelp(docs: HelpDoc[], query: string, limit = MAX_RESULTS): SearchResult[] {
  const tokens = [...new Set(words(query))]
  if (tokens.length === 0) return []

  const hits: { entry: Entry; dev: boolean; score: number; occurrences: number; order: number }[] = []
  indexOf(docs).forEach((entry, order) => {
    const title = tokensHit(entry.titleWords, tokens)
    const heading = tokensHit(entry.headingWords, tokens)
    const bodyHit = tokensHit(entry.bodyWords, tokens)
    const fields = [entry.titleWords, entry.headingWords, entry.bodyWords]
    const matchesAll = tokens.every((t) => fields.some((f) => f.some((w) => w.startsWith(t))))
    if (!matchesAll) return
    hits.push({
      entry,
      dev: DEVELOPER_DOCS.has(entry.docId),
      score: 3 * title + 2 * heading + bodyHit,
      occurrences: entry.bodyWords.filter((w) => startsWithAny(w, tokens)).length,
      order,
    })
  })

  hits.sort((a, b) => Number(a.dev) - Number(b.dev) || b.score - a.score || b.occurrences - a.occurrences || a.order - b.order)

  return hits.slice(0, limit).map(({ entry }) => ({
    docId: entry.docId,
    docTitle: entry.docTitle,
    anchor: entry.anchor,
    heading: entry.heading,
    snippet: snippetFor(entry.body, tokens),
  }))
}
