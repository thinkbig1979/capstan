import { describe, it, expect } from 'vitest'
import { parseDiff } from '../diff-parser'

/**
 * parseDiff turns `git diff` output into the structure DiffViewer renders.
 * It had no tests at all (0/39 statements, agent-os-m1mu).
 *
 * Several assertions below pin behaviour that is WRONG rather than behaviour
 * that is right. They are marked DEFECT and tracked in agent-os-t9up; they are
 * here so the bug is visible and so fixing it shows up as a deliberate,
 * reviewable test change rather than a silent behaviour swap.
 */

const lines = (...l: string[]) => l.join('\n')

const MODIFIED = lines(
  'diff --git a/compose.yml b/compose.yml',
  'index 1111111..2222222 100644',
  '--- a/compose.yml',
  '+++ b/compose.yml',
  '@@ -1,3 +1,3 @@',
  ' services:',
  '   web:',
  '-    image: nginx:1.0',
  '+    image: nginx:2.0',
)

describe('parseDiff — a single modified file', () => {
  const [file] = parseDiff(MODIFIED)

  it('returns one file', () => {
    expect(parseDiff(MODIFIED)).toHaveLength(1)
  })

  it('counts added and removed lines', () => {
    expect(file.addedLines).toBe(1)
    expect(file.removedLines).toBe(1)
  })

  it('keeps every hunk line with its type and its content stripped of the marker', () => {
    expect(file.hunks).toHaveLength(1)
    expect(file.hunks[0].header).toBe('@@ -1,3 +1,3 @@')
    expect(file.hunks[0].lines).toEqual([
      { type: 'context', content: 'services:' },
      { type: 'context', content: '  web:' },
      { type: 'removed', content: '    image: nginx:1.0' },
      { type: 'added', content: '    image: nginx:2.0' },
    ])
  })

  it('never populates oldLine/newLine, though DiffLine declares them', () => {
    // Declared at diff-parser.ts:17-18 and assigned nowhere. Pinned so that
    // adding line numbers is a deliberate change with a visible diff here.
    for (const line of file.hunks[0].lines) {
      expect(line).not.toHaveProperty('oldLine')
      expect(line).not.toHaveProperty('newLine')
    }
  })

  it('DEFECT: leaks the a/ and b/ prefixes into the displayed path', () => {
    // The `diff --git` line parses cleanly to 'compose.yml', but the later
    // `+++ b/compose.yml` line overwrites it with substring(4), which keeps the
    // 'b/'. DiffViewer.tsx:96 renders file.path directly, so the user sees
    // "b/compose.yml". See agent-os-t9up.
    expect(file.path).toBe('b/compose.yml')
    expect(file.oldPath).toBe('a/compose.yml')
  })
})

describe('parseDiff — multiple files', () => {
  const SECOND = lines(
    'diff --git a/second.yml b/second.yml',
    '--- a/second.yml',
    '+++ b/second.yml',
    '@@ -1 +1 @@',
    '+x',
  )

  it('flushes the trailing file after the loop ends', () => {
    // diff-parser.ts:78-80. Without that flush the last file in every diff
    // would be dropped, which is the most consequential path in the function.
    const files = parseDiff(`${MODIFIED}\n${SECOND}`)
    expect(files.map((f) => f.path)).toEqual(['b/compose.yml', 'b/second.yml'])
    expect(files[1].addedLines).toBe(1)
  })

  it('does not leak hunks from one file into the next', () => {
    const files = parseDiff(`${MODIFIED}\n${SECOND}`)
    expect(files[0].hunks).toHaveLength(1)
    expect(files[1].hunks).toHaveLength(1)
    expect(files[1].hunks[0].lines).toEqual([{ type: 'added', content: 'x' }])
  })
})

