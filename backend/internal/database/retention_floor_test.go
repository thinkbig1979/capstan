package database

import (
	"strings"
	"testing"
	"time"
)

// MinRetentionDays is documented on the constant as a property of PRUNING
// (retention.go:17-20), but before agent-os-q1ms it was enforced only on two
// routes TO a prune — the string parser (retention.go:55) and the settings
// endpoint (handlers/settings.go:488) — and never at the three statements that
// actually issue the DELETE. A caller that computed a retention some other way
// reached them unclamped.
//
// What retentionDays=0 does, measured rather than reasoned about:
// `datetime('now', '-' || 0 || ' days')` is NOW, so "older than the cutoff"
// becomes "older than this instant" and every eligible row in the table goes,
// including one written seconds ago. TestRetentionFloor_UnguardedSQLWipesTable
// keeps that demonstration in the suite so these guards never look decorative.
//
// Negative values are NOT worse, contrary to the obvious reading: `'-' || -1`
// concatenates to the string "--1 days", which SQLite does not accept as a
// modifier, so datetime() yields NULL, `col < NULL` is NULL, and the DELETE
// matches nothing. OBSERVED at a2c97e7 via a throwaway probe on this fixture:
// v=-1 and v=-30 both left every seeded row in place in all three tables, while
// v=0 left none. A sub-floor negative is therefore a silently ineffective prune
// rather than a wipe. It is refused here anyway: it is equally out of contract,
// and its silence is its own bug (unbounded growth was agent-os-0jp).

// seedActionLog inserts an action_log row created daysAgo days in the past.
// action_log has no FK on user_id since migration v9, so a bare actor is fine.
func seedActionLog(t *testing.T, d *DB, id string, daysAgo int) {
	t.Helper()
	when := time.Now().AddDate(0, 0, -daysAgo).UTC().Format(time.RFC3339)
	_, err := d.db.Exec(`INSERT INTO action_log (id, user_id, action, detail, created_at)
		VALUES (?, ?, ?, ?, ?)`, id, "u-"+id, "test.action", "seeded", when)
	if err != nil {
		t.Fatalf("seed action_log %s: %v", id, err)
	}
}

// subFloorValues are the out-of-contract inputs every deleter must refuse. 0 is
// the destructive one; the negatives are the silent no-ops. MinRetentionDays-1
// is the boundary neighbour, so the comparison cannot be off by one.
var subFloorValues = []int{0, -1, -30, MinRetentionDays - 1}

func TestDeleteOldUpdateHistory_RefusesBelowFloor(t *testing.T) {
	for _, days := range subFloorValues {
		db := newRetentionTestDB(t)
		seedUpdateHistory(t, db, "ancient", 400)
		seedUpdateHistory(t, db, "fresh", 1)

		deleted, err := db.DeleteOldUpdateHistory(days)
		if err == nil {
			t.Errorf("days=%d: DeleteOldUpdateHistory returned no error, want a refusal", days)
		} else if !strings.Contains(err.Error(), "retention") {
			t.Errorf("days=%d: refusal %q does not mention retention", days, err)
		}
		if deleted != 0 {
			t.Errorf("days=%d: reported %d deleted, want 0", days, deleted)
		}
		// The point of the bead: the ROWS, not the return value.
		if got := countRows(t, db, "update_history"); got != 2 {
			t.Errorf("days=%d: update_history has %d rows, want both seeded rows intact", days, got)
		}
	}
}

func TestDeleteOldBackupRuns_RefusesBelowFloor(t *testing.T) {
	for _, days := range subFloorValues {
		db := newRetentionTestDB(t)
		seedBackupRun(t, db, "ancient", 400)
		seedBackupRun(t, db, "fresh", 1)

		deleted, err := db.DeleteOldBackupRuns(days)
		if err == nil {
			t.Errorf("days=%d: DeleteOldBackupRuns returned no error, want a refusal", days)
		} else if !strings.Contains(err.Error(), "retention") {
			t.Errorf("days=%d: refusal %q does not mention retention", days, err)
		}
		if deleted != 0 {
			t.Errorf("days=%d: reported %d deleted, want 0", days, deleted)
		}
		if got := countRows(t, db, "backup_runs"); got != 2 {
			t.Errorf("days=%d: backup_runs has %d rows, want both seeded runs intact", days, got)
		}
		// backup_run_items goes with the parent via ON DELETE CASCADE, so a
		// refused prune must leave the children as well as the parents.
		if got := countRows(t, db, "backup_run_items"); got != 2 {
			t.Errorf("days=%d: backup_run_items has %d rows, want 2 — the cascade ran", days, got)
		}
	}
}

func TestDeleteOldActionLogs_RefusesBelowFloor(t *testing.T) {
	for _, days := range subFloorValues {
		db := newRetentionTestDB(t)
		seedActionLog(t, db, "ancient", 400)
		seedActionLog(t, db, "fresh", 1)

		err := db.DeleteOldActionLogs(days)
		if err == nil {
			t.Errorf("days=%d: DeleteOldActionLogs returned no error, want a refusal", days)
		} else if !strings.Contains(err.Error(), "retention") {
			t.Errorf("days=%d: refusal %q does not mention retention", days, err)
		}
		if got := countRows(t, db, "action_log"); got != 2 {
			t.Errorf("days=%d: action_log has %d rows, want both seeded rows intact", days, got)
		}
	}
}

