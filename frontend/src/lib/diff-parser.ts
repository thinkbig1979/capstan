export interface DiffFile {
  path: string
  oldPath?: string
  addedLines: number
  removedLines: number
  hunks: DiffHunk[]
}

interface DiffHunk {
  header: string
  lines: DiffLine[]
}

interface DiffLine {
  type: 'added' | 'removed' | 'context'
  content: string
  /** 1-based line number in the pre-image; absent on added lines. */
  oldLine?: number
  /** 1-based line number in the post-image; absent on removed lines. */
  newLine?: number
}

/**
 * Strips git's `a/` / `b/` pre-image and post-image prefixes from a header path.
 *
 * The `diff --git` line is split on ` b/` so it never carried them, but the
 * `---`/`+++` headers are taken verbatim and used to overwrite that value — which
 * is how every file header in the UI came to read `b/compose.yml` (agent-os-t9up).
 */
function stripDiffPathPrefix(path: string): string {
  if (path.startsWith('a/') || path.startsWith('b/')) {
    return path.slice(2)
  }
  return path
}

/** Start line of each side, from a `@@ -oldStart,oldCount +newStart,newCount @@` header. */
function parseHunkStarts(header: string): { oldLine: number; newLine: number } {
  const match = /^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/.exec(header)
  if (!match) {
    return { oldLine: 1, newLine: 1 }
  }
  return { oldLine: Number(match[1]), newLine: Number(match[2]) }
}

export function parseDiff(diff: string): DiffFile[] {
  const lines = diff.split('\n')
  const files: DiffFile[] = []
  let currentFile: DiffFile | null = null
  let currentHunk: DiffHunk | null = null
  let oldLine = 0
  let newLine = 0

  for (const line of lines) {
    if (line.startsWith('diff --git')) {
      if (currentFile) {
        files.push(currentFile)
      }
      currentFile = {
        path: line.split(' b/')[1] || '',
        addedLines: 0,
        removedLines: 0,
        hunks: [],
      }
      currentHunk = null
    } else if (currentHunk === null && line.startsWith('---')) {
      // The `currentHunk === null` guard is the fix for agent-os-t9up: `---` is a
      // file header only BEFORE the first hunk. Inside a hunk it is ordinary
      // content — a YAML document separator, which in a Docker Compose manager is
      // routine. A removed one arrives as `----` and used to be swallowed
      // entirely: the line vanished from the rendered diff, removedLines
      // undercounted, and substring(4) of it wiped oldPath.
      if (currentFile) {
        const path = line.substring(4)
        // `--- /dev/null` marks an added file: there is no pre-image, so leaving
        // oldPath unset says that, where the literal string claimed the file used
        // to be called /dev/null.
        if (path !== '/dev/null') {
          currentFile.oldPath = stripDiffPathPrefix(path)
        }
      }
    } else if (currentHunk === null && line.startsWith('+++')) {
      // Same guard, same reason: an added line whose content begins with `++`
      // arrives as `+++...` and used to be read as a header, wiping the file path
      // and eating a character of the content with it.
      if (currentFile) {
        const path = line.substring(4)
        // `+++ /dev/null` marks a deleted file. Overwriting path with it named the
        // file "/dev/null" in the UI instead of the file that was deleted, so keep
        // the name the `diff --git` line already gave us.
        if (path !== '/dev/null') {
          currentFile.path = stripDiffPathPrefix(path)
        }
      }
    } else if (line.startsWith('@@')) {
      if (currentFile) {
        currentHunk = {
          header: line,
          lines: [],
        }
        currentFile.hunks.push(currentHunk)
        const starts = parseHunkStarts(line)
        oldLine = starts.oldLine
        newLine = starts.newLine
      }
    } else if (currentHunk) {
      let type: DiffLine['type'] = 'context'
      if (line.startsWith('+')) type = 'added'
      else if (line.startsWith('-')) type = 'removed'

      const diffLine: DiffLine = {
        type,
        content: line.substring(1),
      }

      // Line numbers advance on the side each line belongs to: a removed line
      // exists only in the pre-image, an added line only in the post-image, and a
      // context line in both.
      if (type === 'added') {
        diffLine.newLine = newLine++
      } else if (type === 'removed') {
        diffLine.oldLine = oldLine++
      } else {
        diffLine.oldLine = oldLine++
        diffLine.newLine = newLine++
      }

      currentHunk.lines.push(diffLine)

      if (type === 'added' && currentFile) {
        currentFile.addedLines++
      } else if (type === 'removed' && currentFile) {
        currentFile.removedLines++
      }
    }
  }

  if (currentFile) {
    files.push(currentFile)
  }

  return files
}
