package database

import (
	"testing"
	"time"

	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// A caller-supplied RFC3339 bound is re-emitted by .Format(time.RFC3339) with
// whatever offset the caller sent, then compared as text against values stored
// Z-suffixed. Two spellings of one instant do not compare equal lexicographically,
// so the bound selects the wrong rows. The DELETE path fails in BOTH directions:
// a positive offset over-deletes (destroys a row the caller asked to keep), a
// negative offset under-deletes (agent-os-hxra).
//
// Every case below is the SAME INSTANT in a different legal spelling. Any
// disagreement between them is the defect.
//
// SCOPE, stated so a green run is not read as more than it is: these cases vary
// the BOUND spelling and hold the STORED spelling fixed at Z. That is the class
// this file covers. update_history's own writers do not yet store Z -- they
// emit the server's local offset -- so a case seeding what those writers
// actually produce fails today and belongs to agent-os-lmbn, not here. Green
// here means the bound side is canonical, not that the class is closed.
const (
	boundZ     = "2026-02-28T22:00:00Z"
	boundPlus  = "2026-03-01T00:00:00+02:00" // same instant, +02:00
	boundMinus = "2026-02-28T17:00:00-05:00" // same instant, -05:00

	// One hour before the bound. A correct "< bound" DELETE always takes it;
	// a correct ">= bound" read filter never returns it. It is the control
	// that proves each query does something rather than nothing.
	hourBefore = "2026-02-28T21:00:00Z"
)

func boundSpellings() []struct{ name, spelling string } {
	return []struct{ name, spelling string }{
		{"Z", boundZ},
		{"plus0200", boundPlus},
		{"minus0500", boundMinus},
	}
}

func parseBound(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse bound %s: %v", s, err)
	}
	return v
}

// The premise the rest of the file rests on. If the three spellings ever stop
// denoting one instant, every assertion below is meaningless rather than wrong.
func TestRFC3339BoundSpellingsAreOneInstant(t *testing.T) {
	z, plus, minus := parseBound(t, boundZ), parseBound(t, boundPlus), parseBound(t, boundMinus)
	if !z.Equal(plus) || !z.Equal(minus) {
		t.Fatalf("test fixture is broken: %s / %s / %s are not the same instant",
			boundZ, boundPlus, boundMinus)
	}
}

func seedUpdateHistoryAt(t *testing.T, d *DB, id, when string) {
	t.Helper()
	_, err := d.db.Exec(`INSERT INTO update_history
		(id, container_id, container_name, image, status, trigger, started_at, completed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, "c-"+id, "container-"+id, "img:latest", "success", "auto", when, when)
	if err != nil {
		t.Fatalf("seed update_history %s: %v", id, err)
	}
}

func seedBackupRunAt(t *testing.T, d *DB, id, when string) {
	t.Helper()
	// error_message is a plain string on models.BackupRun, so every production
	// writer stores '' rather than NULL. Seeding NULL here would fail the Scan
	// for a reason unrelated to this test and take the control arm with it.
	_, err := d.db.Exec(`INSERT INTO backup_runs
		(id, kind, trigger, status, started_at, finished_at, error_message)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, "backup", "manual", "success", when, when, "")
	if err != nil {
		t.Fatalf("seed backup_runs %s: %v", id, err)
	}
}

func updateHistoryIDs(t *testing.T, d *DB) []string {
	t.Helper()
	rows, err := d.db.Query("SELECT id FROM update_history ORDER BY id")
	if err != nil {
		t.Fatalf("select update_history ids: %v", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan id: %v", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate ids: %v", err)
	}
	return ids
}

// The data-loss arm. "atbound" sits exactly AT the bound, so "completed_at <
// bound" must never take it, whichever spelling the caller used. Asserting the
// surviving row set by identity rather than by count is what makes the
// over-delete direction visible: a bound that both destroys "atbound" and takes
// "older" deletes two rows where the correct answer also touches one table, and
// a count-only assertion on "rows deleted" would not separate them.
func TestDeleteUpdateHistoryOlderThanIsOffsetIndependent(t *testing.T) {
	for _, tc := range boundSpellings() {
		t.Run(tc.name, func(t *testing.T) {
			d := newTestDB(t)
			seedUpdateHistoryAt(t, d, "atbound", boundZ)
			seedUpdateHistoryAt(t, d, "older", hourBefore)

			deleted, err := d.DeleteUpdateHistoryOlderThan(parseBound(t, tc.spelling))
			if err != nil {
				t.Fatalf("DeleteUpdateHistoryOlderThan(%s): %v", tc.spelling, err)
			}

			survivors := updateHistoryIDs(t, d)
			if len(survivors) != 1 || survivors[0] != "atbound" {
				t.Errorf("bound %s deleted the wrong rows: survivors = %v, want [atbound]",
					tc.spelling, survivors)
			}
			if deleted != 1 {
				t.Errorf("bound %s deleted %d rows, want 1 (the row an hour older)",
					tc.spelling, deleted)
			}
		})
	}
}

