package database

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// seedFilterRuns inserts the four runs every GetBackupRunsFiltered test filters
// between.
//
// The rows are deliberately DECORRELATED: kind, trigger and status cut across
// each other rather than partitioning the table the same way, so every filter
// value selects a DIFFERENT set of ids:
//
//	id      kind    trigger    status   started_at
//	run-a   backup  manual     success  2026-01-01
//	run-b   backup  scheduled  failed   2026-03-15
//	run-c   prune   scheduled  failed   2026-06-01
//	run-d   prune   manual     failed   2026-09-01
//
//	kind=backup     -> {a,b}      trigger=manual    -> {a,d}    status=success -> {a}
//	kind=prune      -> {c,d}      trigger=scheduled -> {b,c}    status=failed  -> {b,c,d}
//	from=2026-08-01 -> {d}        to=2026-07-01     -> {a,b,c}
//
// All eight sets are distinct. That is the property being bought: with two
// perfectly anti-correlated rows every filter returns "one row or the other",
// so a clause bound to the wrong column can still select the expected row and
// the test cannot tell which column it filtered on. Here it changes the answer.
//
// Column values come from the CHECK constraints in migrations.go:298-311;
// started_at is RFC3339 because that is what services/backup.go writes and what
// GetBackupRunsFiltered binds for From/To.
func seedFilterRuns(t *testing.T, db *DB) {
	t.Helper()

	rows := []models.BackupRun{
		{ID: "run-a", Kind: "backup", Trigger: "manual", Status: "success", StartedAt: "2026-01-01T00:00:00Z"},
		{ID: "run-b", Kind: "backup", Trigger: "scheduled", Status: "failed", StartedAt: "2026-03-15T00:00:00Z"},
		{ID: "run-c", Kind: "prune", Trigger: "scheduled", Status: "failed", StartedAt: "2026-06-01T00:00:00Z"},
		{ID: "run-d", Kind: "prune", Trigger: "manual", Status: "failed", StartedAt: "2026-09-01T00:00:00Z"},
	}
	for i := range rows {
		require.NoError(t, db.CreateBackupRun(&rows[i]))
	}
}

// allSeededIDs is the full seed set, newest first — the order
// GetBackupRunsFiltered returns.
var allSeededIDs = []string{"run-d", "run-c", "run-b", "run-a"}

func runIDs(runs []models.BackupRun) []string {
	ids := make([]string, 0, len(runs))
	for _, r := range runs {
		ids = append(ids, r.ID)
	}
	return ids
}

