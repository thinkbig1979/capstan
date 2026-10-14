import type { ReactNode } from 'react'
import Markdown, { type Components, type ExtraProps } from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { ExternalLink } from 'lucide-react'
import { slugify } from './slug'

/** Where an in-docs link leads: a bundled doc (optionally at a heading) or the guide index. */
type HelpLinkTarget = { doc: string; anchor?: string } | { kind: 'index' }

interface HelpMarkdownProps {
  markdown: string
  /** Path under docs/ without .md ("help/settings"); relative links resolve against it. */
  docId: string
  onNavigate: (target: HelpLinkTarget) => void
  /** Git ref for links to repo files outside docs/: the running release tag, or main. */
  repoRef?: string
}

const REPO_BLOB = 'https://github.com/thinkbig1979/capstan/blob/'
// A throwaway origin to resolve relative links against; only the path is kept.
const BASE_ORIGIN = 'https://repo.invalid'

type LinkKind =
  | { kind: 'navigate'; target: HelpLinkTarget }
  | { kind: 'external'; href: string }
  | { kind: 'text' }

function decodeAnchor(hash: string): string {
  try {
    return decodeURIComponent(hash.slice(1))
  } catch {
    return hash.slice(1)
  }
}

/**
 * The link policy. `href` has already been through react-markdown's
 * defaultUrlTransform, which empties javascript:, data: and other unsafe schemes.
 */
function classifyLink(href: string | undefined, docId: string, repoRef: string): LinkKind {
  if (!href) return { kind: 'text' }

  const scheme = /^([a-z][a-z0-9+.-]*):/i.exec(href)?.[1].toLowerCase()
  if (scheme) {
    return scheme === 'http' || scheme === 'https' || scheme === 'mailto'
      ? { kind: 'external', href }
      : { kind: 'text' }
  }

  const url = new URL(href, `${BASE_ORIGIN}/docs/${docId}.md`)
  // A protocol-relative "//host/x" leaves the repo: not a docs link.
  if (url.origin !== BASE_ORIGIN) return { kind: 'text' }

  const path = url.pathname.slice(1)
  const anchor = url.hash ? decodeAnchor(url.hash) : ''

  if (path === 'README.md' && anchor === 'documentation') {
    return { kind: 'navigate', target: { kind: 'index' } }
  }
  if (path.startsWith('docs/') && path.endsWith('.md')) {
    const doc = decodeURIComponent(path.slice('docs/'.length, -'.md'.length))
    return { kind: 'navigate', target: anchor ? { doc, anchor } : { doc } }
  }
  return { kind: 'external', href: `${REPO_BLOB}${repoRef}/${path}${url.hash}` }
}

const LINK_CLASS = 'text-primary underline underline-offset-2 hover:no-underline'

function HelpLink({
  href,
  docId,
  repoRef,
  onNavigate,
  children,
}: {
  href: string | undefined
  docId: string
  repoRef: string
  onNavigate: (target: HelpLinkTarget) => void
  children: ReactNode
}) {
  const link = classifyLink(href, docId, repoRef)
  if (link.kind === 'navigate') {
    return (
      <button type="button" className={LINK_CLASS} onClick={() => onNavigate(link.target)}>
        {children}
      </button>
    )
  }
  if (link.kind === 'external') {
    return (
      <a href={link.href} target="_blank" rel="noopener noreferrer" className={`${LINK_CLASS} inline-flex items-center gap-0.5`}>
        {children}
        <ExternalLink aria-hidden="true" className="h-3 w-3 shrink-0" />
      </a>
    )
  }
  return <span>{children}</span>
}

/**
 * The heading's anchor from its source line, so it matches the section anchors
 * content.ts derives from the same line (and Docs CI's slugify), not the
 * rendered text, which differs for headings holding links or entities.
 */
function headingAnchor(markdown: string, node: ExtraProps['node']): string | undefined {
  const start = node?.position?.start.offset
  const end = node?.position?.end.offset
  if (start === undefined || end === undefined) return undefined
  const line = markdown.slice(start, end).split('\n')[0]
  return slugify(line.replace(/^ {0,3}#{1,6}[ \t]+/, '').replace(/[ \t]+$/, ''))
}

/**
 * Renders a bundled help doc as React elements. No raw-HTML plugin: an HTML
 * tag in the source renders as its literal text, so no string ever becomes DOM.
 */
export function HelpMarkdown({ markdown, docId, onNavigate, repoRef = 'main' }: HelpMarkdownProps) {
  const components: Components = {
    a: ({ href, children }) => (
      <HelpLink href={href} docId={docId} repoRef={repoRef} onNavigate={onNavigate}>
        {children}
      </HelpLink>
    ),
    // CSP allows images from self and data: only, and docs are text: show the alt text as a link.
    img: ({ src, alt }) => (
      <HelpLink href={typeof src === 'string' ? src : undefined} docId={docId} repoRef={repoRef} onNavigate={onNavigate}>
        {alt || 'Image'}
      </HelpLink>
    ),
    h1: ({ children }) => <h1 className="mb-3 text-xl font-semibold text-foreground">{children}</h1>,
    // data-help-anchor + tabIndex let the panel scroll to and focus a section.
    h2: ({ node, children }) => (
      <h2 data-help-anchor={headingAnchor(markdown, node)} tabIndex={-1} className="mb-2 mt-6 text-lg font-semibold text-foreground focus:outline-none">
        {children}
      </h2>
    ),
    h3: ({ node, children }) => (
      <h3 data-help-anchor={headingAnchor(markdown, node)} tabIndex={-1} className="mb-2 mt-4 text-base font-semibold text-foreground focus:outline-none">
        {children}
      </h3>
    ),
    h4: ({ children }) => <h4 className="mb-1 mt-3 text-sm font-semibold text-foreground">{children}</h4>,
    p: ({ children }) => <p className="mb-3 leading-relaxed">{children}</p>,
    ul: ({ children }) => <ul className="mb-3 list-disc space-y-1 pl-5">{children}</ul>,
    ol: ({ children }) => <ol className="mb-3 list-decimal space-y-1 pl-5">{children}</ol>,
    blockquote: ({ children }) => (
      <blockquote className="mb-3 border-l-2 border-border pl-3 text-muted-foreground">{children}</blockquote>
    ),
    pre: ({ children }) => (
      <pre className="mb-3 overflow-x-auto rounded-md border border-border bg-muted p-3 text-xs [&_code]:bg-transparent [&_code]:p-0">
        {children}
      </pre>
    ),
    code: ({ children }) => <code className="rounded bg-muted px-1 py-0.5 font-mono text-[0.85em]">{children}</code>,
    table: ({ children }) => (
      <div className="mb-3 overflow-x-auto">
        <table className="w-full border-collapse text-xs">{children}</table>
      </div>
    ),
    th: ({ children }) => <th className="border border-border bg-muted px-2 py-1 text-left font-medium">{children}</th>,
    td: ({ children }) => <td className="border border-border px-2 py-1 align-top">{children}</td>,
    hr: () => <hr className="my-4 border-border" />,
  }

  return (
    <div className="text-sm text-foreground">
      <Markdown remarkPlugins={[remarkGfm]} components={components}>
        {markdown}
      </Markdown>
    </div>
  )
}
