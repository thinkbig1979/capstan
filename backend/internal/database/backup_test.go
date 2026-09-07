package database

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// seedFilterRuns inserts the two runs every GetBackupRunsFiltered test filters
// between. They differ in EVERY filterable column, so each filter's excluding
// value can be borrowed from the other row rather than invented — a filter that
// silently matched nothing would fail the "absent" arm as well as the "set" arm.
//
// Column values are drawn from the CHECK constraints in migrations.go:298-311
// (kind, trigger, status); started_at is RFC3339 because that is what
// services/backup.go writes and what GetBackupRunsFiltered binds for From/To.
func seedFilterRuns(t *testing.T, db *DB) {
	t.Helper()

	require.NoError(t, db.CreateBackupRun(&models.BackupRun{
		ID:        "run-old",
		Kind:      "backup",
		Trigger:   "manual",
		Status:    "success",
		StartedAt: "2026-01-01T00:00:00Z",
	}))
	require.NoError(t, db.CreateBackupRun(&models.BackupRun{
		ID:        "run-new",
		Kind:      "prune",
		Trigger:   "scheduled",
		Status:    "failed",
		StartedAt: "2026-06-01T00:00:00Z",
	}))
}

func runIDs(runs []models.BackupRun) []string {
	ids := make([]string, 0, len(runs))
	for _, r := range runs {
		ids = append(ids, r.ID)
	}
	return ids
}

// TestGetBackupRunsFiltered_EachFilterIsTwoSided proves every filter both ways
// on one instrument: with the filter absent the target row IS returned, and with
// the filter set to a value the target row does not carry it is NOT returned.
//
// A one-sided "0 rows with the filter" result is equally consistent with a
// working filter, a typo'd column name, an empty seed, and a query that errors
// into an empty slice. Asserting the same row present in the unfiltered arm of
// the SAME call rules all of those out.
func TestGetBackupRunsFiltered_EachFilterIsTwoSided(t *testing.T) {
	db := newTestDB(t)
	seedFilterRuns(t, db)

	march := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	cases := []struct {
		name string
		// target is the run the filter is meant to exclude.
		target string
		// excluding carries a value target does not have.
		excluding models.BackupHistoryFilters
	}{
		{"status", "run-old", models.BackupHistoryFilters{Status: "failed"}},
		{"kind", "run-old", models.BackupHistoryFilters{Kind: "prune"}},
		{"trigger", "run-old", models.BackupHistoryFilters{Trigger: "scheduled"}},
		{"from", "run-old", models.BackupHistoryFilters{From: &march}},
		{"to", "run-new", models.BackupHistoryFilters{To: &march}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arm 1 — filter ABSENT: the target must be there. Without this the
			// arm below proves nothing about the filter.
			all, total, err := db.GetBackupRunsFiltered(models.BackupHistoryFilters{})
			require.NoError(t, err)
			assert.Equal(t, 2, total, "unfiltered total")
			assert.Contains(t, runIDs(all), tc.target, "target row must be present with no filter")

			// Arm 2 — filter SET: the target must be gone, and the OTHER row
			// must survive, so an empty result cannot pass as a working filter.
			got, gotTotal, err := db.GetBackupRunsFiltered(tc.excluding)
			require.NoError(t, err)
			ids := runIDs(got)
			assert.NotContains(t, ids, tc.target, "target row must be filtered out")
			assert.Len(t, ids, 1, "the non-matching row must be filtered out, not everything")
			assert.Equal(t, 1, gotTotal, "total must reflect the filter, not the table")
		})
	}
}

// TestGetBackupRunsFiltered_RejectsSQLInjection is the AC3 guard. The payload is
// bound as a parameter, so it can only ever be compared as a literal status
// string that no row carries — it must not terminate the clause and return the
// table.
func TestGetBackupRunsFiltered_RejectsSQLInjection(t *testing.T) {
	db := newTestDB(t)
	seedFilterRuns(t, db)

	// Control: the same call with no filter sees both rows, so a zero below is
	// the filter working and not an empty table.
	all, allTotal, err := db.GetBackupRunsFiltered(models.BackupHistoryFilters{})
	require.NoError(t, err)
	require.Len(t, all, 2)
	require.Equal(t, 2, allTotal)

	got, total, err := db.GetBackupRunsFiltered(models.BackupHistoryFilters{Status: "' OR 1=1 --"})
	require.NoError(t, err, "the payload must be a bound value, not a syntax error")
	assert.Empty(t, got, "injection payload must match no rows")
	assert.Equal(t, 0, total, "COUNT(*) must be filtered by the same bound clause")
}

// TestGetBackupRunsFiltered_PagesAndOrders covers the page/limit/offset half:
// newest first, total independent of the page window.
func TestGetBackupRunsFiltered_PagesAndOrders(t *testing.T) {
	db := newTestDB(t)

	// Distinct started_at per row: ORDER BY started_at DESC has no tiebreaker,
	// so equal timestamps would make the page-2 window non-deterministic.
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		require.NoError(t, db.CreateBackupRun(&models.BackupRun{
			ID:        string(rune('a' + i)),
			Kind:      "backup",
			Trigger:   "manual",
			Status:    "success",
			StartedAt: base.Add(time.Duration(i) * time.Hour).Format(time.RFC3339),
		}))
	}

	page1, total, err := db.GetBackupRunsFiltered(models.BackupHistoryFilters{Page: 1, Limit: 2})
	require.NoError(t, err)
	assert.Equal(t, 5, total, "total counts the whole match set, not the page")
	assert.Equal(t, []string{"e", "d"}, runIDs(page1), "newest first")

	page2, total, err := db.GetBackupRunsFiltered(models.BackupHistoryFilters{Page: 2, Limit: 2})
	require.NoError(t, err)
	assert.Equal(t, 5, total)
	assert.Equal(t, []string{"c", "b"}, runIDs(page2), "page 2 continues the same ordering")

	page3, _, err := db.GetBackupRunsFiltered(models.BackupHistoryFilters{Page: 3, Limit: 2})
	require.NoError(t, err)
	assert.Equal(t, []string{"a"}, runIDs(page3), "last page is short, not empty")
}

// TestGetBackupRunsFiltered_CombinesFiltersWithAND guards against a rewrite that
// ORs the clauses together: a run matching only one of two filters must not come
// back.
func TestGetBackupRunsFiltered_CombinesFiltersWithAND(t *testing.T) {
	db := newTestDB(t)
	seedFilterRuns(t, db)

	// run-old is status=success AND kind=backup; run-new is neither.
	both, total, err := db.GetBackupRunsFiltered(models.BackupHistoryFilters{Status: "success", Kind: "backup"})
	require.NoError(t, err)
	assert.Equal(t, []string{"run-old"}, runIDs(both))
	assert.Equal(t, 1, total)

	// Half-matching: status is run-old's, kind is run-new's. Under AND nothing
	// matches; under OR both rows would.
	none, total, err := db.GetBackupRunsFiltered(models.BackupHistoryFilters{Status: "success", Kind: "prune"})
	require.NoError(t, err)
	assert.Empty(t, none, "clauses must be ANDed, not ORed")
	assert.Equal(t, 0, total)
}
