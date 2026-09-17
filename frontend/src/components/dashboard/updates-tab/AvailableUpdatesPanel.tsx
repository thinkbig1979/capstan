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
    isRefreshing, isLoading, isError, error, fromCache, neverScanned, hasData,
    handleCheck, sortBy, setSortBy, query, setQuery, scannedAt, sortedUpdates,
    updates, policies, jobForContainer, expandedIds, toggleExpand, handleUpdate,
    updatePending,
  } = data

  if (isRefreshing) {
    return <CheckingUpdatesCard />
  }

  if (isLoading) {
    return <LoadingSkeletons />
  }

  if (isError && !fromCache) {
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

  if (!hasData) {
    return <NoUpdatesCard onCheck={handleCheck} isRefreshing={isRefreshing} />
  }

  return (
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
  )
}
