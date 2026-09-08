package database

import (
	"database/sql"
	"testing"
	"time"

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
	// Three values one SECOND apart, for the control arm. Second-level
	// separation, not minute-level: a fixture separated at the minute field is
	// satisfied by minute ordering and cannot notice a sort that is wrong
	// inside a minute, which is precisely the resolution this column's text
	// sort operates at.
	secondA = "2026-02-28T23:30:00Z"
	secondB = "2026-02-28T23:30:01Z"
	secondC = "2026-02-28T23:30:02Z"
	// The same whole second as secondA, half a second LATER. This is the pair
	// that a variable-width layout gets wrong: '.' (0x2E) sorts BELOW 'Z'
	// (0x5A), so "...00.5Z" text-sorts before "...00Z" while being the later
	// instant.
	subSecondSpelling = "2026-02-28T23:30:00.5Z"
	// Width of a whole-second RFC3339 UTC value, "2026-02-28T23:30:00Z".
	fixedWidth = 20
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
//
// The fixture is separated by ONE SECOND, deliberately. An earlier version of
// this test separated its rows at the MINUTE field, which meant the ordering
// assertion was satisfied by minute separation rather than by the behaviour it
// names -- a row sorted wrongly inside a minute would still have passed. The
// column's text sort is only as good as its finest field, so the fixture has
// to discriminate at that resolution.
func TestGetUpdateHistory_AlreadyCanonicalIsUnchangedAndOrdersCorrectly(t *testing.T) {
	db := newTestDB(t)
	require.NoError(t, db.InsertUpdateHistory(newEntry("early", secondA, nil)))
	require.NoError(t, db.InsertUpdateHistory(newEntry("mid", secondB, nil)))
	require.NoError(t, db.InsertUpdateHistory(newEntry("late", secondC, nil)))

	// Byte-identical storage, not merely equivalent.
	for _, tc := range []struct{ id, want string }{
		{"early", secondA},
		{"mid", secondB},
		{"late", secondC},
	} {
		got, _ := rawTimestamps(t, db, tc.id)
		require.Equal(t, tc.want, got, "%s must be stored byte-identically", tc.id)
	}

	entries, _, err := db.GetUpdateHistory(models.UpdateHistoryFilters{})
	require.NoError(t, err)
	require.Equal(t, []string{"late", "mid", "early"},
		[]string{entries[0].ID, entries[1].ID, entries[2].ID})
}

// THE PROPERTY THE WHOLE BEAD RESTS ON: these columns are ORDERed and compared
// as TEXT, so text order must equal instant order. That requires every stored
// value to be the SAME WIDTH -- not merely correct, and not merely precise.
//
// A variable-width layout breaks it inside a single second. '.' is 0x2E and
// 'Z' is 0x5A, so "...:00.5Z" text-sorts BELOW "...:00Z" while being half a
// second LATER. Both arms below are the bead's own two symptoms regenerated
// from values the chokepoint itself produces, which is what makes this
// different from the offset case: no non-UTC server is needed.
//
// The ordering arm is written as an INVARIANT rather than an expected
// permutation, because that is the form which is meaningful on both sides:
// under a fixed-width layout the two values collapse to one string and their
// order is a legitimate tie, so asserting a specific permutation would be
// asserting something false. "The later instant never sorts strictly below the
// earlier one" is true under the fix and violated without it.
func TestGetUpdateHistory_SubSecondValuesStaySortableAndInBounds(t *testing.T) {
	db := newTestDB(t)
	require.NoError(t, db.InsertUpdateHistory(newEntry("whole", secondA, nil)))
	require.NoError(t, db.InsertUpdateHistory(newEntry("frac", subSecondSpelling, nil)))

	storedWhole, _ := rawTimestamps(t, db, "whole")
	storedFrac, _ := rawTimestamps(t, db, "frac")

	// Subtests, not a straight sequence: the three arms are the mechanism and
	// its two independent symptoms, and a plain require would stop at the
	// first and hide whether the other two actually discriminate.
	t.Run("text order does not contradict instant order", func(t *testing.T) {
		require.False(t, storedFrac < storedWhole,
			"%q (the LATER instant) text-sorts below %q", storedFrac, storedWhole)
	})

	t.Run("a bound at the whole second includes a row inside it", func(t *testing.T) {
		// The agent-os-hxra symptom, regenerated from values this chokepoint
		// produces itself -- no non-UTC server required.
		bound := time.Date(2026, 2, 28, 23, 30, 0, 0, time.UTC)
		entries, total, err := db.GetUpdateHistory(models.UpdateHistoryFilters{From: &bound})
		require.NoError(t, err)
		require.Equal(t, 2, total, "a row written half a second after the bound was excluded")
		require.Len(t, entries, 2)
	})

	t.Run("every stored value is fixed width", func(t *testing.T) {
		require.Len(t, storedWhole, fixedWidth)
		require.Len(t, storedFrac, fixedWidth,
			"a sub-second input must still be stored fixed width, or it cannot text-sort against its own second")
	})
}

// Sub-second precision is DISCARDED, on purpose, and this pins that so a
// later reader does not "fix" it back to a precision-preserving layout without
// the evidence in front of them.
//
// The trade is deliberate and one-directional: this column is SORTED and
// COMPARED as text, so fixed width is load-bearing and retained precision is
// not used by anything. A variable-width layout buys precision nobody reads at
// the cost of the ordering the whole table depends on -- see
// TestGetUpdateHistory_SubSecondValuesStaySortableAndInBounds for the two
// symptoms that produces. Every one of the five inputs below collapses to the
// same fixed-width whole second, which is the property being asserted; the
// discarded fraction is at most 999ms on a row whose purpose is to say which
// day an update ran.
//
// Latent in practice: all 16 existing writers emit whole seconds via
// time.Now().Format(time.RFC3339), so this truncation changes nothing about
// what they store. It governs the next caller.
func TestInsertUpdateHistory_SubSecondPrecisionIsDiscardedForFixedWidth(t *testing.T) {
	db := newTestDB(t)
	for _, tc := range []struct{ id, in, wantStored string }{
		{"z", "2026-02-28T23:30:00Z", "2026-02-28T23:30:00Z"},      // already whole
		{"n3", "2026-02-28T23:30:00.123Z", "2026-02-28T23:30:00Z"}, // truncated
		{"n2", "2026-02-28T23:30:00.120Z", "2026-02-28T23:30:00Z"}, // truncated
		{"n1", "2026-02-28T23:30:00.100Z", "2026-02-28T23:30:00Z"}, // truncated
		{"n0", "2026-02-28T23:30:00.000Z", "2026-02-28T23:30:00Z"}, // truncated
		{"n9", "2026-02-28T23:30:00.999Z", "2026-02-28T23:30:00Z"}, // truncated, never rounded up
	} {
		require.NoError(t, db.InsertUpdateHistory(newEntry(tc.id, tc.in, nil)))
		stored, _ := rawTimestamps(t, db, tc.id)
		require.Equal(t, tc.wantStored, stored, "%s: stored spelling", tc.id)

		require.Len(t, stored, fixedWidth, "%s: stored value must be fixed width", tc.id)

		// Truncation only ever moves a value EARLIER, never later, and never
		// past the second it names. Rounding would break both.
		in, err := time.Parse(time.RFC3339, tc.in)
		require.NoError(t, err)
		out, err := time.Parse(time.RFC3339, stored)
		require.NoError(t, err)
		require.False(t, out.After(in), "%s: truncation must never move a value later", tc.id)
		require.True(t, in.Sub(out) < time.Second, "%s: must stay within its own second", tc.id)
	}
}

// Guard 2's scope, pinned: the migration leaves a row that already ends in Z
// alone, sub-second fraction included.
//
// This is a KNOWN AND ACCEPTED GAP rather than a property to be proud of. Such
// a row keeps a variable-width spelling and so still does not text-sort
// correctly against whole-second rows in its own second -- the same defect
// canonicalTimestamp's truncation exists to prevent on the write side. It is
// left alone deliberately: rewriting it is a data-touching edit whose only
// beneficiary is a row shape this application has never written, since every
// writer goes through the chokepoint and emits whole seconds. Pinned here so
// the gap is visible in the test record instead of being rediscovered.
//
// Two-sided on ONE migration run, so it cannot pass by the statement simply
// doing nothing: the offset row in the same table must be rewritten by the
// same statement that leaves the fractional row alone.
func TestMigration15_LeavesStoredSubSecondRowsAloneWhileStillRewritingOffsets(t *testing.T) {
	db := newTestDB(t)
	seedRawUpdateHistory(t, db, "frac", "2026-02-28T23:30:00.120Z", "2026-02-28T23:30:00.120Z")
	seedRawUpdateHistory(t, db, "off", offsetSpelling, nil)

	_, err := db.db.Exec("DELETE FROM schema_migrations WHERE version = 15")
	require.NoError(t, err)
	require.NoError(t, RunMigrations(db))

	frac, fracCompleted := rawTimestamps(t, db, "frac")
	require.Equal(t, "2026-02-28T23:30:00.120Z", frac, "the migration must not touch an already-Z row")
	require.True(t, fracCompleted.Valid)
	require.Equal(t, "2026-02-28T23:30:00.120Z", fracCompleted.String)

	off, _ := rawTimestamps(t, db, "off")
	require.Equal(t, utcSpelling, off, "the same statement must still rewrite an offset row")
}

// Criterion 1's pass-through requirement, and the other half of the control:
// a normaliser must not zero, reject, or reshape a value it cannot parse. Must
// hold on BOTH sides of the fix.
//
// A sub-second value is NOT in this set -- Go's RFC3339 parses it, so it is
// normalised rather than passed through. That case is
// TestInsertUpdateHistory_SubSecondPrecisionIsDiscardedForFixedWidth.
func TestInsertUpdateHistory_UninterpretableValuesSurviveUnchanged(t *testing.T) {
	db := newTestDB(t)
	for _, tc := range []struct{ id, startedAt string }{
		{"garbage", "not-a-date"},
		{"zoneless", "2026-02-28T23:30:00"},
		{"spaced", "2026-02-28 23:30:00"},
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
		// Rows already ending in Z are byte-identical, including the
		// sub-second one that SQLite's strftime would otherwise truncate.
		// See TestMigration15_LeavesStoredSubSecondRowsAloneWhileStillRewritingOffsets
		// for why leaving that one alone is an accepted gap, not a win.
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
