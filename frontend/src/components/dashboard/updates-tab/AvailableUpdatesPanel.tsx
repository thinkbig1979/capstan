import {
  CheckingUpdatesCard,
  LoadingSkeletons,
  UpdateCheckErrorCard,
  NeverScannedCard,
  NoUpdatesCard,
} from './UpdatesEmptyStates'
import { UpdatesTable } from './UpdatesTable'
import type { useUpdatesData } from './useUpdatesData'
import { classifyError } from '@/lib/error-handler'
import { RefreshFailedNotice } from '@/components/RefreshFailedNotice'

type UpdatesData = ReturnType<typeof useUpdatesData>

interface AvailableUpdatesPanelProps {
  data: UpdatesData
}

/**
 * Picks which state the Available Updates tab is in — scanning, loading,
 * errored, never-scanned, all-up-to-date, or the results table — and renders it.
 */
export function AvailableUpdatesPanel({ data }: AvailableUpdatesPanelProps) {
  const {
    isRefreshing, isLoading, isError, error, updateData, neverScanned, hasData,
    handleCheck, sortBy, setSortBy, query, setQuery, scannedAt, sortedUpdates,
    updates, policies, jobForContainer, expandedIds, toggleExpand, handleUpdate,
    updatePending, refetchUpdates,
  } = data

  if (isRefreshing) {
    return <CheckingUpdatesCard />
  }

  if (isLoading) {
    return <LoadingSkeletons />
  }

  if (isError && !updateData) {
    // agent-os-rtn8: useCheckUpdates calls checkUpdates(FALSE), so the refusals
    // that reach here are the non-refresh branch's three 500s, carrying two
    // distinct sentences -- "Failed to get cached updates" (the cache read) and
    // "Failed to read the last scan time" (the settings read, from two places).
    // An unreachable registry and an unreadable database are different operator
    // problems and this card said the same thing for both. They reach us at all
    // only because agent-os-mc4i stopped classifyError's 5xx arm answering a
    // bare status code.
    //
    // Gated on `error`, not on `isError`: classifyError(undefined) returns the
    // invented sentence "An unexpected error occurred", and manufacturing a
    // cause the backend never sent is the worse defect. `isError` and `error`
    // are separate fields, so the guard is not redundant with the branch above
    // it. A test pins the no-error case.
    //
    // agent-os-4gve: the OPERAND was `!fromCache` and is now `!updateData`.
    // `fromCache` is a payload FIELD read off data TanStack retains across a
    // failed refetch, so it answered "did the payload claim to be cached",
    // never "is there anything to show". A populated list carrying
    // fromCache:false reaches the cache through useResources'
    // checkUpdatesRefresh mutation (setQueryData on the no-scheduler branch),
    // and from there one focus refetch that 500s replaced a populated table
    // with this card. `!hasData` is NOT the fix either -- it is
    // `updates.length > 0`, list non-emptiness, payload-derived in the same
    // way. Only the presence of the payload itself answers the question.
    return (
      <UpdateCheckErrorCard
        onCheck={handleCheck}
        cause={error ? classifyError(error).message : undefined}
      />
    )
  }

  if (neverScanned) {
    return <NeverScannedCard onCheck={handleCheck} />
  }

  // agent-os-4gve, the other half: past the guard above, `isError` here means a
  // REFRESH failed while TanStack still holds the last payload the server really
  // sent. Without this the panel renders that payload with no sign the check
  // failed -- "no updates" reads as "you are up to date". onRetry re-runs the
  // failed read (agent-os-3k31); the tail states' own "Check for Updates"
  // control starts a registry scan, which is not a retry of that read.
  const refreshFailed = isError && Boolean(updateData)

  if (!hasData) {
    return (
      <>
        {refreshFailed && <RefreshFailedNotice what="the available updates" onRetry={() => void refetchUpdates()} />}
        <NoUpdatesCard onCheck={handleCheck} isRefreshing={isRefreshing} />
      </>
    )
  }

  return (
    <>
      {refreshFailed && <RefreshFailedNotice what="the available updates" onRetry={() => void refetchUpdates()} />}
      <UpdatesTable
        sortedUpdates={sortedUpdates}
        totalCount={updates.length}
        sortBy={sortBy}
        onSortChange={setSortBy}
        query={query}
        onQueryChange={setQuery}
        scannedAt={scannedAt}
        isRefreshing={isRefreshing}
        onCheck={handleCheck}
        policies={policies}
        jobForContainer={jobForContainer}
        expandedIds={expandedIds}
        onToggleExpand={toggleExpand}
        onUpdate={handleUpdate}
        updatePending={updatePending}
      />
    </>
  )
}
