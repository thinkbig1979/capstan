package database

import (
	"errors"
	"strings"
	"testing"

	"github.com/thinkbig1979/capstan/backend/internal/errdefs"
)

// agent-os-r1kc. (*DB).RetentionDays returned a bare int with no error channel,
// so ANY settings read fault — a closed, locked or corrupt database, not just an
// absent row — was answered with DefaultRetentionDays = 90 and fed straight into
// three irreversible DELETEs (four since agent-os-fn7x.7 added
// docker_cleanup_runs). The original three retention keys are seeded by a
// migration (migrations.go:182, :394, :395), so after migrations an error means
// a fault, never absence, and the comment that justified the swallow was false
// in production. The cleanup key is deliberately NOT seeded: its absent row
// resolves to the default, which is the same answer a seed would give.

// faultRetentionReads makes the settings READ fail while leaving every table the
// prune DELETEs from intact and populated.
//
// The discrimination is the whole point (criterion 6, and the trap that cost
// agent-os-1gqn a cycle): a fixture that breaks the entire database makes the
// DELETE fail too, so "the rows survived" would pass against the UNFIXED code
// and prove nothing. Dropping only `settings` faults exactly one read path.
// TestPruneHistory_FaultFixtureLeavesDeletesWorking is the arm that proves this
// fixture has that property rather than asserting it in a comment.
func faultRetentionReads(t *testing.T, d *DB) {
	t.Helper()
	if _, err := d.db.Exec(`DROP TABLE settings`); err != nil {
		t.Fatalf("drop settings table: %v", err)
	}
	// Positive control on the fixture itself: the read must now fail, and must
	// fail with something OTHER than errdefs.ErrNotFound, or the fix's discriminator
	// would route it to the documented fresh-install default and this test would
	// be measuring the wrong branch.
	_, err := d.GetSetting(SettingLogRetentionDays)
	if err == nil {
		t.Fatalf("fixture did not fault: GetSetting(%q) succeeded", SettingLogRetentionDays)
	}
	if errors.Is(err, errdefs.ErrNotFound) {
		t.Fatalf("fixture produced errdefs.ErrNotFound, which is the absence case, not a fault: %v", err)
	}
}

// seedOldAndFresh puts one 200-day-old row and one fresh row in each of the four
// tables PruneHistory deletes from. At the default 90 days the old rows are
// exactly what an unrefused pass destroys.
func seedOldAndFresh(t *testing.T, d *DB) {
	t.Helper()
	seedUpdateHistory(t, d, "old", 200)
	seedUpdateHistory(t, d, "fresh", 1)
	seedBackupRun(t, d, "old", 200)
	seedBackupRun(t, d, "fresh", 1)
	seedCleanupRun(t, d, "old", 200)
	seedCleanupRun(t, d, "fresh", 1)
	if _, err := d.db.Exec(
		`INSERT INTO action_log (id, user_id, action, detail, created_at)
		 VALUES ('old', 'u', 'login', '{}', datetime('now', '-200 days')),
		        ('fresh', 'u', 'login', '{}', datetime('now'))`); err != nil {
		t.Fatalf("seed action_log: %v", err)
	}
}

// TestPruneHistory_RefusesWhenRetentionUnreadable asserts ROWS, not a log line
// and not an error value: agent-os-obgr established rows-not-logs on the
// neighbouring prune, because only the rows establish that the DELETE did not
// happen.
//
// The operator harm this pins: a 3650-day compliance retention truncated to 90
// by one transient read fault, with no undo and nothing said.
func TestPruneHistory_RefusesWhenRetentionUnreadable(t *testing.T) {
	db := newRetentionTestDB(t)
	seedOldAndFresh(t, db)
	faultRetentionReads(t, db)

	db.PruneHistory()

	for table, want := range map[string]int{
		"update_history":      2,
		"backup_runs":         2,
		"backup_run_items":    2,
		"docker_cleanup_runs": 2,
		"action_log":          2,
	} {
		if got := countRows(t, db, table); got != want {
			t.Errorf("%s has %d rows after a pass whose retention could not be read, want %d — the pass deleted at the default instead of refusing", table, got, want)
		}
	}
}

