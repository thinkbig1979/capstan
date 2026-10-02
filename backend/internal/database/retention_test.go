package database

import (
	"fmt"
	"testing"
	"time"

	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// update_history, backup_runs and backup_run_items grew without bound: only
// sessions and action_log were ever pruned. The growth is slow, silent and only
// shows up on exactly the deployments that matter — long-lived ones with
// scheduled updates or backups switched on (agent-os-0jp). docker_cleanup_runs
// repeated the same omission when it was added, and gained its own retention in
// agent-os-fn7x.7.

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

// seedCleanupRun records a cleanup run started daysAgo days in the past through
// CreateDockerCleanupRun, the production writer, so the row carries the spelling
// canonicalTimestamp actually stores rather than one a fixture picked. Rows are
// only ever written when a run finishes, so finished_at is always set.
func seedCleanupRun(t *testing.T, d *DB, id string, daysAgo int) {
	t.Helper()
	seedCleanupRunAt(t, d, id, time.Now().AddDate(0, 0, -daysAgo).UTC().Format(time.RFC3339))
}

func seedCleanupRunAt(t *testing.T, d *DB, id, startedAt string) {
	t.Helper()
	if err := d.CreateDockerCleanupRun(&models.DockerCleanupRun{
		ID: id, Trigger: "scheduled", Status: "success",
		StartedAt: startedAt, FinishedAt: &startedAt, MinAgeHours: 24,
	}); err != nil {
		t.Fatalf("seed docker_cleanup_runs %s: %v", id, err)
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

// TestDeleteOldDockerCleanupRuns_RespectsBoundary pins both sides of the cutoff
// in one run: a row one day inside the window is KEPT and a row one day outside
// it is DELETED. Whole-day margins, because SQLite computes the cutoff at DELETE
// time and a sub-day margin would race the clock.
func TestDeleteOldDockerCleanupRuns_RespectsBoundary(t *testing.T) {
	db := newRetentionTestDB(t)
	const retention = 30
	seedCleanupRun(t, db, "ancient", 400)
	seedCleanupRun(t, db, "just-outside", retention+1)
	seedCleanupRun(t, db, "just-inside", retention-1)

	deleted, err := db.DeleteOldDockerCleanupRuns(retention)
	if err != nil {
		t.Fatalf("DeleteOldDockerCleanupRuns: %v", err)
	}
	if deleted != 2 {
		t.Errorf("deleted %d runs, want the 2 older than %d days", deleted, retention)
	}
	var left string
	if err := db.db.QueryRow(`SELECT id FROM docker_cleanup_runs`).Scan(&left); err != nil || left != "just-inside" {
		t.Errorf("remaining run = %q (err %v), want only the one inside the window", left, err)
	}
}

// TestDeleteOldDockerCleanupRuns_StoredSpellingMatchesCutoff pins the premise
// the TEXT comparison rests on. The cutoff is spelled
// strftime('%Y-%m-%dT%H:%M:%SZ', ...), fixed width, and `<` between two TEXT
// values is only an instant comparison when both sides share that spelling: a
// sub-second '...:00.5Z' sorts BELOW '...:00Z', and an offset spelling compares
// its local wall clock. The writer normalises through canonicalTimestamp, so
// every stored started_at must be its own strftime() rendering, including for
// inputs given with a fraction and a non-UTC offset.
func TestDeleteOldDockerCleanupRuns_StoredSpellingMatchesCutoff(t *testing.T) {
	db := newRetentionTestDB(t)
	east := time.FixedZone("UTC+2", 2*3600)
	west := time.FixedZone("UTC-5", -5*3600)
	outside := time.Now().AddDate(0, 0, -31).Add(500 * time.Millisecond).In(east).Format(time.RFC3339Nano)
	inside := time.Now().AddDate(0, 0, -29).Add(500 * time.Millisecond).In(west).Format(time.RFC3339Nano)
	seedCleanupRunAt(t, db, "outside-east", outside)
	seedCleanupRunAt(t, db, "inside-west", inside)

	var mismatched int
	if err := db.db.QueryRow(`SELECT COUNT(*) FROM docker_cleanup_runs
		WHERE started_at IS NOT strftime('%Y-%m-%dT%H:%M:%SZ', started_at)`).Scan(&mismatched); err != nil {
		t.Fatalf("spelling check: %v", err)
	}
	if mismatched != 0 {
		t.Errorf("%d stored started_at values are not in the cutoff's fixed-width UTC spelling", mismatched)
	}

	deleted, err := db.DeleteOldDockerCleanupRuns(30)
	if err != nil {
		t.Fatalf("DeleteOldDockerCleanupRuns: %v", err)
	}
	var left string
	if err := db.db.QueryRow(`SELECT id FROM docker_cleanup_runs`).Scan(&left); err != nil || deleted != 1 || left != "inside-west" {
		t.Errorf("deleted %d, remaining %q (err %v); want 1 deleted and inside-west kept", deleted, left, err)
	}
}

// TestDeleteOldDockerCleanupRuns_PrunesRowOnTheCutoffDate mirrors the
// agent-os-h8qa arms in retention_cutoff_spelling_test.go: a row older than the
// cutoff by instant but on the cutoff's own calendar date must go, which a
// space-separated datetime() cutoff got wrong.
func TestDeleteOldDockerCleanupRuns_PrunesRowOnTheCutoffDate(t *testing.T) {
	db := newRetentionTestDB(t)
	seedCleanupRunAt(t, db, "on-cutoff", onCutoffDateUTC(90))
	seedCleanupRunAt(t, db, "newer", time.Now().UTC().AddDate(0, 0, -90).Add(time.Hour).Format(time.RFC3339))

	deleted, err := db.DeleteOldDockerCleanupRuns(90)
	if err != nil {
		t.Fatalf("DeleteOldDockerCleanupRuns: %v", err)
	}
	var left string
	if err := db.db.QueryRow(`SELECT id FROM docker_cleanup_runs`).Scan(&left); err != nil || deleted != 1 || left != "newer" {
		t.Errorf("deleted %d, remaining %q (err %v); want 1 deleted and only the newer run kept", deleted, left, err)
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
			got, err := db.RetentionDays("test_retention_key")
			if err != nil {
				t.Fatalf("RetentionDays(%q): %v", tc.setting, err)
			}
			if got != tc.want {
				t.Errorf("RetentionDays(%q) = %d, want %d", tc.setting, got, tc.want)
			}
		})
	}
}

// TestRetentionDays_UnsetKeyUsesDefault covers a fresh install where the
// migration's INSERT OR IGNORE has not seeded the key.
func TestRetentionDays_UnsetKeyUsesDefault(t *testing.T) {
	db := newRetentionTestDB(t)
	got, err := db.RetentionDays("key_that_does_not_exist")
	if err != nil {
		t.Fatalf("an absent key must not be an error, it is the fresh-install case: %v", err)
	}
	if got != DefaultRetentionDays {
		t.Errorf("RetentionDays for a missing key = %d, want %d", got, DefaultRetentionDays)
	}
}

// TestPruneHistory_PrunesEveryTable covers the pass the daily ticker runs: one
// table failing must not skip the others, and all four settings are honoured.
func TestPruneHistory_PrunesEveryTable(t *testing.T) {
	db := newRetentionTestDB(t)

	seedUpdateHistory(t, db, "old", 200)
	seedUpdateHistory(t, db, "fresh", 1)
	seedBackupRun(t, db, "old", 200)
	seedBackupRun(t, db, "fresh", 1)
	seedCleanupRun(t, db, "old", 200)
	seedCleanupRun(t, db, "fresh", 1)
	if _, err := db.db.Exec(
		`INSERT INTO action_log (id, user_id, action, detail, created_at)
		 VALUES ('old', 'u', 'login', '{}', datetime('now', '-200 days')),
		        ('fresh', 'u', 'login', '{}', datetime('now'))`); err != nil {
		t.Fatalf("seed action_log: %v", err)
	}

	result := db.PruneHistory()

	if result.UpdateHistory != 1 || result.BackupRuns != 1 || result.CleanupRuns != 1 {
		t.Errorf("PruneHistory reported %+v, want 1 of each", result)
	}
	for table, want := range map[string]int{
		"update_history":      1,
		"backup_runs":         1,
		"backup_run_items":    1,
		"docker_cleanup_runs": 1,
		"action_log":          1,
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
	seedCleanupRun(t, db, "twenty-days", 20)

	// Default is 90 days, so nothing should go yet.
	if r := db.PruneHistory(); r.UpdateHistory != 0 || r.BackupRuns != 0 || r.CleanupRuns != 0 {
		t.Fatalf("default retention deleted %+v, want nothing", r)
	}

	for _, key := range []string{
		SettingUpdateHistoryRetentionDays, SettingBackupHistoryRetentionDays, SettingCleanupHistoryRetentionDays,
	} {
		if err := db.SetSetting(key, "10"); err != nil {
			t.Fatalf("SetSetting(%s): %v", key, err)
		}
	}

	if r := db.PruneHistory(); r.UpdateHistory != 1 || r.BackupRuns != 1 || r.CleanupRuns != 1 {
		t.Errorf("with retention 10 days the pass deleted %+v, want 1 of each", r)
	}
}

// TestPruneHistory_CleanupHistoryUsesItsOwnSetting proves the cleanup prune
// reads max_cleanup_history_retention_days and not a neighbour's key: with only
// the cleanup retention lowered, a 40-day-old cleanup run goes while a 40-day-old
// backup run, governed by the untouched 90-day default, stays.
func TestPruneHistory_CleanupHistoryUsesItsOwnSetting(t *testing.T) {
	db := newRetentionTestDB(t)
	seedCleanupRun(t, db, "forty-days", 40)
	seedCleanupRun(t, db, "twenty-days", 20)
	seedBackupRun(t, db, "forty-days", 40)

	if err := db.SetSetting(SettingCleanupHistoryRetentionDays, "30"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	r := db.PruneHistory()

	if r.CleanupRuns != 1 || r.BackupRuns != 0 {
		t.Errorf("PruneHistory reported %+v, want CleanupRuns=1 and BackupRuns=0", r)
	}
	var left string
	if err := db.db.QueryRow(`SELECT id FROM docker_cleanup_runs`).Scan(&left); err != nil || left != "twenty-days" {
		t.Errorf("remaining cleanup run = %q (err %v), want twenty-days", left, err)
	}
	if got := countRows(t, db, "backup_runs"); got != 1 {
		t.Errorf("backup_runs has %d rows, want 1: the cleanup setting leaked into the backup prune", got)
	}
}