// TestGetBackupRunsFiltered_EachFilterIsTwoSided proves every filter both ways
// on one instrument: with the filter absent the whole seed comes back, and with
// the filter set the result is EXACTLY the set that filter's column selects.
//
// Asserting the exact set rather than "the target is gone" is what pins the
// COLUMN. A one-sided "0 rows with the filter" result is equally consistent
// with a working filter, a typo'd column, an empty seed, and a query that
// errored into an empty slice; a filter that returns the right COUNT off the
// wrong column passes all of those checks too. Only the exact set fails when
// the clause moves to a neighbouring column — see the mutation arm recorded in
// the commit message.
func TestGetBackupRunsFiltered_EachFilterIsTwoSided(t *testing.T) {
	db := newTestDB(t)
	seedFilterRuns(t, db)

	aug := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	jul := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

	cases := []struct {
		name    string
		filters models.BackupHistoryFilters
		// want is the exact expected result, newest first.
		want []string
	}{
		{"status", models.BackupHistoryFilters{Status: "failed"}, []string{"run-d", "run-c", "run-b"}},
		{"kind", models.BackupHistoryFilters{Kind: "backup"}, []string{"run-b", "run-a"}},
		{"trigger", models.BackupHistoryFilters{Trigger: "scheduled"}, []string{"run-c", "run-b"}},
		{"from", models.BackupHistoryFilters{From: &aug}, []string{"run-d"}},
		{"to", models.BackupHistoryFilters{To: &jul}, []string{"run-c", "run-b", "run-a"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arm 1 — filter ABSENT: the whole seed is there. Without this the
			// arm below proves nothing, because an empty table also returns
			// "not the target".
			all, total, err := db.GetBackupRunsFiltered(models.BackupHistoryFilters{})
			require.NoError(t, err)
			require.Equal(t, len(allSeededIDs), total, "unfiltered total")
			require.Equal(t, allSeededIDs, runIDs(all), "unfiltered result")
			require.Less(t, len(tc.want), len(allSeededIDs),
				"a filter that excludes nothing cannot demonstrate filtering")

			// Arm 2 — filter SET: exactly the rows that column selects, and the
			// rows it does not select are gone.
			got, gotTotal, err := db.GetBackupRunsFiltered(tc.filters)
			require.NoError(t, err)
			assert.Equal(t, tc.want, runIDs(got), "exact matching set, newest first")
			assert.Equal(t, len(tc.want), gotTotal, "total must reflect the filter, not the table")
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

	// Control: the same call with no filter sees every row, so a zero below is
	// the filter working and not an empty table.
	all, allTotal, err := db.GetBackupRunsFiltered(models.BackupHistoryFilters{})
	require.NoError(t, err)
	require.Len(t, all, len(allSeededIDs))
	require.Equal(t, len(allSeededIDs), allTotal)

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

// TestGetBackupRunsFiltered_HugePageDoesNotWrapToPageOne guards the OFFSET
// arithmetic against integer overflow.
//
// offset = (page-1)*limit is computed from a client-supplied page. At
// page = MaxInt the product wraps to a NEGATIVE offset, and SQLite treats a
// negative OFFSET as no offset at all — so the request comes back holding page
// ONE's rows while the caller believes it asked for a page far past the end.
// That is a wrong answer served as a correct one, which is worse than an error.
//
// Both arms matter: the huge page must be EMPTY, and the in-range page-1 call
// on the same seed must be non-empty, or "empty" would prove only that the
// table is empty.
func TestGetBackupRunsFiltered_HugePageDoesNotWrapToPageOne(t *testing.T) {
	db := newTestDB(t)
	seedFilterRuns(t, db)

	// Control arm: page one over the same seed is populated.
	first, _, err := db.GetBackupRunsFiltered(models.BackupHistoryFilters{Page: 1, Limit: 50})
	require.NoError(t, err)
	require.Equal(t, allSeededIDs, runIDs(first), "page one is non-empty, so empty below means something")

	got, total, err := db.GetBackupRunsFiltered(models.BackupHistoryFilters{Page: math.MaxInt, Limit: 50})
	require.NoError(t, err)
	assert.Empty(t, runIDs(got), "a page past the end must be empty, never page one")
	assert.Equal(t, len(allSeededIDs), total, "total still describes the whole match set")

	// A merely large page (no overflow) must behave the same way, so the
	// overflow guard cannot be mistaken for a general "big page" rejection.
	big, bigTotal, err := db.GetBackupRunsFiltered(models.BackupHistoryFilters{Page: 999999999, Limit: 50})
	require.NoError(t, err)
	assert.Empty(t, runIDs(big), "an in-range page past the end is empty too")
	assert.Equal(t, len(allSeededIDs), bigTotal)
}

// TestGetBackupRunsFiltered_CombinesFiltersWithAND guards against a rewrite that
// ORs the clauses together: a run matching only one of two filters must not come
// back. The decorrelated seed is what gives this teeth — kind=backup and
// trigger=scheduled overlap in exactly one row.
func TestGetBackupRunsFiltered_CombinesFiltersWithAND(t *testing.T) {
	db := newTestDB(t)
	seedFilterRuns(t, db)

	// kind=backup is {a,b}, trigger=scheduled is {b,c}; the AND is {b} while an
	// OR would be {a,b,c}.
	both, total, err := db.GetBackupRunsFiltered(models.BackupHistoryFilters{Kind: "backup", Trigger: "scheduled"})
	require.NoError(t, err)
	assert.Equal(t, []string{"run-b"}, runIDs(both), "the single row in both sets")
	assert.Equal(t, 1, total)

	// Disjoint pair: status=success is {a}, kind=prune is {c,d}. Under AND
	// nothing matches; under OR all three would.
	none, total, err := db.GetBackupRunsFiltered(models.BackupHistoryFilters{Status: "success", Kind: "prune"})
	require.NoError(t, err)
	assert.Empty(t, none, "clauses must be ANDed, not ORed")
	assert.Equal(t, 0, total)
}
