import { useState } from 'react'
import { Input } from '@/components/ui/input'
import { Button } from '@/components/ui/button'
import { useGitLog } from '@/hooks/useGit'
import { DiffViewer } from './DiffViewer'
import { Search } from 'lucide-react'

interface GitHistoryProps {
  directoryPath: string
}

export function GitHistory({ directoryPath }: GitHistoryProps) {
  const [offset, setOffset] = useState(0)
  const [fileFilter, setFileFilter] = useState('')
  const [selectedCommit, setSelectedCommit] = useState<string | null>(null)
  const limit = 50

  const { data: logData, isLoading, error } = useGitLog(directoryPath, limit, offset, fileFilter || undefined)

  const handleLoadMore = () => {
    setOffset((prev) => prev + limit)
  }

  const formatRelativeTime = (dateString: string) => {
    const date = new Date(dateString)
    const now = new Date()
    const seconds = Math.floor((now.getTime() - date.getTime()) / 1000)

    if (seconds < 60) return 'just now'
    if (seconds < 3600) return `${Math.floor(seconds / 60)} minutes ago`
    if (seconds < 86400) return `${Math.floor(seconds / 3600)} hours ago`
    if (seconds < 604800) return `${Math.floor(seconds / 86400)} days ago`
    return date.toLocaleDateString()
  }

  const truncateMessage = (message: string, maxLength = 60) => {
    if (message.length <= maxLength) return message
    return message.substring(0, maxLength) + '...'
  }

  if (isLoading && offset === 0) {
    return <div className="flex items-center justify-center py-8">Loading git history...</div>
  }

  if (error && offset === 0) {
    return <div className="flex items-center justify-center py-8 text-muted-foreground">Failed to load git history</div>
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center gap-2">
        <div className="relative flex-1">
          <Search className="absolute left-2 top-2 h-4 w-4 text-muted-foreground" />
          <Input
            placeholder="Filter by file..."
            value={fileFilter}
            onChange={(e) => {
              setFileFilter(e.target.value)
              setOffset(0)
              setSelectedCommit(null)
            }}
            className="pl-8"
          />
        </div>
      </div>

      <div className="space-y-2">
        {logData?.commits.map((commit) => (
          <div key={commit.hash} className="space-y-2">
            <div
              className={`rounded-lg border bg-card p-4 cursor-pointer transition-colors hover:bg-muted/50 ${
                selectedCommit === commit.hash ? 'border-primary' : ''
              }`}
              onClick={() => setSelectedCommit(selectedCommit === commit.hash ? null : commit.hash)}
            >
              <div className="flex items-start justify-between gap-4">
                <div className="flex-1 min-w-0">
                  <div className="flex items-center gap-2 mb-1">
                    <code className="text-sm font-mono text-muted-foreground">{commit.short}</code>
                    <span className="text-sm">{truncateMessage(commit.message)}</span>
                  </div>
                  <div className="flex items-center gap-2 text-xs text-muted-foreground">
                    <span>{commit.author}</span>
                    <span>•</span>
                    <span>{formatRelativeTime(commit.date)}</span>
                  </div>
                </div>
              </div>
            </div>

            {selectedCommit === commit.hash && (
              <div className="ml-4">
                <DiffViewer directoryPath={directoryPath} commitHash={commit.hash} />
              </div>
            )}
          </div>
        ))}
      </div>

      {logData?.hasMore && (
        <div className="flex justify-center">
          <Button variant="outline" onClick={handleLoadMore} disabled={isLoading}>
            {isLoading ? 'Loading...' : 'Load More'}
          </Button>
        </div>
      )}

      {logData?.commits.length === 0 && !isLoading && (
        <div className="flex items-center justify-center py-8 text-muted-foreground">
          {fileFilter ? 'No commits found matching filter' : 'No commits in this repository'}
        </div>
      )}
    </div>
  )
}
