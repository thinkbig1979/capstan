import { useState, useMemo } from 'react'
import { Input } from '@/components/ui/input'
import { Button } from '@/components/ui/button'
import { Select, SelectTrigger, SelectValue, SelectContent, SelectItem } from '@/components/ui/select'
import { useGitLog } from '@/hooks/useGit'
import { DiffViewer } from './DiffViewer'
import { Search, X } from 'lucide-react'

interface GitHistoryProps {
  stackId: string
}

type SearchScope = 'all' | 'files' | 'messages' | 'authors'

export function GitHistory({ stackId }: GitHistoryProps) {
  const [offset, setOffset] = useState(0)
  const [searchQuery, setSearchQuery] = useState('')
  const [searchScope, setSearchScope] = useState<SearchScope>('all')
  const [selectedCommit, setSelectedCommit] = useState<string | null>(null)
  const limit = 50

  const { data: logData, isLoading, error } = useGitLog(stackId, limit, offset)

  const filteredCommits = useMemo(() => {
    if (!logData?.commits) return []
    if (!searchQuery.trim()) return logData.commits

    const query = searchQuery.toLowerCase()

    return logData.commits.filter((commit) => {
      const matchesAll = commit.message.toLowerCase().includes(query) ||
                         commit.author.toLowerCase().includes(query) ||
                         commit.short.toLowerCase().includes(query)

      if (searchScope === 'all') return matchesAll
      if (searchScope === 'files') return commit.short.toLowerCase().includes(query)
      if (searchScope === 'messages') return commit.message.toLowerCase().includes(query)
      if (searchScope === 'authors') return commit.author.toLowerCase().includes(query)

      return matchesAll
    })
  }, [logData, searchQuery, searchScope])

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
            placeholder="Search commits..."
            value={searchQuery}
            onChange={(e) => {
              setSearchQuery(e.target.value)
              setOffset(0)
              setSelectedCommit(null)
            }}
            className="pl-8 pr-8"
          />
          {searchQuery && (
            <button
              onClick={() => {
                setSearchQuery('')
                setSearchScope('all')
                setOffset(0)
                setSelectedCommit(null)
              }}
              className="absolute right-2 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground"
              aria-label="Clear search"
            >
              <X className="h-4 w-4" />
            </button>
          )}
        </div>
        <Select
          value={searchScope}
          onValueChange={(value: SearchScope) => {
            setSearchScope(value)
            setOffset(0)
            setSelectedCommit(null)
          }}
        >
          <SelectTrigger className="w-[140px]">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">All</SelectItem>
            <SelectItem value="files">Files</SelectItem>
            <SelectItem value="messages">Messages</SelectItem>
            <SelectItem value="authors">Authors</SelectItem>
          </SelectContent>
        </Select>
      </div>

      <div className="text-xs text-muted-foreground">
        Showing {filteredCommits.length} commit{filteredCommits.length !== 1 ? 's' : ''}
      </div>

      <div className="space-y-2">
        {filteredCommits.map((commit) => (
          <div key={commit.hash} className="space-y-2">
            <div
              className={`rounded-lg border bg-card p-4 cursor-pointer transition-colors hover:bg-muted/50 ${
                selectedCommit === commit.hash ? 'border-primary' : ''
              }`}
              role="button"
              tabIndex={0}
              aria-expanded={selectedCommit === commit.hash}
              aria-label={`Commit ${commit.short}: ${commit.message}`}
              onClick={() => setSelectedCommit(selectedCommit === commit.hash ? null : commit.hash)}
              onKeyDown={(e) => {
                if (e.key === 'Enter' || e.key === ' ') {
                  e.preventDefault()
                  setSelectedCommit(selectedCommit === commit.hash ? null : commit.hash)
                }
              }}
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
                <DiffViewer stackId={stackId} commitHash={commit.hash} />
              </div>
            )}
          </div>
        ))}
      </div>

      {logData?.hasMore && !searchQuery && (
        <div className="flex justify-center">
          <Button variant="outline" onClick={handleLoadMore} disabled={isLoading}>
            {isLoading ? 'Loading...' : 'Load More'}
          </Button>
        </div>
      )}

      {filteredCommits.length === 0 && !isLoading && (
        <div className="flex items-center justify-center py-8 text-muted-foreground">
          {searchQuery ? `No commits found matching '${searchQuery}'` : 'No commits in this repository'}
        </div>
      )}
    </div>
  )
}
