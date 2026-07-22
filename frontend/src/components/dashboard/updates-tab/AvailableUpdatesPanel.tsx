import {
  CheckingUpdatesCard,
  LoadingSkeletons,
  UpdateCheckErrorCard,
  NeverScannedCard,
  NoUpdatesCard,
} from './UpdatesEmptyStates'
import { UpdatesTable } from './UpdatesTable'
import type { useUpdatesData } from './useUpdatesData'

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
    isRefreshing, isLoading, isError, fromCache, neverScanned, hasData,
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
    return <UpdateCheckErrorCard onCheck={handleCheck} />
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
