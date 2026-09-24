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

// insertNullErrorMessageRun inserts a backup_runs row whose error_message is
// NULL. No Capstan writer produces one: CreateBackupRun, UpdateBackupRun and
// SweepInterruptedBackupRuns all bind a Go string, and migrations 12 and 16
// copy the column verbatim (agent-os-1m58, established by a writer sweep, not
// tested). The column is nullable, though, so a hand-edited or externally
// written database can hold one, and the readers must survive it.
func insertNullErrorMessageRun(t *testing.T, db *DB, id, startedAt string) {
	t.Helper()
	_, err := db.db.Exec(
		`INSERT INTO backup_runs (id, kind, trigger, status, started_at) VALUES (?, 'backup', 'manual', 'running', ?)`,
		id, startedAt)
	require.NoError(t, err)
}

// A single NULL error_message used to fail rows.Scan and lose the whole list
// (agent-os-1m58). Every reader must return the NULL row, with an empty
// message, alongside the others, and a real message must still read back.
func TestBackupRunReaders_SurviveNullErrorMessage(t *testing.T) {
	db := newTestDB(t)
	seedFilterRuns(t, db)
	withMsg := models.BackupRun{ID: "run-e", Kind: "backup", Trigger: "manual", Status: "failed",
		StartedAt: "2026-10-01T00:00:00Z", ErrorMessage: "repository locked"}
	require.NoError(t, db.CreateBackupRun(&withMsg))
	insertNullErrorMessageRun(t, db, "run-null", "2026-05-01T00:00:00Z")

	wantIDs := []string{"run-e", "run-d", "run-c", "run-null", "run-b", "run-a"}

	runs, err := db.GetBackupRuns(10)
	require.NoError(t, err)
	assert.Equal(t, wantIDs, runIDs(runs))

	filtered, total, err := db.GetBackupRunsFiltered(models.BackupHistoryFilters{})
	require.NoError(t, err)
	assert.Equal(t, wantIDs, runIDs(filtered))
	assert.Equal(t, 6, total)

	for _, list := range [][]models.BackupRun{runs, filtered} {
		for _, r := range list {
			switch r.ID {
			case "run-null":
				assert.Empty(t, r.ErrorMessage)
			case "run-e":
				assert.Equal(t, "repository locked", r.ErrorMessage)
			}
		}
	}

	byID, err := db.GetBackupRunByID("run-null")
	require.NoError(t, err)
	assert.Equal(t, "run-null", byID.ID)
	assert.Empty(t, byID.ErrorMessage)

	byID, err = db.GetBackupRunByID("run-e")
	require.NoError(t, err)
	assert.Equal(t, "repository locked", byID.ErrorMessage)
}

