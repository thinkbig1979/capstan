package database

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// update_history.started_at / completed_at are compared AND ordered as TEXT
// (GetUpdateHistory's `started_at >= ?` bounds and its `ORDER BY started_at
// DESC`), so two rows written in different spellings of the same instant do
// not order by instant. agent-os-lmbn normalises at the DB-layer chokepoint
// rather than at the 16 call sites, so these tests exercise the chokepoint.

const (
	// The same instant, 2026-02-28T22:30:00Z, in two spellings.
	offsetSpelling = "2026-03-01T00:30:00+02:00"
	utcSpelling    = "2026-02-28T22:30:00Z"
	// One hour LATER than the pair above, written canonically. Text-sorted
	// DESC it comes SECOND behind offsetSpelling ("2026-03-01" > "2026-02-28")
	// even though it is the later instant: that inversion is the bug.
	laterUTCSpelling = "2026-02-28T23:30:00Z"
)

// seedRawUpdateHistory writes a row with the DB layer bypassed, so the stored
// spelling is exactly what the caller asked for. Used to stage rows a
// pre-migration database would hold; every other test goes through
// InsertUpdateHistory on purpose.
func seedRawUpdateHistory(t *testing.T, db *DB, id, startedAt string, completedAt interface{}) {
	t.Helper()
	_, err := db.db.Exec(`INSERT INTO update_history
	    (id, container_id, container_name, image, status, trigger, started_at, completed_at)
	    VALUES (?, 'c1', 'web', 'nginx:latest', 'success', 'manual', ?, ?)`,
		id, startedAt, completedAt)
	require.NoError(t, err)
}

// rawTimestamps reads the bytes actually stored, which requires the CAST.
// started_at and completed_at are declared DATETIME, and modernc.org/sqlite
// converts a DATETIME-decltyped column on read: a stored "2026-02-28T23:30:00"
// comes back as "2026-02-28T23:30:00Z" (OBSERVED). A plain SELECT would
// therefore measure the DRIVER rather than the column, and would report a
// pass-through failure as a success. CAST removes the declared type, so the
// value arrives unconverted -- verified two-sided: the same probe shows
// CAST returning the zone-less value unchanged while the plain select
// Z-suffixes it.
func rawTimestamps(t *testing.T, db *DB, id string) (string, sql.NullString) {
	t.Helper()
	var startedAt string
	var completedAt sql.NullString
	err := db.db.QueryRow(
		"SELECT CAST(started_at AS TEXT), CAST(completed_at AS TEXT) FROM update_history WHERE id = ?", id).
		Scan(&startedAt, &completedAt)
	require.NoError(t, err)
	return startedAt, completedAt
}

func newEntry(id, startedAt string, completedAt *string) *models.UpdateHistoryEntry {
	return &models.UpdateHistoryEntry{
		ID:            id,
		ContainerID:   "c1",
		ContainerName: "web",
		Image:         "nginx:latest",
		Status:        "success",
		Trigger:       "manual",
		StartedAt:     startedAt,
		CompletedAt:   completedAt,
	}
}

// Criterion 2, insert half: a caller-supplied local-offset timestamp must
// reach SQL as UTC.
func TestInsertUpdateHistory_NormalisesOffsetToUTC(t *testing.T) {
	db := newTestDB(t)
	completed := "2026-03-01T01:30:00+02:00"
	require.NoError(t, db.InsertUpdateHistory(newEntry("i1", offsetSpelling, &completed)))

	startedAt, completedAt := rawTimestamps(t, db, "i1")
	require.Equal(t, utcSpelling, startedAt, "started_at must be stored as UTC")
	require.True(t, completedAt.Valid)
	require.Equal(t, laterUTCSpelling, completedAt.String, "completed_at must be stored as UTC")
}

