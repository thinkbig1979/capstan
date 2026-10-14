import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { HelpMarkdown } from '../HelpMarkdown'
import { buildHelpLibrary, parseHelpDoc } from '../content'
import { RAW_DOCS } from '../docs-raw'

declare global {
  interface Window {
    __x?: unknown
  }
}

function renderDoc(markdown: string, docId = 'help/settings', repoRef?: string) {
  const onNavigate = vi.fn()
  const { container } = render(
    <HelpMarkdown markdown={markdown} docId={docId} onNavigate={onNavigate} repoRef={repoRef} />,
  )
  return { container, onNavigate }
}

describe('HelpMarkdown sanitisation', () => {
  afterEach(() => {
    delete window.__x
  })

  it('renders no script, img, javascript: or data: link from hostile markdown', () => {
    const { container } = renderDoc(
      [
        '[x](javascript:alert(1))',
        '',
        '<script>window.__x=1</script>',
        '',
        '<img src=x onerror="window.__x=1">',
        '',
        '[y](data:text/html,hi)',
      ].join('\n'),
    )

    expect(container.querySelector('script')).toBeNull()
    expect(container.querySelector('img')).toBeNull()
    expect(container.querySelector('a[href^="javascript:"]')).toBeNull()
    expect(container.querySelector('a[href^="data:"]')).toBeNull()
    expect(window.__x).toBeUndefined()
    // The unsafe links keep their text, as plain text.
    expect(screen.getByText('x').tagName).toBe('SPAN')
    expect(screen.getByText('y').tagName).toBe('SPAN')
  })

  it('shows an HTML-looking placeholder in a real doc as text', () => {
    // docs/how-to/upgrade-and-roll-back.md quotes the error `Host "<name>" is not allowed`.
    renderDoc(RAW_DOCS['../../../docs/how-to/upgrade-and-roll-back.md'], 'how-to/upgrade-and-roll-back')
    expect(screen.getByText(/Host "<name>" is not allowed/)).toBeInTheDocument()
  })
})

describe('HelpMarkdown link policy', () => {
  it('turns a relative docs link into in-panel navigation to doc + anchor', () => {
    const { container, onNavigate } = renderDoc('[c](../how-to/configure-backups.md#configuration)')
    expect(container.querySelector('a')).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: 'c' }))
    expect(onNavigate).toHaveBeenCalledTimes(1)
    expect(onNavigate.mock.calls[0]).toEqual([{ doc: 'how-to/configure-backups', anchor: 'configuration' }])
  })

  it('omits the anchor for a docs link without one, and resolves a same-doc #anchor', () => {
    const { onNavigate } = renderDoc('[a](../reference/api.md) and [b](#retention)', 'how-to/configure-backups')
    fireEvent.click(screen.getByRole('button', { name: 'a' }))
    fireEvent.click(screen.getByRole('button', { name: 'b' }))
    expect(onNavigate.mock.calls).toEqual([
      [{ doc: 'reference/api' }],
      [{ doc: 'how-to/configure-backups', anchor: 'retention' }],
    ])
  })

  it('sends the README documentation back link to the guide index from any depth', () => {
    const { onNavigate } = renderDoc('[two](../../README.md#documentation)', 'help/settings')
    fireEvent.click(screen.getByRole('button', { name: 'two' }))
    const top = renderDoc('[one](../README.md#documentation)', 'getting-started')
    fireEvent.click(screen.getByRole('button', { name: 'one' }))
    expect(onNavigate.mock.calls).toEqual([[{ kind: 'index' }]])
    expect(top.onNavigate.mock.calls).toEqual([[{ kind: 'index' }]])
  })

  it('opens http(s) and mailto links in a new tab with noopener', () => {
    renderDoc('[r](https://restic.net) [m](mailto:a@example.com)')
    for (const name of ['r', 'm']) {
      const link = screen.getByRole('link', { name })
      expect(link).toHaveAttribute('target', '_blank')
      expect(link.getAttribute('rel')).toContain('noopener')
      expect(link.getAttribute('rel')).toContain('noreferrer')
    }
    expect(screen.getByRole('link', { name: 'r' })).toHaveAttribute('href', 'https://restic.net')
  })

  it('links repo files outside docs/ to GitHub at the given ref', () => {
    renderDoc('[d](../../docker/Dockerfile) [q](../../README.md#quick-start)', 'explanation/architecture')
    expect(screen.getByRole('link', { name: 'd' }).getAttribute('href')).toMatch(/blob\/main\/docker\/Dockerfile$/)
    expect(screen.getByRole('link', { name: 'q' })).toHaveAttribute(
      'href',
      'https://github.com/thinkbig1979/capstan/blob/main/README.md#quick-start',
    )

    renderDoc('[v](../../docker/Dockerfile)', 'explanation/architecture', 'v1.4.0')
    expect(screen.getByRole('link', { name: 'v' })).toHaveAttribute(
      'href',
      'https://github.com/thinkbig1979/capstan/blob/v1.4.0/docker/Dockerfile',
    )
  })

  it('does not treat a protocol-relative link as a repo path', () => {
    const { container } = renderDoc('[p](//evil.example/x)')
    expect(container.querySelector('a')).toBeNull()
    expect(screen.getByText('p').tagName).toBe('SPAN')
  })

  it('renders an image as its alt text plus a link, never an img', () => {
    const { container } = renderDoc('![Backup screen](../images/backup.png)')
    expect(container.querySelector('img')).toBeNull()
    expect(screen.getByRole('link', { name: 'Backup screen' }).getAttribute('href')).toMatch(
      /blob\/main\/docs\/images\/backup\.png$/,
    )
  })
})

