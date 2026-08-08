import { describe, it, expect, afterEach } from 'vitest'
import { EditorState } from '@codemirror/state'
import { EditorView, basicSetup } from 'codemirror'
import { yaml } from '@codemirror/lang-yaml'
import { json } from '@codemirror/lang-json'

/**
 * Highlighting is a property of the real dependency graph, not of our code, so
 * this file deliberately mocks NOTHING.
 *
 * useCodeMirrorEditor.test.tsx starts with vi.mock('codemirror', ...) and stubs
 * every CodeMirror module, which is why it could not see that
 * @codemirror/language had resolved to two versions at once: lang-yaml against
 * 6.12.4, basicSetup against 6.12.1. CodeMirror matches language support by
 * facet identity, and facets from two copies of the module are different
 * objects — so the editor simply did not see the YAML language and the compose
 * editor, the primary editing surface of this app, rendered as plain text
 * (agent-os-l1hn).
 *
 * The assertion is the user-visible effect: a highlighted line is split into
 * classed spans, an unhighlighted one is a single flat text run.
 */

const YAML_DOC = ['services:', '  web:', '    image: nginx:latest', '    restart: always'].join('\n')
const JSON_DOC = '{\n  "services": {\n    "web": { "image": "nginx:latest" }\n  }\n}'

const views: EditorView[] = []

function renderDoc(doc: string, language: ReturnType<typeof yaml> | ReturnType<typeof json>) {
  const parent = document.createElement('div')
  document.body.appendChild(parent)
  const view = new EditorView({
    state: EditorState.create({ doc, extensions: [basicSetup, language] }),
    parent,
  })
  views.push(view)
  return parent
}

/** Spans carrying a highlight class inside the rendered lines. */
function highlightedTokens(parent: HTMLElement): string[] {
  return Array.from(parent.querySelectorAll('.cm-line span'))
    .filter((el) => el.className.length > 0)
    .map((el) => el.textContent ?? '')
}

afterEach(() => {
  for (const view of views.splice(0)) {
    view.destroy()
  }
  document.body.innerHTML = ''
})

describe('CodeMirror language packs resolve against the same @codemirror/language', () => {
  it('highlights YAML in the compose editor', () => {
    const tokens = highlightedTokens(renderDoc(YAML_DOC, yaml()))

    expect(tokens.length).toBeGreaterThan(0)
    // The keys are what a reader most needs coloured differently from values.
    expect(tokens).toContain('services')
    expect(tokens).toContain('image')
  })

  it('still highlights JSON', () => {
    // JSON was never broken — lang-json and basicSetup both landed on 6.12.1 —
    // so this is the control saying the dedupe moved lang-json onto 6.12.4
    // without costing anything. This token was produced both before and after
    // the dedupe. (Property names carry no tag of their own under the default
    // highlight style, so the styled tokens are the string values.)
    const tokens = highlightedTokens(renderDoc(JSON_DOC, json()))

    expect(tokens).toContain('"nginx:latest"')
  })
})