describe('parseDiff — added and deleted files', () => {
  it('DEFECT: reports /dev/null as the old path of a newly added file', () => {
    const [file] = parseDiff(
      lines(
        'diff --git a/new.yml b/new.yml',
        'new file mode 100644',
        '--- /dev/null',
        '+++ b/new.yml',
        '@@ -0,0 +1 @@',
        '+hello',
      ),
    )
    expect(file.path).toBe('b/new.yml')
    expect(file.oldPath).toBe('/dev/null') // agent-os-t9up
    expect(file.addedLines).toBe(1)
  })

  it('DEFECT: names a deleted file "/dev/null" in the UI', () => {
    // `+++ /dev/null` overwrites the real path, so DiffViewer's file header
    // reads "/dev/null" instead of the file that was deleted. agent-os-t9up.
    const [file] = parseDiff(
      lines(
        'diff --git a/gone.yml b/gone.yml',
        'deleted file mode 100644',
        '--- a/gone.yml',
        '+++ /dev/null',
        '@@ -1 +0,0 @@',
        '-bye',
      ),
    )
    expect(file.path).toBe('/dev/null')
    expect(file.oldPath).toBe('a/gone.yml')
    expect(file.removedLines).toBe(1)
  })
})

describe('parseDiff — YAML document separators', () => {
  // This is the one that matters most for a Docker Compose manager: `---` is
  // ordinary YAML, and the parser tests for it BEFORE it tests whether it is
  // inside a hunk.
  const SEPARATOR_DIFF = lines(
    'diff --git a/compose.yml b/compose.yml',
    '--- a/compose.yml',
    '+++ b/compose.yml',
    '@@ -1,3 +1,2 @@',
    ' services:',
    '---',
    '-  image: nginx:1.0',
  )

  it('DEFECT: swallows a "---" line inside a hunk and corrupts oldPath', () => {
    const [file] = parseDiff(SEPARATOR_DIFF)

    // The '---' line is simply gone from the rendered diff...
    expect(file.hunks[0].lines).toEqual([
      { type: 'context', content: 'services:' },
      { type: 'removed', content: '  image: nginx:1.0' },
    ])
    // ...and it was parsed as a file header, so substring(4) of '---' wiped
    // oldPath. agent-os-t9up.
    expect(file.oldPath).toBe('')
  })

  it('DEFECT: a line starting with "+++" inside a hunk wipes the file path', () => {
    const [file] = parseDiff(
      lines(
        'diff --git a/compose.yml b/compose.yml',
        '--- a/compose.yml',
        '+++ b/compose.yml',
        '@@ -1,1 +1,2 @@',
        ' services:',
        '+++trailing',
      ),
    )
    // substring(4) of '+++trailing' also eats the first character of the
    // content. Should be 'compose.yml'. agent-os-t9up.
    expect(file.path).toBe('railing')
  })
})

describe('parseDiff — degenerate input', () => {
  it('returns an empty array for an empty diff', () => {
    expect(parseDiff('')).toEqual([])
  })

  it('returns an empty array when there is no "diff --git" header', () => {
    expect(parseDiff(lines('@@ -1 +1 @@', '+orphan'))).toEqual([])
  })

  it('parses the path from the "diff --git" line when no +++ header follows', () => {
    const [file] = parseDiff(lines('diff --git a/only.yml b/only.yml', '@@ -1 +1 @@', '+x'))
    expect(file.path).toBe('only.yml')
    expect(file.oldPath).toBeUndefined()
  })

  it('yields an empty path when the "diff --git" line has no " b/" segment', () => {
    // diff-parser.ts:35 — split(' b/')[1] is undefined, and the `|| ''` fallback
    // is what stops it throwing.
    const [file] = parseDiff(lines('diff --git weird', '@@ -1 +1 @@', '+x'))
    expect(file.path).toBe('')
  })

  it('ignores hunk lines that arrive before any @@ header', () => {
    const files = parseDiff(lines('diff --git a/x.yml b/x.yml', '+stray', ' context'))
    expect(files[0].hunks).toEqual([])
    expect(files[0].addedLines).toBe(0)
  })

  it('records a file with no hunks at all', () => {
    const files = parseDiff(lines('diff --git a/x.yml b/x.yml', 'old mode 100644', 'new mode 100755'))
    expect(files).toHaveLength(1)
    expect(files[0].hunks).toEqual([])
  })
})