describe('HelpMarkdown layout', () => {
  it('wraps a GFM table in an overflow-x-auto container', () => {
    const { container } = renderDoc('| a | b |\n|---|---|\n| 1 | 2 |')
    const table = container.querySelector('table')
    expect(table).not.toBeNull()
    expect(table!.parentElement).toHaveClass('overflow-x-auto')
  })

  it('gives code blocks horizontal scroll', () => {
    const { container } = renderDoc('```\nlong line\n```')
    expect(container.querySelector('pre')).toHaveClass('overflow-x-auto')
  })
})

describe('HelpMarkdown heading anchors', () => {
  it('derives the anchor from the source line, as content.ts does, not the rendered text', () => {
    const markdown = '# T\n\n## See [the API](../reference/api.md)\n\n### Docker &amp; you\n'
    const { container } = renderDoc(markdown, 'how-to/x')
    const anchors = [...container.querySelectorAll('[data-help-anchor]')].map((h) => h.getAttribute('data-help-anchor'))
    expect(anchors).toEqual(parseHelpDoc('../../../docs/how-to/x.md', markdown).sections.map((s) => s.anchor))
    expect(anchors).toEqual(['see-the-apireferenceapimd', 'docker-amp-you'])
  })
})

describe('HelpMarkdown over the bundled docs', () => {
  const docs = buildHelpLibrary(RAW_DOCS)

  it.each(docs.map((d) => [d.id, d] as const))('%s: heading anchors match the parsed sections', (_id, doc) => {
    const { container } = renderDoc(doc.markdown, doc.id)
    const anchors = [...container.querySelectorAll('[data-help-anchor]')].map((h) => h.getAttribute('data-help-anchor'))
    expect(anchors).toEqual(doc.sections.map((s) => s.anchor))
    for (const h of container.querySelectorAll('h2, h3')) expect(h).toHaveAttribute('tabindex', '-1')
  })

  it.each(docs.map((d) => [d.id, d] as const))('%s: every link is in-panel or absolute', (_id, doc) => {
    const { container } = renderDoc(doc.markdown, doc.id)
    for (const a of container.querySelectorAll('a')) {
      expect(a.getAttribute('href')).toMatch(/^(https:\/\/|http:\/\/|mailto:)/)
    }
  })
})
