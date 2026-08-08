import { describe, it, expect } from 'vitest'
import { parseDiff } from '../diff-parser'

/**
 * parseDiff turns `git diff` output into the structure DiffViewer renders.
 * It had no tests at all (0/39 statements, agent-os-m1mu).
 *
 * The first version of this file pinned five behaviours that were WRONG rather
 * than right, each marked DEFECT and tracked in agent-os-t9up. Those assertions
 * are the deliberate, reviewable record of what agent-os-t9up changed: the
 * expectations below are their corrected counterparts, and each one names the
 * defect it replaces.
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
      { type: 'context', content: 'services:', oldLine: 1, newLine: 1 },
      { type: 'context', content: '  web:', oldLine: 2, newLine: 2 },
      { type: 'removed', content: '    image: nginx:1.0', oldLine: 3 },
      { type: 'added', content: '    image: nginx:2.0', newLine: 3 },
    ])
  })

  it('numbers each line on the side it exists on', () => {
    // Replaces "never populates oldLine/newLine, though DiffLine declares them".
    // The `@@` header carries both start lines, so throwing them away was the
    // defect; a removed line exists only in the pre-image and an added line only
    // in the post-image, so each carries just the number that applies.
    const [context, , removed, added] = file.hunks[0].lines
    expect(context).toMatchObject({ oldLine: 1, newLine: 1 })
    expect(removed).toMatchObject({ oldLine: 3 })
    expect(removed).not.toHaveProperty('newLine')
    expect(added).toMatchObject({ newLine: 3 })
    expect(added).not.toHaveProperty('oldLine')
  })

  it('strips the a/ and b/ prefixes from the displayed path', () => {
    // Replaces "DEFECT: leaks the a/ and b/ prefixes into the displayed path".
    // DiffViewer.tsx:96 renders file.path directly, so every file header in the
    // UI used to read "b/compose.yml".
    expect(file.path).toBe('compose.yml')
    expect(file.oldPath).toBe('compose.yml')
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
    // Without that flush the last file in every diff would be dropped, which is
    // the most consequential path in the function.
    const files = parseDiff(`${MODIFIED}\n${SECOND}`)
    expect(files.map((f) => f.path)).toEqual(['compose.yml', 'second.yml'])
    expect(files[1].addedLines).toBe(1)
  })

  it('does not leak hunks from one file into the next', () => {
    const files = parseDiff(`${MODIFIED}\n${SECOND}`)
    expect(files[0].hunks).toHaveLength(1)
    expect(files[1].hunks).toHaveLength(1)
    expect(files[1].hunks[0].lines).toEqual([{ type: 'added', content: 'x', newLine: 1 }])
  })

  it('parses the second file\'s headers even though the first file had hunks', () => {
    // The header branches are now gated on "not inside a hunk", so this is the
    // guard that keeps that gate from breaking every file after the first:
    // `diff --git` resets currentHunk, which is what reopens them.
    const files = parseDiff(`${MODIFIED}\n${SECOND}`)
    expect(files[1].oldPath).toBe('second.yml')
  })
})

describe('parseDiff — added and deleted files', () => {
  it('leaves oldPath unset for a newly added file', () => {
    // Replaces "DEFECT: reports /dev/null as the old path of a newly added file".
    // `--- /dev/null` means there is no pre-image; the literal string claimed the
    // file used to be called /dev/null.
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
    expect(file.path).toBe('new.yml')
    expect(file.oldPath).toBeUndefined()
    expect(file.addedLines).toBe(1)
  })

  it('keeps the real name of a deleted file', () => {
    // Replaces 'DEFECT: names a deleted file "/dev/null" in the UI'. `+++
    // /dev/null` used to overwrite path, so DiffViewer's file header read
    // "/dev/null" instead of the file that was deleted.
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
    expect(file.path).toBe('gone.yml')
    expect(file.oldPath).toBe('gone.yml')
    expect(file.removedLines).toBe(1)
  })
})

describe('parseDiff — YAML document separators', () => {
  /**
   * The case that matters most for a Docker Compose manager: `---` is ordinary
   * YAML. The parser used to test for the `---` / `+++` prefixes BEFORE it tested
   * whether it was inside a hunk, so compose content was read as a file header.
   *
   * Note how git actually renders these: a REMOVED `---` arrives as `----`
   * (the `-` marker plus the content) and a context one as ` ---`. Both used to
   * match `startsWith('---')`.
   */
  it('treats a removed "---" as content, not as a file header', () => {
    // Replaces 'DEFECT: swallows a "---" line inside a hunk and corrupts oldPath'.
    const [file] = parseDiff(
      lines(
        'diff --git a/compose.yml b/compose.yml',
        '--- a/compose.yml',
        '+++ b/compose.yml',
        '@@ -1,3 +1,1 @@',
        ' services:',
        '----',
        '-  image: nginx:1.0',
      ),
    )

    expect(file.hunks[0].lines).toEqual([
      { type: 'context', content: 'services:', oldLine: 1, newLine: 1 },
      { type: 'removed', content: '---', oldLine: 2 },
      { type: 'removed', content: '  image: nginx:1.0', oldLine: 3 },
    ])
    // Both removals are counted; the separator used to vanish and undercount.
    expect(file.removedLines).toBe(2)
    // And oldPath survives, where substring(4) of the separator used to wipe it.
    expect(file.oldPath).toBe('compose.yml')
  })

  it('keeps an unchanged "---" separator in the rendered diff', () => {
    const [file] = parseDiff(
      lines(
        'diff --git a/compose.yml b/compose.yml',
        '--- a/compose.yml',
        '+++ b/compose.yml',
        '@@ -1,2 +1,2 @@',
        ' ---',
        '+services: {}',
      ),
    )
    expect(file.hunks[0].lines).toEqual([
      { type: 'context', content: '---', oldLine: 1, newLine: 1 },
      { type: 'added', content: 'services: {}', newLine: 2 },
    ])
    expect(file.oldPath).toBe('compose.yml')
  })

  it('treats a bare "---" inside a hunk as content rather than a header', () => {
    // Not a shape git emits for a content line, but it is the shortest statement
    // of the guard: once a hunk is open, `---` can no longer reset oldPath.
    const [file] = parseDiff(
      lines(
        'diff --git a/compose.yml b/compose.yml',
        '--- a/compose.yml',
        '+++ b/compose.yml',
        '@@ -1,2 +1,1 @@',
        ' services:',
        '---',
      ),
    )
    expect(file.oldPath).toBe('compose.yml')
    expect(file.hunks[0].lines).toHaveLength(2)
  })

  it('keeps the file path when an added line starts with "++"', () => {
    // Replaces 'DEFECT: a line starting with "+++" inside a hunk wipes the file
    // path'. substring(4) also ate the first character of the content.
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
    expect(file.path).toBe('compose.yml')
    expect(file.hunks[0].lines).toEqual([
      { type: 'context', content: 'services:', oldLine: 1, newLine: 1 },
      { type: 'added', content: '++trailing', newLine: 2 },
    ])
    expect(file.addedLines).toBe(1)
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
    // split(' b/')[1] is undefined, and the `|| ''` fallback is what stops it
    // throwing.
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

  it('falls back to line 1 on each side when the @@ header is unparseable', () => {
    const [file] = parseDiff(
      lines('diff --git a/x.yml b/x.yml', '@@ nonsense @@', ' only'),
    )
    expect(file.hunks[0].lines).toEqual([
      { type: 'context', content: 'only', oldLine: 1, newLine: 1 },
    ])
  })

  it('restarts numbering at each hunk in the same file', () => {
    const [file] = parseDiff(
      lines(
        'diff --git a/x.yml b/x.yml',
        '--- a/x.yml',
        '+++ b/x.yml',
        '@@ -1,1 +1,1 @@',
        ' first',
        '@@ -40,1 +42,1 @@',
        ' later',
      ),
    )
    expect(file.hunks).toHaveLength(2)
    expect(file.hunks[0].lines[0]).toMatchObject({ oldLine: 1, newLine: 1 })
    expect(file.hunks[1].lines[0]).toMatchObject({ oldLine: 40, newLine: 42 })
  })
})
