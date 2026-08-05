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
  type: 'added' | 'removed' | 'context' | 'header'
  content: string
  oldLine?: number
  newLine?: number
}

export function parseDiff(diff: string): DiffFile[] {
  const lines = diff.split('\n')
  const files: DiffFile[] = []
  let currentFile: DiffFile | null = null
  let currentHunk: DiffHunk | null = null

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i]

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
    } else if (line.startsWith('---')) {
      if (currentFile) {
        currentFile.oldPath = line.substring(4)
      }
    } else if (line.startsWith('+++')) {
      if (currentFile) {
        currentFile.path = line.substring(4)
      }
    } else if (line.startsWith('@@')) {
      if (currentFile) {
        currentHunk = {
          header: line,
          lines: [],
        }
        currentFile.hunks.push(currentHunk)
      }
    } else if (currentHunk) {
      let type: DiffLine['type'] = 'context'
      if (line.startsWith('+')) type = 'added'
      else if (line.startsWith('-')) type = 'removed'
      else if (line.startsWith('@@')) type = 'header'

      const diffLine: DiffLine = {
        type,
        content: line.substring(1),
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