// TestBackupRunTimestamp pins the WRITE-side spelling of backup_runs.started_at
// and finished_at (agent-os-zsgy), the third table in the class agent-os-lmbn
// (update_history) and agent-os-fn7x.8 (docker_cleanup_runs) fixed.
//
// started_at is TEXT, ORDERed by GetBackupRuns and GetBackupRunsFiltered and
// range-compared by the latter's From/To bounds, so a row written in a
// non-UTC offset or with sub-second precision does not order or filter by
// instant against a UTC one. Every arm below reads the SQL result or the raw
// column, so a read-side normaliser cannot satisfy it: the rows on disk would
// stay mixed and every SQL comparison over them would keep using the text.
func TestBackupRunTimestamp(t *testing.T) {
	db, err := NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	// Three distinct instants. Lexically the raw strings run "11..." >
	// "10..." > "09...", so a verbatim bind returns subsecond, older, newer --
	// the middle two swapped. By instant it is subsecond (11:00Z), newer
	// (09:00Z), older (08:00Z).
	olderFinished := "2026-01-01T10:30:00+02:00" // = 2026-01-01T08:30:00Z
	older := &models.BackupRun{
		ID: "run-older", Kind: "backup", Trigger: "scheduled", Status: "success",
		StartedAt:  "2026-01-01T10:00:00+02:00", // = 2026-01-01T08:00:00Z
		FinishedAt: &olderFinished,
	}
	newer := &models.BackupRun{
		ID: "run-newer", Kind: "backup", Trigger: "manual", Status: "success",
		StartedAt: "2026-01-01T09:00:00Z",
	}
	subsecond := &models.BackupRun{
		ID: "run-subsecond", Kind: "backup", Trigger: "scheduled", Status: "running",
		StartedAt: "2026-01-01T11:00:00.500Z",
	}

	// Inserted in none of the orders asserted below, so neither insertion
	// order nor rowid can satisfy the ordering arms.
	require.NoError(t, db.CreateBackupRun(newer))
	require.NoError(t, db.CreateBackupRun(subsecond))
	require.NoError(t, db.CreateBackupRun(older))

	want := []string{"run-subsecond", "run-newer", "run-older"}

	runs, err := db.GetBackupRuns(10)
	require.NoError(t, err)
	assert.Equal(t, want, runIDs(runs), "GetBackupRuns must order by instant, not by the text written")

	runs, total, err := db.GetBackupRunsFiltered(models.BackupHistoryFilters{})
	require.NoError(t, err)
	assert.Equal(t, 3, total)
	assert.Equal(t, want, runIDs(runs), "GetBackupRunsFiltered must order by instant, not by the text written")

	// The range bounds are already UTC-formatted (agent-os-hxra), so what
	// decides these is the stored spelling. A verbatim bind puts run-older
	// ("...T10:00:00+02:00") above an 08:30Z From and above an 08:30Z To.
	bound := time.Date(2026, 1, 1, 8, 30, 0, 0, time.UTC)
	runs, total, err = db.GetBackupRunsFiltered(models.BackupHistoryFilters{From: &bound})
	require.NoError(t, err)
	assert.Equal(t, 2, total)
	assert.Equal(t, []string{"run-subsecond", "run-newer"}, runIDs(runs), "From must select by instant")

	runs, total, err = db.GetBackupRunsFiltered(models.BackupHistoryFilters{To: &bound})
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	assert.Equal(t, []string{"run-older"}, runIDs(runs), "To must select by instant")

	// On disk, not merely through a getter.
	var storedStart, storedFinish string
	require.NoError(t, db.db.QueryRow(
		`SELECT started_at, finished_at FROM backup_runs WHERE id = 'run-older'`,
	).Scan(&storedStart, &storedFinish))
	assert.Equal(t, "2026-01-01T08:00:00Z", storedStart, "started_at must be stored as UTC")
	assert.Equal(t, "2026-01-01T08:30:00Z", storedFinish, "finished_at must be normalised on create too")

	require.NoError(t, db.db.QueryRow(
		`SELECT started_at FROM backup_runs WHERE id = 'run-subsecond'`,
	).Scan(&storedStart))
	assert.Equal(t, "2026-01-01T11:00:00Z", storedStart,
		"sub-second precision must be truncated: '...00.500Z' sorts BELOW '...00Z' in the same second")

	// UpdateBackupRun is the writer that finalises a run, so it binds
	// finished_at too.
	subFinished := "2026-01-01T13:05:00+02:00" // = 2026-01-01T11:05:00Z
	subsecond.Status = "success"
	subsecond.FinishedAt = &subFinished
	require.NoError(t, db.UpdateBackupRun(subsecond))
	require.NoError(t, db.db.QueryRow(
		`SELECT finished_at FROM backup_runs WHERE id = 'run-subsecond'`,
	).Scan(&storedFinish))
	assert.Equal(t, "2026-01-01T11:05:00Z", storedFinish, "UpdateBackupRun must normalise finished_at")

	// The caller owns the struct it passed and the runners reuse it after the
	// write, so normalising must happen in locals -- never back into *r.
	assert.Equal(t, "2026-01-01T10:00:00+02:00", older.StartedAt, "the caller's struct must not be mutated")
	assert.Equal(t, "2026-01-01T10:30:00+02:00", *older.FinishedAt, "the caller's struct must not be mutated")
	assert.Equal(t, "2026-01-01T13:05:00+02:00", *subsecond.FinishedAt, "the caller's struct must not be mutated")
}

// TestBackupRunTimestampCanonicalUnchanged is the other side of
// TestBackupRunTimestamp: the spelling every Capstan writer actually binds,
// time.Now().UTC().Format(time.RFC3339), is stored byte-identical, through
// both CreateBackupRun and UpdateBackupRun. A normaliser that rewrote canonical
// input (a different layout, a trailing fraction) would pass the mixed-spelling
// test's ordering arms and still change every row written from here on.
func TestBackupRunTimestampCanonicalUnchanged(t *testing.T) {
	db, err := NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	started := "2026-01-01T09:00:00Z"
	createFinished := "2026-01-01T09:01:00Z"
	run := &models.BackupRun{
		ID: "run-canonical", Kind: "backup", Trigger: "manual", Status: "success",
		StartedAt: started, FinishedAt: &createFinished,
	}
	require.NoError(t, db.CreateBackupRun(run))

	var storedStart, storedFinish string
	require.NoError(t, db.db.QueryRow(
		`SELECT started_at, finished_at FROM backup_runs WHERE id = 'run-canonical'`,
	).Scan(&storedStart, &storedFinish))
	assert.Equal(t, started, storedStart)
	assert.Equal(t, createFinished, storedFinish)

	updateFinished := "2026-01-01T09:05:00Z"
	run.FinishedAt = &updateFinished
	require.NoError(t, db.UpdateBackupRun(run))
	require.NoError(t, db.db.QueryRow(
		`SELECT finished_at FROM backup_runs WHERE id = 'run-canonical'`,
	).Scan(&storedFinish))
	assert.Equal(t, updateFinished, storedFinish)

	// A run still in flight has no finished_at; it must stay NULL, not become
	// an empty or zero-time string.
	open := &models.BackupRun{
		ID: "run-open", Kind: "backup", Trigger: "manual", Status: "running",
		StartedAt: started,
	}
	require.NoError(t, db.CreateBackupRun(open))
	var finished *string
	require.NoError(t, db.db.QueryRow(
		`SELECT finished_at FROM backup_runs WHERE id = 'run-open'`,
	).Scan(&finished))
	assert.Nil(t, finished, "a nil FinishedAt must be stored as NULL")
}
