import { useState, useEffect } from 'react'
import { useGitDiff } from '@/hooks/useGit'
import { ChevronDown, ChevronRight } from 'lucide-react'
import { Button } from '@/components/ui/button'
import {
  Select,
  SelectTrigger,
  SelectValue,
  SelectContent,
  SelectItem,
} from '@/components/ui/select'

type DiffView = 'unified' | 'split'

interface DiffViewerProps {
  directoryPath: string
  commitHash: string
}

interface DiffFile {
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

export function DiffViewer({ directoryPath, commitHash }: DiffViewerProps) {
  const [collapsedFiles, setCollapsedFiles] = useState<Set<string>>(new Set())
  const [viewMode, setViewMode] = useState<DiffView>(() => {
    if (typeof window !== 'undefined') {
      const saved = localStorage.getItem('diff-view-preference') as DiffView
      return saved === 'split' ? 'split' : 'unified'
    }
    return 'unified'
  })

  const { data: diffData, isLoading, error } = useGitDiff(directoryPath, commitHash)

  useEffect(() => {
    localStorage.setItem('diff-view-preference', viewMode)
  }, [viewMode])

  const parseDiff = (diff: string): DiffFile[] => {
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

  const toggleFile = (path: string) => {
    setCollapsedFiles((prev) => {
      const next = new Set(prev)
      if (next.has(path)) {
        next.delete(path)
      } else {
        next.add(path)
      }
      return next
    })
  }

  if (isLoading) {
    return <div className="flex items-center justify-center py-4">Loading diff...</div>
  }

  if (error || !diffData) {
    return <div className="flex items-center justify-center py-4 text-muted-foreground">Failed to load diff</div>
  }

  const files = parseDiff(diffData.diff)

  if (files.length === 0) {
    return <div className="flex items-center justify-center py-4 text-muted-foreground">No diff available</div>
  }

  return (
    <div className="space-y-2 rounded-lg border">
      <div className="flex items-center justify-between px-4 py-2 border-b bg-muted/50">
        <span className="text-sm font-medium">Diff View</span>
        <Select value={viewMode} onValueChange={(value) => setViewMode(value as DiffView)}>
          <SelectTrigger className="w-[140px] h-8">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="unified">Unified</SelectItem>
            <SelectItem value="split">Split</SelectItem>
          </SelectContent>
        </Select>
      </div>

      {files.map((file, fileIndex) => {
        const isCollapsed = collapsedFiles.has(file.path)
        return (
          <div key={fileIndex} className="border-b last:border-b-0">
            <Button
              variant="ghost"
              className="w-full justify-start px-4 py-2 font-mono text-sm hover:bg-muted/50"
              onClick={() => toggleFile(file.path)}
            >
              {isCollapsed ? (
                <ChevronRight className="mr-2 h-4 w-4" />
              ) : (
                <ChevronDown className="mr-2 h-4 w-4" />
              )}
              <span className="flex-1 truncate">{file.path}</span>
              {file.addedLines > 0 && (
                <span className="mr-2 text-xs text-green-600">+{file.addedLines}</span>
              )}
              {file.removedLines > 0 && (
                <span className="text-xs text-red-600">-{file.removedLines}</span>
              )}
            </Button>

            {!isCollapsed && (
              <div className="overflow-x-auto">
                {viewMode === 'unified' ? (
                  <table className="w-full text-sm font-mono">
                    <tbody>
                      {file.hunks.map((hunk, hunkIndex) => (
                        <tr key={hunkIndex}>
                          <td className="p-0">
                            <div className="bg-muted/50 px-4 py-1 text-xs text-muted-foreground">
                              {hunk.header}
                            </div>
                          </td>
                        </tr>
                      ))}
                      {file.hunks.flatMap((hunk, hunkIndex) =>
                        hunk.lines.map((line, lineIndex) => (
                          <tr
                            key={`${hunkIndex}-${lineIndex}`}
                            className={`${
                              line.type === 'added'
                                ? 'bg-green-100 dark:bg-green-900/30'
                                : line.type === 'removed'
                                ? 'bg-red-100 dark:bg-red-900/30'
                                : ''
                            }`}
                          >
                            <td className="px-4 py-0.5 whitespace-nowrap">
                              <span
                                className={`${
                                  line.type === 'added'
                                    ? 'text-green-600'
                                    : line.type === 'removed'
                                    ? 'text-red-600'
                                    : 'text-muted-foreground'
                                }`}
                              >
                                {line.type === 'added' ? '+' : line.type === 'removed' ? '-' : ' '}
                                {line.content}
                              </span>
                            </td>
                          </tr>
                        )),
                      )}
                    </tbody>
                  </table>
                ) : (
                  <table className="w-full text-sm font-mono">
                    <tbody>
                      {file.hunks.map((hunk, hunkIndex) => (
                        <tr key={hunkIndex}>
                          <td colSpan={2} className="p-0">
                            <div className="bg-muted/50 px-4 py-1 text-xs text-muted-foreground">
                              {hunk.header}
                            </div>
                          </td>
                        </tr>
                      ))}
                      {file.hunks.flatMap((hunk, hunkIndex) =>
                        hunk.lines.map((line, lineIndex) => (
                          <tr
                            key={`${hunkIndex}-${lineIndex}`}
                            className="border-b"
                          >
                            {line.type === 'removed' || line.type === 'context' ? (
                              <td
                                className={`px-4 py-0.5 whitespace-nowrap ${
                                  line.type === 'removed'
                                    ? 'bg-red-100 dark:bg-red-900/30 text-red-600'
                                    : 'bg-muted/50'
                                }`}
                              >
                                {line.type === 'removed' ? '-' : ' '}{line.content}
                              </td>
                            ) : (
                              <td className="px-4 py-0.5 whitespace-nowrap bg-muted/30"></td>
                            )}
                            {line.type === 'added' || line.type === 'context' ? (
                              <td
                                className={`px-4 py-0.5 whitespace-nowrap ${
                                  line.type === 'added'
                                    ? 'bg-green-100 dark:bg-green-900/30 text-green-600'
                                    : 'bg-muted/50'
                                }`}
                              >
                                {line.type === 'added' ? '+' : ' '}{line.content}
                              </td>
                            ) : (
                              <td className="px-4 py-0.5 whitespace-nowrap bg-muted/30"></td>
                            )}
                          </tr>
                        )),
                      )}
                    </tbody>
                  </table>
                )}
              </div>
            )}
          </div>
        )
      })}
    </div>
  )
}