// TestRetentionFloor_AcceptsTheFloorItself is the must-PASS half of the same
// instrument. A guard that refuses everything would satisfy every assertion
// above, so the floor value itself has to still prune, at all three deleters.
func TestRetentionFloor_AcceptsTheFloorItself(t *testing.T) {
	db := newRetentionTestDB(t)
	seedUpdateHistory(t, db, "stale", MinRetentionDays+1)
	seedUpdateHistory(t, db, "recent", 1)
	seedBackupRun(t, db, "stale", MinRetentionDays+1)
	seedBackupRun(t, db, "recent", 1)
	seedActionLog(t, db, "stale", MinRetentionDays+1)
	seedActionLog(t, db, "recent", 1)

	n, err := db.DeleteOldUpdateHistory(MinRetentionDays)
	if err != nil {
		t.Fatalf("DeleteOldUpdateHistory at the floor: %v", err)
	}
	if n != 1 || countRows(t, db, "update_history") != 1 {
		t.Errorf("update_history: deleted %d, %d left; want 1 deleted and the recent row kept",
			n, countRows(t, db, "update_history"))
	}

	n, err = db.DeleteOldBackupRuns(MinRetentionDays)
	if err != nil {
		t.Fatalf("DeleteOldBackupRuns at the floor: %v", err)
	}
	if n != 1 || countRows(t, db, "backup_runs") != 1 {
		t.Errorf("backup_runs: deleted %d, %d left; want 1 deleted and the recent run kept",
			n, countRows(t, db, "backup_runs"))
	}

	if err := db.DeleteOldActionLogs(MinRetentionDays); err != nil {
		t.Fatalf("DeleteOldActionLogs at the floor: %v", err)
	}
	if got := countRows(t, db, "action_log"); got != 1 {
		t.Errorf("action_log has %d rows, want only the recent one", got)
	}
}

// TestRetentionFloor_UnguardedSQLWipesTable is the negative arm, kept in the
// suite rather than run once and described in a commit message.
//
// The tests above prove the guarded functions refuse and the rows survive. On
// their own they cannot distinguish "the guard stopped the deletion" from "the
// statement would not have deleted anything anyway" — a guard in front of a
// harmless statement passes every one of those assertions. This test removes
// that ambiguity by executing the SAME statement constants the production
// functions execute, with the guard bypassed, and showing the tables emptied at
// retentionDays = 0. Sharing the constants is the point: an inlined copy would
// keep passing while the production SQL changed underneath it.
func TestRetentionFloor_UnguardedSQLWipesTable(t *testing.T) {
	db := newRetentionTestDB(t)
	seedUpdateHistory(t, db, "ancient", 400)
	seedUpdateHistory(t, db, "fresh", 1)
	seedBackupRun(t, db, "ancient", 400)
	seedBackupRun(t, db, "fresh", 1)
	seedActionLog(t, db, "ancient", 400)
	seedActionLog(t, db, "fresh", 1)

	for _, tc := range []struct{ stmt, table string }{
		{deleteOldUpdateHistoryStmt, "update_history"},
		{deleteOldBackupRunsStmt, "backup_runs"},
		{deleteOldActionLogsStmt, "action_log"},
	} {
		if got := countRows(t, db, tc.table); got != 2 {
			t.Fatalf("precondition: %s has %d rows, want 2", tc.table, got)
		}
		if _, err := db.db.Exec(tc.stmt, 0); err != nil {
			t.Fatalf("unguarded %s at 0 days: %v", tc.table, err)
		}
		// Not "the old row went" — everything went, including a row one day old.
		if got := countRows(t, db, tc.table); got != 0 {
			t.Errorf("unguarded %s at 0 days left %d rows; the floor guard is "+
				"no longer what stops the wipe, so the tests above prove nothing",
				tc.table, got)
		}
	}

	// The cascade goes with it: backup_run_items is what makes the backup_runs
	// prune irreversible beyond its own table (ON DELETE CASCADE, agent-os-94t).
	if got := countRows(t, db, "backup_run_items"); got != 0 {
		t.Errorf("backup_run_items has %d rows after the unguarded wipe, want 0", got)
	}
}

// TestRetentionFloor_NegativeIsANoOpNotAWipe records the measured behaviour the
// guard's doc comment asserts, so the comment cannot quietly become false.
// "-1 is worse than 0" is the intuitive reading and it is wrong: `'-' || -1` is
// the string "--1 days", not a modifier SQLite accepts, so datetime() is NULL
// and the predicate matches nothing.
func TestRetentionFloor_NegativeIsANoOpNotAWipe(t *testing.T) {
	db := newRetentionTestDB(t)
	seedUpdateHistory(t, db, "ancient", 400)
	seedUpdateHistory(t, db, "fresh", 1)

	var modifier any
	if err := db.db.QueryRow(`SELECT datetime('now', '-' || ? || ' days')`, -1).Scan(&modifier); err != nil {
		t.Fatalf("evaluating the -1 modifier: %v", err)
	}
	if modifier != nil {
		t.Errorf("datetime() with -1 returned %v, want NULL — the guard comment's "+
			"reasoning about negatives no longer holds", modifier)
	}

	if _, err := db.db.Exec(deleteOldUpdateHistoryStmt, -1); err != nil {
		t.Fatalf("unguarded update_history at -1 days: %v", err)
	}
	if got := countRows(t, db, "update_history"); got != 2 {
		t.Errorf("unguarded prune at -1 days deleted %d of 2 rows; negatives are "+
			"no longer inert and the severity note needs revisiting", 2-got)
	}
}