// TestPruneHistory_FaultFixtureLeavesDeletesWorking is the other half of the
// fixture's two-sided control. It proves faultRetentionReads breaks the READ
// only: every DELETE PruneHistory issues still succeeds and still removes the
// old row. Without this arm, "the rows survived" in the test above would be
// indistinguishable from "the database was too broken to delete anything".
func TestPruneHistory_FaultFixtureLeavesDeletesWorking(t *testing.T) {
	db := newRetentionTestDB(t)
	seedOldAndFresh(t, db)
	faultRetentionReads(t, db)

	if err := db.DeleteOldActionLogs(DefaultRetentionDays); err != nil {
		t.Fatalf("DeleteOldActionLogs under the fault fixture: %v", err)
	}
	if got := countRows(t, db, "action_log"); got != 1 {
		t.Errorf("action_log has %d rows after a direct delete, want 1 — the fixture broke the write path too", got)
	}

	n, err := db.DeleteOldUpdateHistory(DefaultRetentionDays)
	if err != nil {
		t.Fatalf("DeleteOldUpdateHistory under the fault fixture: %v", err)
	}
	if n != 1 {
		t.Errorf("DeleteOldUpdateHistory deleted %d under the fault fixture, want 1 — the fixture broke the write path too", n)
	}

	n, err = db.DeleteOldBackupRuns(DefaultRetentionDays)
	if err != nil {
		t.Fatalf("DeleteOldBackupRuns under the fault fixture: %v", err)
	}
	if n != 1 {
		t.Errorf("DeleteOldBackupRuns deleted %d under the fault fixture, want 1 — the fixture broke the write path too", n)
	}

	n, err = db.DeleteOldDockerCleanupRuns(DefaultRetentionDays)
	if err != nil {
		t.Fatalf("DeleteOldDockerCleanupRuns under the fault fixture: %v", err)
	}
	if n != 1 {
		t.Errorf("DeleteOldDockerCleanupRuns deleted %d under the fault fixture, want 1 — the fixture broke the write path too", n)
	}
}

// TestPruneHistory_AbsentKeysStillPruneAtDefault is the passing side of the same
// instrument (criterion 5): with the settings table PRESENT but the four
// retention rows absent — the documented fresh-install case, before the
// migration's INSERT OR IGNORE — the pass must still run at the default.
//
// Refusing here instead would be a regression dressed as a fix: "refuses" must
// not be satisfiable by a prune that has simply stopped working.
func TestPruneHistory_AbsentKeysStillPruneAtDefault(t *testing.T) {
	db := newRetentionTestDB(t)
	seedOldAndFresh(t, db)
	if _, err := db.db.Exec(`DELETE FROM settings WHERE key IN (?, ?, ?, ?)`,
		SettingLogRetentionDays, SettingUpdateHistoryRetentionDays, SettingBackupHistoryRetentionDays,
		SettingCleanupHistoryRetentionDays); err != nil {
		t.Fatalf("clear seeded retention settings: %v", err)
	}
	if _, err := db.GetSetting(SettingLogRetentionDays); !errors.Is(err, errdefs.ErrNotFound) {
		t.Fatalf("after clearing the rows GetSetting returned %v, want errdefs.ErrNotFound — this arm is not exercising the absence branch", err)
	}

	result := db.PruneHistory()

	if result.UpdateHistory != 1 || result.BackupRuns != 1 || result.CleanupRuns != 1 {
		t.Errorf("PruneHistory with absent retention keys reported %+v, want 1 of each", result)
	}
	for table, want := range map[string]int{
		"update_history":      1,
		"backup_runs":         1,
		"backup_run_items":    1,
		"docker_cleanup_runs": 1,
		"action_log":          1,
	} {
		if got := countRows(t, db, table); got != want {
			t.Errorf("%s has %d rows after a default-retention pass, want %d", table, got, want)
		}
	}
}

