import { useState, useMemo } from 'react'
import { Input } from '@/components/ui/input'
import { Button } from '@/components/ui/button'
import { Select, SelectTrigger, SelectValue, SelectContent, SelectItem } from '@/components/ui/select'
import { useGitLog } from '@/hooks/useGit'
import { DiffViewer } from './DiffViewer'
import { Search, X } from 'lucide-react'
import { formatRelativeTime } from '@/lib/format'
import { classifyError } from '@/lib/error-handler'
import { RefreshFailedNotice } from '@/components/RefreshFailedNotice'

interface GitHistoryProps {
  stackId: string
}

type SearchScope = 'all' | 'files' | 'messages' | 'authors'

function truncateMessage(message: string, maxLength = 60) {
  if (message.length <= maxLength) return message
  return message.substring(0, maxLength) + '...'
}

export function GitHistory({ stackId }: GitHistoryProps) {
  const [offset, setOffset] = useState(0)
  const [searchQuery, setSearchQuery] = useState('')
  const [searchScope, setSearchScope] = useState<SearchScope>('all')
  const [selectedCommit, setSelectedCommit] = useState<string | null>(null)
  const limit = 50

  const { data: logData, isLoading, error, refetch } = useGitLog(stackId, limit, offset)

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

  // agent-os-o5ud: `!logData`, not `offset === 0`. The cursor answered a
  // question about the DATA, and past the first page it answered it wrongly.
  // useGitLog is a plain useQuery keyed on the offset with no placeholderData
  // (useGit.ts:28-34), so Load More starts a FRESH query whose data is
  // undefined while it is in flight. The old guard did not fire, and the
  // component rendered the count line below -- which is ungated -- as
  // "Showing 0 commits" for a repository whose commits were still on the wire.
  // MEASURED, not assumed: the empty-state sentence further down is NOT
  // reachable in that window, because it is gated on `!isLoading` and
  // `isLoading` is true for exactly that window. The count line was the one
  // that lied.
  if (isLoading && !logData) {
    return <div className="flex items-center justify-center py-8">Loading git history...</div>
  }

  // agent-os-o5ud: `!logData`, not `offset === 0`. This is the agent-os-wczm /
  // agent-os-4gve class -- a second operand that is not a data-presence test --
  // and here it was a PAGINATION CURSOR. `logData` survives a failed refetch and
  // is still read at :33, :34, :38 and :50, so at offset 0 one focus-refetch
  // that 500s threw a rendered commit list away for this error view (staleTime
  // 30s, refetchOnWindowFocus on, a 500 not auto-retryable: query-client.ts:7,
  // 13, 14). Past the first page the branch was unreachable, so the same
  // failure showed stale commits with no disclosure at all. Both halves now get
  // the same answer: the error view is for an error with NOTHING to show; a
  // failure with data on screen is a refresh failure, reported by
  // RefreshFailedNotice below without taking anything away.
  if (error && !logData) {
    // agent-os-rtn8: GetLog routes three DISTINCT 404s into this one branch --
    // GIT_NOT_REPO "Not a git repository", GIT_NO_COMMITS "Repository has no
    // commits yet" and STACK_DIR_MISSING "Stack directory does not exist on
    // disk" (services/git.go:782, :118, :769) -- and they ask for three
    // different operator actions. The screen said the same thing for all three.
    //
    // The cause reaches us at all only because agent-os-mc4i stopped
    // classifyError's 404 arm replacing the backend's message.
    //
    // Nothing was ever rendered here, so "Failed to load git history" is still
    // true and the fixed sentence stays as the HEADLINE: a failure the backend
    // said nothing about renders what it rendered before.
    return (
      <div className="flex flex-col items-center justify-center gap-1 py-8 text-center text-muted-foreground">
        <p>Failed to load git history</p>
        <p>{classifyError(error).message}</p>
      </div>
    )
  }

  return (
    <div className="space-y-4">
      {/*
        agent-os-o5ud. Reached only with `logData` present, since an error
        without it returned above. The commits stay on screen -- they are the
        last ones the server really sent -- and this says so rather than
        pretending the view is current. Keyed on `error` alone so it cannot
        outlive the failure it describes, and placed here so it covers every
        page, not just the first.
      */}
      {error && (
        <RefreshFailedNotice what="the git history" onRetry={() => refetch()} />
      )}

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
              type="button"
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
          <SelectTrigger className="w-[140px]" aria-label="Search scope">
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
