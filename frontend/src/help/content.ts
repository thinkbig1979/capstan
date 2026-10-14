import { slugify } from './slug'

export type HelpGroup = 'start' | 'help' | 'how-to' | 'reference' | 'explanation'

export interface HelpSection {
  docId: string
  /** slugify(heading); the in-app and Docs CI anchor. */
  anchor: string
  heading: string
  depth: 2 | 3
  /** The section's markdown body, up to the next H2/H3. */
  text: string
}

export interface HelpDoc {
  /** Path under docs/ without .md, e.g. "how-to/configure-backups". */
  id: string
  /** The first H1. */
  title: string
  group: HelpGroup
  markdown: string
  sections: HelpSection[]
}

const GROUP_BY_DIR: Record<string, HelpGroup> = {
  help: 'help',
  'how-to': 'how-to',
  reference: 'reference',
  explanation: 'explanation',
}

const FENCE = /^ {0,3}(`{3,}|~{3,})/
const HEADING = /^ {0,3}(#{1,6})[ \t]+(.*?)[ \t]*$/

/**
 * Parse one bundled doc. `path` is the glob key ("../../../docs/how-to/x.md").
 * Headings inside ``` or ~~~ fences are code (shell comments), not headings.
 * A fence closes only on the same character at least as long, per CommonMark.
 */
export function parseHelpDoc(path: string, markdown: string): HelpDoc {
  const id = path.replace(/^.*?\/docs\//, '').replace(/\.md$/, '')
  const slash = id.indexOf('/')
  const group = slash === -1 ? 'start' : GROUP_BY_DIR[id.slice(0, slash)]
  if (!group) throw new Error(`help doc ${id}: unknown group "${id.slice(0, slash)}"`)

  let title: string | undefined
  const sections: HelpSection[] = []
  let body: string[] = []
  let fence: string | null = null

  const flush = () => {
    const last = sections[sections.length - 1]
    if (last) last.text = body.join('\n').trim()
    body = []
  }

  for (const line of markdown.split('\n')) {
    const fenceMatch = FENCE.exec(line)
    if (fence) {
      if (fenceMatch && fenceMatch[1][0] === fence[0] && fenceMatch[1].length >= fence.length && line.trim() === fenceMatch[1]) {
        fence = null
      }
      body.push(line)
      continue
    }
    if (fenceMatch) {
      fence = fenceMatch[1]
      body.push(line)
      continue
    }
    const heading = HEADING.exec(line)
    const depth = heading?.[1].length
    if (heading && depth === 1 && title === undefined) {
      title = heading[2]
      continue
    }
    if (heading && (depth === 2 || depth === 3)) {
      flush()
      sections.push({ docId: id, anchor: slugify(heading[2]), heading: heading[2], depth, text: '' })
      continue
    }
    body.push(line)
  }
  flush()

  if (title === undefined) throw new Error(`help doc ${id}: no H1 title`)
  return { id, title, group, markdown, sections }
}

export function buildHelpLibrary(raw: Record<string, string>): HelpDoc[] {
  const docs = Object.entries(raw).map(([path, markdown]) => parseHelpDoc(path, markdown))
  // Fail closed: an empty glob means the build could not see docs/.
  if (docs.length === 0) throw new Error('no docs bundled: the build did not see docs/')
  return docs.sort((a, b) => a.id.localeCompare(b.id))
}

let library: Promise<HelpDoc[]> | null = null

/**
 * The bundled docs, parsed once. The docs live in a lazy chunk reached only
 * through this dynamic import. A failed load is forgotten so a Retry reloads.
 */
export function loadHelpLibrary(): Promise<HelpDoc[]> {
  library ??= import('./docs-raw')
    .then((m) => buildHelpLibrary(m.RAW_DOCS))
    .catch((err: unknown) => {
      library = null
      throw err
    })
  return library
}
