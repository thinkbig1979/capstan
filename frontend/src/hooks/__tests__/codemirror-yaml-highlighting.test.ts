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

/**
 * CodeMirror's INITIAL parse runs on a wall-clock budget, so a busy machine can
 * leave the editor showing a partly-highlighted document for a moment.
 * @codemirror/language's LanguageState.init calls `work(20, viewportEnd)`, and
 * ParseContext.work turns that into `endTime = Date.now() + 20`, re-checked
 * after every `parse.advance()`. A process descheduled mid-construction — which
 * is what a loaded full-suite run does — finds the budget already spent after
 * the first advance, takes the partial tree, and highlights only the first
 * token. The parseWorker view plugin then finishes the parse and re-renders,
 * but jsdom has no requestIdleCallback so that lands on the library's
 * setTimeout(…, 500) fallback: recovery is real, and up to ~500ms late.
 *
 * OBSERVED (the third test below reproduces it on demand): the same document
 * that renders ['services','web','image','restart'] on an idle box renders
 * ['services'] synchronously under a stalled clock, then completes ~500ms later.
 *
 * So poll for the tokens instead of reading them once. The wait is observable,
 * not a fixed sleep — it returns on the first check whenever the parse already
 * finished, which is every run on an unloaded box.
 *
 * On the wall-clock budget below, since this repo has been busy REMOVING those
 * from tests (agent-os-fzqb, agent-os-gs7r): what those beads forbid is a bound
 * the test's PASS depends on in normal operation. This one is hang-guard duty,
 * not assertion duty. Nothing passes because the budget elapsed — the poll exits
 * the moment the tokens are there, and the budget only exists so that a parse
 * which genuinely got descheduled fails with the tokens it saw instead of
 * hanging. Its size is set by the library, not by taste: recovery is pinned to
 * that fixed setTimeout(…, 500), `Work.MaxPause` in CodeMirror's own source, so
 * 5s is ~10x the pause it has to clear, and the describe() timeout below sits
 * above the poll so the runner never wins the race and hides what was seen.
 * Please don't tidy either number down.
 */
const SETTLE_TIMEOUT_MS = 5_000

async function settledTokens(parent: HTMLElement, expected: string[]): Promise<string[]> {
  const deadline = Date.now() + SETTLE_TIMEOUT_MS
  let tokens = highlightedTokens(parent)
  while (!expected.every((token) => tokens.includes(token)) && Date.now() < deadline) {
    await new Promise((resolve) => setTimeout(resolve, 25))
    tokens = highlightedTokens(parent)
  }
  // Returned rather than asserted here so the caller's own expect() produces the
  // diagnostic, e.g. `expected [ 'services' ] to include 'image'`.
  return tokens
}

/**
 * Spends the initial parse budget before CodeMirror can use it, by making every
 * Date.now() reading a second later than the last. Deterministic stand-in for
 * the descheduling that a loaded full-suite run does to this test for real.
 */
function withStalledClock<T>(fn: () => T): T {
  const realNow = Date.now
  let readings = 0
  Date.now = () => realNow.call(Date) + readings++ * 1_000
  try {
    return fn()
  } finally {
    Date.now = realNow
  }
}

afterEach(() => {
  for (const view of views.splice(0)) {
    view.destroy()
  }
  document.body.innerHTML = ''
})

// The settle budget above is longer than vitest's 5s default test timeout, which
// would otherwise abort the poll before it could report what it actually saw.
describe('CodeMirror language packs resolve against the same @codemirror/language', { timeout: 20_000 }, () => {
  it('highlights YAML in the compose editor', async () => {
    const tokens = await settledTokens(renderDoc(YAML_DOC, yaml()), ['services', 'image'])

    expect(tokens.length).toBeGreaterThan(0)
    // The keys are what a reader most needs coloured differently from values.
    expect(tokens).toContain('services')
    expect(tokens).toContain('image')
  })

  it('still highlights JSON', async () => {
    // JSON was never broken — lang-json and basicSetup both landed on 6.12.1 —
    // so this is the control saying the dedupe moved lang-json onto 6.12.4
    // without costing anything. This token was produced both before and after
    // the dedupe. (Property names carry no tag of their own under the default
    // highlight style, so the styled tokens are the string values.)
    const tokens = await settledTokens(renderDoc(JSON_DOC, json()), ['"nginx:latest"'])

    expect(tokens).toContain('"nginx:latest"')
  })

  it('finishes highlighting YAML after an initial parse budget that expired early', async () => {
    // Positive control for the two waits above: without the stalled clock they
    // return on their first check and would prove nothing. This arm is the flake
    // itself, made deterministic — it reproduces the reported failure exactly,
    // `expected [ 'services' ] to include 'image'` (agent-os-mc7f).
    const parent = withStalledClock(() => renderDoc(YAML_DOC, yaml()))

    // Reading synchronously, the way this file used to, sees the truncated tree.
    // Should this ever stop holding, CodeMirror finished the parse inside one
    // advance() and the race is gone, not that highlighting broke.
    expect(highlightedTokens(parent)).not.toContain('image')

    expect(await settledTokens(parent, ['services', 'image'])).toContain('image')
  })
})