func TestGetUpdateHistoryFromBoundIsOffsetIndependent(t *testing.T) {
	for _, tc := range boundSpellings() {
		t.Run(tc.name, func(t *testing.T) {
			d := newTestDB(t)
			seedUpdateHistoryAt(t, d, "atbound", boundZ)
			seedUpdateHistoryAt(t, d, "older", hourBefore)

			from := parseBound(t, tc.spelling)
			entries, total, err := d.GetUpdateHistory(models.UpdateHistoryFilters{
				From: &from, Page: 1, Limit: 50,
			})
			if err != nil {
				t.Fatalf("GetUpdateHistory(from=%s): %v", tc.spelling, err)
			}
			if total != 1 || len(entries) != 1 || entries[0].ID != "atbound" {
				t.Errorf("from=%s returned total=%d entries=%d, want exactly [atbound]",
					tc.spelling, total, len(entries))
			}
		})
	}
}

func TestGetUpdateHistoryToBoundIsOffsetIndependent(t *testing.T) {
	for _, tc := range boundSpellings() {
		t.Run(tc.name, func(t *testing.T) {
			d := newTestDB(t)
			seedUpdateHistoryAt(t, d, "atbound", boundZ)
			seedUpdateHistoryAt(t, d, "newer", "2026-02-28T23:00:00Z")

			to := parseBound(t, tc.spelling)
			entries, total, err := d.GetUpdateHistory(models.UpdateHistoryFilters{
				To: &to, Page: 1, Limit: 50,
			})
			if err != nil {
				t.Fatalf("GetUpdateHistory(to=%s): %v", tc.spelling, err)
			}
			if total != 1 || len(entries) != 1 || entries[0].ID != "atbound" {
				t.Errorf("to=%s returned total=%d entries=%d, want exactly [atbound]",
					tc.spelling, total, len(entries))
			}
		})
	}
}

func TestGetBackupRunsFilteredFromBoundIsOffsetIndependent(t *testing.T) {
	for _, tc := range boundSpellings() {
		t.Run(tc.name, func(t *testing.T) {
			d := newTestDB(t)
			seedBackupRunAt(t, d, "atbound", boundZ)
			seedBackupRunAt(t, d, "older", hourBefore)

			from := parseBound(t, tc.spelling)
			runs, total, err := d.GetBackupRunsFiltered(models.BackupHistoryFilters{
				From: &from, Page: 1, Limit: 50,
			})
			if err != nil {
				t.Fatalf("GetBackupRunsFiltered(from=%s): %v", tc.spelling, err)
			}
			if total != 1 || len(runs) != 1 || runs[0].ID != "atbound" {
				t.Errorf("from=%s returned total=%d runs=%d, want exactly [atbound]",
					tc.spelling, total, len(runs))
			}
		})
	}
}

func TestGetBackupRunsFilteredToBoundIsOffsetIndependent(t *testing.T) {
	for _, tc := range boundSpellings() {
		t.Run(tc.name, func(t *testing.T) {
			d := newTestDB(t)
			seedBackupRunAt(t, d, "atbound", boundZ)
			seedBackupRunAt(t, d, "newer", "2026-02-28T23:00:00Z")

			to := parseBound(t, tc.spelling)
			runs, total, err := d.GetBackupRunsFiltered(models.BackupHistoryFilters{
				To: &to, Page: 1, Limit: 50,
			})
			if err != nil {
				t.Fatalf("GetBackupRunsFiltered(to=%s): %v", tc.spelling, err)
			}
			if total != 1 || len(runs) != 1 || runs[0].ID != "atbound" {
				t.Errorf("to=%s returned total=%d runs=%d, want exactly [atbound]",
					tc.spelling, total, len(runs))
			}
		})
	}
}