// Criterion 2, update half. started_at is deliberately NOT tested here: it is
// absent from UpdateUpdateHistory's allowedColumns whitelist
// (update_history.go), so a started_at branch there would be dead code and an
// assertion about it would be asserting nothing.
func TestUpdateUpdateHistory_NormalisesCompletedAtToUTC(t *testing.T) {
	db := newTestDB(t)
	require.NoError(t, db.InsertUpdateHistory(newEntry("u1", utcSpelling, nil)))

	require.NoError(t, db.UpdateUpdateHistory("u1", map[string]interface{}{
		"status":       "success",
		"completed_at": "2026-03-01T01:30:00+02:00",
	}))

	_, completedAt := rawTimestamps(t, db, "u1")
	require.True(t, completedAt.Valid)
	require.Equal(t, laterUTCSpelling, completedAt.String)
}

// Criterion 3: ordering is the user-visible symptom. A storage-only assertion
// passes while the history list still displays wrong.
func TestGetUpdateHistory_OrdersByInstantAcrossSpellings(t *testing.T) {
	db := newTestDB(t)
	// A is the EARLIER instant (22:30Z) written with a +02:00 offset;
	// B is the LATER instant (23:30Z) written canonically. DESC order is B, A.
	require.NoError(t, db.InsertUpdateHistory(newEntry("A", offsetSpelling, nil)))
	require.NoError(t, db.InsertUpdateHistory(newEntry("B", laterUTCSpelling, nil)))

	entries, total, err := db.GetUpdateHistory(models.UpdateHistoryFilters{})
	require.NoError(t, err)
	require.Equal(t, 2, total)
	require.Len(t, entries, 2)
	require.Equal(t, []string{"B", "A"}, []string{entries[0].ID, entries[1].ID},
		"ORDER BY started_at DESC is a text sort; the later instant must come first")
}

// Criterion 4, the control arm: it MUST pass on both sides of the fix, or a
// normaliser that mangles every value would satisfy criteria 1-3.
func TestGetUpdateHistory_AlreadyCanonicalIsUnchangedAndOrdersCorrectly(t *testing.T) {
	db := newTestDB(t)
	require.NoError(t, db.InsertUpdateHistory(newEntry("early", utcSpelling, nil)))
	require.NoError(t, db.InsertUpdateHistory(newEntry("late", laterUTCSpelling, nil)))

	// Byte-identical storage, not merely equivalent.
	early, _ := rawTimestamps(t, db, "early")
	late, _ := rawTimestamps(t, db, "late")
	require.Equal(t, utcSpelling, early)
	require.Equal(t, laterUTCSpelling, late)

	entries, _, err := db.GetUpdateHistory(models.UpdateHistoryFilters{})
	require.NoError(t, err)
	require.Equal(t, []string{"late", "early"}, []string{entries[0].ID, entries[1].ID})
}

// Criterion 1's pass-through requirement, and the other half of the control:
// a normaliser must not zero, reject, or reshape what it cannot parse, and it
// must not truncate a value that is already canonical but carries sub-second
// precision. Both must hold on BOTH sides of the fix.
func TestInsertUpdateHistory_UninterpretableAndSubSecondValuesSurviveUnchanged(t *testing.T) {
	db := newTestDB(t)
	for _, tc := range []struct{ id, startedAt string }{
		{"garbage", "not-a-date"},
		{"zoneless", "2026-02-28T23:30:00"},
		{"spaced", "2026-02-28 23:30:00"},
		{"subsecond", "2026-02-28T23:30:00.123Z"},
	} {
		completed := tc.startedAt
		require.NoError(t, db.InsertUpdateHistory(newEntry(tc.id, tc.startedAt, &completed)))
		startedAt, completedAt := rawTimestamps(t, db, tc.id)
		require.Equal(t, tc.startedAt, startedAt, "%s: started_at must survive byte-identical", tc.id)
		require.True(t, completedAt.Valid, "%s: completed_at must not be NULLed", tc.id)
		require.Equal(t, tc.startedAt, completedAt.String, "%s: completed_at must survive byte-identical", tc.id)
	}
}

// The same pass-through requirement on the update path.
func TestUpdateUpdateHistory_UninterpretableCompletedAtSurvivesUnchanged(t *testing.T) {
	db := newTestDB(t)
	require.NoError(t, db.InsertUpdateHistory(newEntry("p1", utcSpelling, nil)))
	require.NoError(t, db.UpdateUpdateHistory("p1", map[string]interface{}{"completed_at": "not-a-date"}))

	_, completedAt := rawTimestamps(t, db, "p1")
	require.True(t, completedAt.Valid, "an unparseable value must not become NULL")
	require.Equal(t, "not-a-date", completedAt.String)
}