// TestRetentionDays_DiscriminatesFaultFromAbsence pins criterion 1 directly on
// the accessor, both sides on the same instrument and the same database.
//
// Note what "same database" buys: the absence arm runs first, on a DB whose
// settings table is present, and the fault arm runs after dropping it. A refusal
// that came from the accessor having simply stopped working would fail the first
// arm, so "returns an error" here cannot be satisfied by a broken function.
func TestRetentionDays_DiscriminatesFaultFromAbsence(t *testing.T) {
	db := newRetentionTestDB(t)

	// Absence: the documented fresh-install case, before the migration's seed.
	days, err := db.RetentionDays("key_that_was_never_seeded")
	if err != nil {
		t.Fatalf("an absent key must resolve to the default, not an error: %v", err)
	}
	if days != DefaultRetentionDays {
		t.Errorf("absent key resolved to %d, want %d", days, DefaultRetentionDays)
	}

	// Configured: the ordinary case, so the arms below cannot both pass on a
	// function that answers everything the same way.
	if err := db.SetSetting(SettingLogRetentionDays, "3650"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	days, err = db.RetentionDays(SettingLogRetentionDays)
	if err != nil {
		t.Fatalf("a configured key must resolve: %v", err)
	}
	if days != 3650 {
		t.Errorf("configured retention resolved to %d, want 3650", days)
	}

	// Fault: the same key, now unreadable. This is the 3650-day compliance
	// retention from the bead's harm analysis; before the fix it came back 90.
	faultRetentionReads(t, db)
	days, err = db.RetentionDays(SettingLogRetentionDays)
	if err == nil {
		t.Fatalf("an unreadable settings table returned (%d, nil), want an error", days)
	}
	if days == DefaultRetentionDays {
		t.Errorf("a read fault resolved to %d, the default — that is the defect this bead is about", days)
	}
	if !strings.Contains(err.Error(), SettingLogRetentionDays) {
		t.Errorf("the refusal must name the key that could not be read, got %q", err)
	}
	if errors.Is(err, errdefs.ErrNotFound) {
		t.Errorf("a fault must not be reported as absence, got %v", err)
	}
}

// faultCleanupRetentionRead makes ONLY the cleanup-history key unreadable,
// leaving the other three keys readable and every table intact.
//
// faultRetentionReads above drops the whole settings table, so every key faults
// together and "the other tables are still pruned" cannot be asked of it. Here
// settings is moved aside and replaced by a view that yields NULL for the one
// key. The row exists (a real 90 is stored first), so the read reaches Scan,
// and scanning NULL into a string fails with a driver error that is neither
// sql.ErrNoRows nor errdefs.ErrNotFound: the fault branch, not the absence one.
func faultCleanupRetentionRead(t *testing.T, d *DB) {
	t.Helper()
	if err := d.SetSetting(SettingCleanupHistoryRetentionDays, "90"); err != nil {
		t.Fatalf("store cleanup retention: %v", err)
	}
	for _, stmt := range []string{
		`ALTER TABLE settings RENAME TO settings_backing`,
		`CREATE VIEW settings AS SELECT key,
			CASE WHEN key = '` + SettingCleanupHistoryRetentionDays + `' THEN NULL ELSE value END AS value
			FROM settings_backing`,
	} {
		if _, err := d.db.Exec(stmt); err != nil {
			t.Fatalf("per-key fault fixture: %v", err)
		}
	}
	// Positive control: the cleanup key now faults, and not as absence.
	_, err := d.GetSetting(SettingCleanupHistoryRetentionDays)
	if err == nil {
		t.Fatalf("fixture did not fault: GetSetting(%q) succeeded", SettingCleanupHistoryRetentionDays)
	}
	if errors.Is(err, errdefs.ErrNotFound) {
		t.Fatalf("fixture produced errdefs.ErrNotFound, which is the absence case, not a fault: %v", err)
	}
	// Negative control: the other three keys still read, so the fault is per-key.
	for _, key := range []string{
		SettingLogRetentionDays, SettingUpdateHistoryRetentionDays, SettingBackupHistoryRetentionDays,
	} {
		if _, err := d.GetSetting(key); err != nil {
			t.Fatalf("fixture faulted %q too, so it is not per-key: %v", key, err)
		}
	}
}

// TestPruneHistory_RefusesCleanupPruneWhenOnlyItsRetentionUnreadable: the
// cleanup prune is refused on its own key's fault, and that refusal does not
// skip the other three tables (PruneHistory's "each table independently" rule).
func TestPruneHistory_RefusesCleanupPruneWhenOnlyItsRetentionUnreadable(t *testing.T) {
	db := newRetentionTestDB(t)
	seedOldAndFresh(t, db)
	faultCleanupRetentionRead(t, db)

	result := db.PruneHistory()

	if result.CleanupRuns != 0 || result.UpdateHistory != 1 || result.BackupRuns != 1 {
		t.Errorf("PruneHistory reported %+v, want CleanupRuns=0 and 1 for each other table", result)
	}
	for table, want := range map[string]int{
		"docker_cleanup_runs": 2,
		"update_history":      1,
		"backup_runs":         1,
		"backup_run_items":    1,
		"action_log":          1,
	} {
		if got := countRows(t, db, table); got != want {
			t.Errorf("%s has %d rows after the pass, want %d", table, got, want)
		}
	}
}
