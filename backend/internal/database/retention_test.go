package database

import (
	"fmt"
	"testing"
	"time"
)

// update_history, backup_runs and backup_run_items grew without bound: only
// sessions and action_log were ever pruned. The growth is slow, silent and only
// shows up on exactly the deployments that matter — long-lived ones with
// scheduled updates or backups switched on (agent-os-0jp).

// seedUpdateHistory inserts a row completed daysAgo days in the past.
func seedUpdateHistory(t *testing.T, d *DB, id string, daysAgo int) {
	t.Helper()
	when := time.Now().AddDate(0, 0, -daysAgo).UTC().Format(time.RFC3339)
	_, err := d.db.Exec(`INSERT INTO update_history
		(id, container_id, container_name, image, status, trigger, started_at, completed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, "c-"+id, "container-"+id, "img:latest", "success", "auto", when, when)
	if err != nil {
		t.Fatalf("seed update_history %s: %v", id, err)
	}
}

// seedBackupRun inserts a run started daysAgo days in the past, with one item.
func seedBackupRun(t *testing.T, d *DB, id string, daysAgo int) {
	t.Helper()
	when := time.Now().AddDate(0, 0, -daysAgo).UTC().Format(time.RFC3339)
	_, err := d.db.Exec(`INSERT INTO backup_runs (id, kind, trigger, status, started_at)
		VALUES (?, ?, ?, ?, ?)`, id, "backup", "scheduled", "success", when)
	if err != nil {
		t.Fatalf("seed backup_runs %s: %v", id, err)
	}
	_, err = d.db.Exec(`INSERT INTO backup_run_items (id, run_id, stack_id, status)
		VALUES (?, ?, ?, ?)`, "item-"+id, id, "stacks~demo:default", "success")
	if err != nil {
		t.Fatalf("seed backup_run_items for %s: %v", id, err)
	}
}

func countRows(t *testing.T, d *DB, table string) int {
	t.Helper()
	var n int
	if err := d.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func newRetentionTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := NewWithMigrations(":memory:")
	if err != nil {
		t.Fatalf("NewWithMigrations: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestDeleteOldUpdateHistory_RespectsBoundary(t *testing.T) {
	db := newRetentionTestDB(t)
	seedUpdateHistory(t, db, "ancient", 400)
	seedUpdateHistory(t, db, "old", 91)
	seedUpdateHistory(t, db, "fresh", 3)

	deleted, err := db.DeleteOldUpdateHistory(90)
	if err != nil {
		t.Fatalf("DeleteOldUpdateHistory: %v", err)
	}
	if deleted != 2 {
		t.Errorf("deleted %d rows, want the 2 older than 90 days", deleted)
	}
	if got := countRows(t, db, "update_history"); got != 1 {
		t.Errorf("update_history has %d rows, want only the fresh one", got)
	}
}

func TestDeleteOldBackupRuns_RespectsBoundary(t *testing.T) {
	db := newRetentionTestDB(t)
	seedBackupRun(t, db, "ancient", 400)
	seedBackupRun(t, db, "old", 91)
	seedBackupRun(t, db, "fresh", 3)

	deleted, err := db.DeleteOldBackupRuns(90)
	if err != nil {
		t.Fatalf("DeleteOldBackupRuns: %v", err)
	}
	if deleted != 2 {
		t.Errorf("deleted %d runs, want the 2 older than 90 days", deleted)
	}
	if got := countRows(t, db, "backup_runs"); got != 1 {
		t.Errorf("backup_runs has %d rows, want only the fresh one", got)
	}
}

// TestDeleteOldBackupRuns_CascadesToItems verifies rather than assumes the
// cascade: backup_run_items declares ON DELETE CASCADE, but that only takes
// effect when foreign_keys enforcement is genuinely on for the connection
// (see agent-os-94t). Orphaned items would defeat the whole point of the prune.
func TestDeleteOldBackupRuns_CascadesToItems(t *testing.T) {
	db := newRetentionTestDB(t)
	seedBackupRun(t, db, "old", 120)
	seedBackupRun(t, db, "fresh", 1)

	if got := countRows(t, db, "backup_run_items"); got != 2 {
		t.Fatalf("test precondition: expected 2 items, got %d", got)
	}

	if _, err := db.DeleteOldBackupRuns(90); err != nil {
		t.Fatalf("DeleteOldBackupRuns: %v", err)
	}

	if got := countRows(t, db, "backup_run_items"); got != 1 {
		t.Errorf("backup_run_items has %d rows, want 1 — the old run's item was orphaned", got)
	}

	var orphans int
	err := db.db.QueryRow(`SELECT COUNT(*) FROM backup_run_items i
		WHERE NOT EXISTS (SELECT 1 FROM backup_runs r WHERE r.id = i.run_id)`).Scan(&orphans)
	if err != nil {
		t.Fatalf("orphan check: %v", err)
	}
	if orphans != 0 {
		t.Errorf("%d backup_run_items rows have no parent run", orphans)
	}
}

// TestRetentionDays_FloorIsClamped mirrors the action_log behaviour: an absurdly
// low setting must not let a prune wipe the history the operator is looking at.
func TestRetentionDays_FloorIsClamped(t *testing.T) {
	cases := []struct {
		setting string
		want    int
	}{
		{"", DefaultRetentionDays},
		{"0", MinRetentionDays},
		{"1", MinRetentionDays},
		{"-30", MinRetentionDays},
		{"not-a-number", DefaultRetentionDays},
		{"30", 30},
		{fmt.Sprint(MinRetentionDays), MinRetentionDays},
	}

	db := newRetentionTestDB(t)
	for _, tc := range cases {
		t.Run("setting="+tc.setting, func(t *testing.T) {
			if err := db.SetSetting("test_retention_key", tc.setting); err != nil {
				t.Fatalf("SetSetting: %v", err)
			}
			if got := db.RetentionDays("test_retention_key"); got != tc.want {
				t.Errorf("RetentionDays(%q) = %d, want %d", tc.setting, got, tc.want)
			}
		})
	}
}

// TestRetentionDays_UnsetKeyUsesDefault covers a fresh install where the
// migration's INSERT OR IGNORE has not seeded the key.
func TestRetentionDays_UnsetKeyUsesDefault(t *testing.T) {
	db := newRetentionTestDB(t)
	if got := db.RetentionDays("key_that_does_not_exist"); got != DefaultRetentionDays {
		t.Errorf("RetentionDays for a missing key = %d, want %d", got, DefaultRetentionDays)
	}
}

// TestPruneHistory_PrunesEveryTable covers the pass the daily ticker runs: one
// table failing must not skip the others, and all three settings are honoured.
func TestPruneHistory_PrunesEveryTable(t *testing.T) {
	db := newRetentionTestDB(t)

	seedUpdateHistory(t, db, "old", 200)
	seedUpdateHistory(t, db, "fresh", 1)
	seedBackupRun(t, db, "old", 200)
	seedBackupRun(t, db, "fresh", 1)
	if _, err := db.db.Exec(
		`INSERT INTO action_log (id, user_id, action, detail, created_at)
		 VALUES ('old', 'u', 'login', '{}', datetime('now', '-200 days')),
		        ('fresh', 'u', 'login', '{}', datetime('now'))`); err != nil {
		t.Fatalf("seed action_log: %v", err)
	}

	result := db.PruneHistory()

	if result.UpdateHistory != 1 || result.BackupRuns != 1 {
		t.Errorf("PruneHistory reported %+v, want 1 of each", result)
	}
	for table, want := range map[string]int{
		"update_history":   1,
		"backup_runs":      1,
		"backup_run_items": 1,
		"action_log":       1,
	} {
		if got := countRows(t, db, table); got != want {
			t.Errorf("%s has %d rows after the pass, want %d", table, got, want)
		}
	}
}

// TestPruneHistory_HonoursConfiguredRetention proves the pass reads the
// settings rather than a hardcoded 90 days.
func TestPruneHistory_HonoursConfiguredRetention(t *testing.T) {
	db := newRetentionTestDB(t)
	seedUpdateHistory(t, db, "twenty-days", 20)
	seedBackupRun(t, db, "twenty-days", 20)

	// Default is 90 days, so nothing should go yet.
	if r := db.PruneHistory(); r.UpdateHistory != 0 || r.BackupRuns != 0 {
		t.Fatalf("default retention deleted %+v, want nothing", r)
	}

	if err := db.SetSetting(SettingUpdateHistoryRetentionDays, "10"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	if err := db.SetSetting(SettingBackupHistoryRetentionDays, "10"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	if r := db.PruneHistory(); r.UpdateHistory != 1 || r.BackupRuns != 1 {
		t.Errorf("with retention 10 days the pass deleted %+v, want 1 of each", r)
	}
}