// Criterion 5. Seeded with the DB layer bypassed, so these are rows a
// pre-migration database would actually hold, then migration 15 is unstamped
// and re-run -- the same shape schedule_settings_migration_test.go uses.
func TestMigration15_NormalisesStoredTimestampsWithoutDestroyingAnything(t *testing.T) {
	db := newTestDB(t)

	seedRawUpdateHistory(t, db, "plus", "2026-03-01T00:30:00+02:00", "2026-03-01T01:30:00+02:00")
	seedRawUpdateHistory(t, db, "minus", "2026-02-28T17:30:00-05:00", "2026-02-28T18:30:00-05:00")
	seedRawUpdateHistory(t, db, "zulu", utcSpelling, laterUTCSpelling)
	seedRawUpdateHistory(t, db, "subsecond", "2026-02-28T22:30:00.123Z", "2026-02-28T23:30:00.456Z")
	seedRawUpdateHistory(t, db, "garbage", "not-a-date", "also-not-a-date")
	seedRawUpdateHistory(t, db, "nullcompleted", "2026-03-01T00:30:00+02:00", nil)

	rerunMigration15 := func() {
		t.Helper()
		_, err := db.db.Exec("DELETE FROM schema_migrations WHERE version = 15")
		require.NoError(t, err)
		require.NoError(t, RunMigrations(db))
	}
	rerunMigration15()

	assertAll := func(when string) {
		t.Helper()
		// Offset rows are rewritten to the instant they denote.
		for _, tc := range []struct{ id, startedAt, completedAt string }{
			{"plus", utcSpelling, laterUTCSpelling},
			{"minus", utcSpelling, laterUTCSpelling},
		} {
			s, c := rawTimestamps(t, db, tc.id)
			require.Equal(t, tc.startedAt, s, "%s: %s started_at", when, tc.id)
			require.True(t, c.Valid, "%s: %s completed_at must not be NULLed", when, tc.id)
			require.Equal(t, tc.completedAt, c.String, "%s: %s completed_at", when, tc.id)
		}
		// Already-canonical rows are byte-identical -- including the
		// sub-second one, which SQLite's strftime would truncate.
		for _, tc := range []struct{ id, startedAt, completedAt string }{
			{"zulu", utcSpelling, laterUTCSpelling},
			{"subsecond", "2026-02-28T22:30:00.123Z", "2026-02-28T23:30:00.456Z"},
		} {
			s, c := rawTimestamps(t, db, tc.id)
			require.Equal(t, tc.startedAt, s, "%s: %s started_at must be untouched", when, tc.id)
			require.True(t, c.Valid)
			require.Equal(t, tc.completedAt, c.String, "%s: %s completed_at must be untouched", when, tc.id)
		}
		// An uninterpretable value must be PRESERVED, not NULLed. A NULLed
		// completed_at is never pruned again (retention.go's
		// deleteOldUpdateHistoryStmt requires completed_at IS NOT NULL), so
		// the unguarded migration makes the row immortal as well as empty.
		s, c := rawTimestamps(t, db, "garbage")
		require.Equal(t, "not-a-date", s, "%s: garbage started_at", when)
		require.True(t, c.Valid, "%s: garbage completed_at was NULLed -- the row is now unprunable", when)
		require.Equal(t, "also-not-a-date", c.String, "%s: garbage completed_at", when)
		// A genuinely NULL completed_at stays NULL, and its started_at is
		// still normalised.
		s, c = rawTimestamps(t, db, "nullcompleted")
		require.Equal(t, utcSpelling, s, "%s: nullcompleted started_at", when)
		require.False(t, c.Valid, "%s: a NULL completed_at must stay NULL", when)
	}
	assertAll("first run")

	// Idempotent: a second application changes nothing.
	rerunMigration15()
	assertAll("second run")
}
