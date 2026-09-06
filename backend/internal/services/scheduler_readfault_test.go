package services

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/thinkbig1979/capstan/backend/internal/database"
)

// ---------------------------------------------------------------------------
// agent-os-rltu — loadApplySchedule must REFUSE, not apply, on a read fault
// ---------------------------------------------------------------------------
//
// The complaint: an operator configures a maintenance window, the settings read
// fails, and the scheduler applies container updates NOW — outside the window
// they explicitly asked for — on the strength of a failed query. agent-os-r1kc
// settled the identical shape for retention the other way (refuse rather than
// act at a default when the setting cannot be read); this pins the same rule
// here.
//
// TWO SITES, both in loadApplySchedule and both a read fault:
//   - the update_apply_time / update_apply_days reads (the bead's site)
//   - the update_apply_mode read 13 lines above it (its unnamed sibling)
//
// And ONE branch that must NOT change: sql.ErrNoRows on update_apply_mode is
// the pre-migration-14 state, where immediate is exactly what migration 14
// seeds. That is a legitimately known value, not a fault, and it keeps
// applying. TestApplyScheduleAbsentModeStillApplies is that arm — without it a
// mutant that refuses on EVERY error would pass.

// rltuPastTheGate is only reachable INSIDE RunAutoUpdates, past its own
// auto_update_enabled gate (scheduler.go, "Failed to get auto-update
// policies"). With auto_update_policies dropped it is therefore a direct
// read-out of the one thing these tests are about: did runCycle hand this pass
// to RunAutoUpdates at all?
//
// A log line is the instrument rather than the fake checker's updatedIDs
// because UpdateContainer is only reached once a matching policy exists, and
// the policies table is exactly what this fixture destroys.
const rltuPastTheGate = "Failed to get auto-update policies"

// rltuFixture is a migrated on-disk database with auto-updates ON and the
// policies table dropped, wired to a scheduler whose logs land in the returned
// buffer. dataDir is returned so a test can corrupt one settings row.
func rltuFixture(t *testing.T) (*database.DB, string, *bytes.Buffer, *SchedulerService) {
	t.Helper()
	db, dataDir := koy9HealthyDB(t)
	if err := db.SetSetting("auto_update_enabled", "true"); err != nil {
		t.Fatalf("seed auto_update_enabled: %v", err)
	}
	koy9DropTable(t, dataDir, "auto_update_policies")

	var buf bytes.Buffer
	return db, dataDir, &buf, NewSchedulerService(&fakeUpdateChecker{}, db, koy9Logger(&buf), nil)
}

// rltuNullSetting makes ONE settings row unreadable while leaving every other
// row readable, which is the whole difficulty: dropping or closing the store
// makes the update_apply_mode read fail too, so it can never reach the
// time/days branch, and it would break the instrument's own read as well.
//
// The settings table declares value NOT NULL, so the column is rebuilt without
// that constraint first. GetSetting scans into a string, so a NULL value
// returns "converting NULL to string is unsupported" — a genuine error that is
// NOT sql.ErrNoRows, from one key only.
func rltuNullSetting(t *testing.T, dataDir, key string) {
	t.Helper()
	raw := koy9Raw(t, dataDir)
	for _, stmt := range []string{
		`ALTER TABLE settings RENAME TO settings_rebuild`,
		`CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT)`,
		`INSERT INTO settings SELECT * FROM settings_rebuild`,
		`DROP TABLE settings_rebuild`,
	} {
		if _, err := raw.Exec(stmt); err != nil {
			t.Fatalf("rebuild settings (%s): %v", stmt, err)
		}
	}
	if _, err := raw.Exec("UPDATE settings SET value = NULL WHERE key = ?", key); err != nil {
		t.Fatalf("null out %s: %v", key, err)
	}
}

// rltuRequireFault pins the premise rather than assuming the fixture worked: a
// test that silently failed to corrupt the row would pass for the wrong reason.
func rltuRequireFault(t *testing.T, db *database.DB, key string) {
	t.Helper()
	if _, err := db.GetSetting(key); err == nil {
		t.Fatalf("%s must be unreadable for this test to mean anything, but it read back cleanly", key)
	} else if errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("%s must fail with a genuine fault, not sql.ErrNoRows — got %v", key, err)
	}
}

// rltuRequireReadable is the other half: the keys this test is NOT corrupting
// must still read, or the fixture has quietly moved the fault to a different
// branch than the one under test.
func rltuRequireReadable(t *testing.T, db *database.DB, key, want string) {
	t.Helper()
	got, err := db.GetSetting(key)
	if err != nil {
		t.Fatalf("%s must still be readable, got error %v", key, err)
	}
	if got != want {
		t.Fatalf("%s = %q, want %q", key, got, want)
	}
}

// TestApplyScheduleUnreadableTimeRefusesToApply is the bead's own site:
// update_apply_mode says "scheduled", and the schedule itself cannot be read.
// Before the fix this fell back to immediate and applied the updates NOW,
// overriding the maintenance window the operator configured.
func TestApplyScheduleUnreadableTimeRefusesToApply(t *testing.T) {
	db, dataDir, buf, s := rltuFixture(t)
	if err := db.SetSetting("update_apply_mode", "scheduled"); err != nil {
		t.Fatalf("seed update_apply_mode: %v", err)
	}
	rltuNullSetting(t, dataDir, "update_apply_time")

	rltuRequireReadable(t, db, "update_apply_mode", "scheduled")
	rltuRequireFault(t, db, "update_apply_time")

	s.runCycle(context.Background())

	if got := buf.String(); strings.Contains(got, rltuPastTheGate) {
		t.Fatalf("the maintenance window could not be read, so no update may be applied on this pass, "+
			"but runCycle handed the pass to RunAutoUpdates anyway.\ngot log:\n%s", got)
	}
}

// TestApplyScheduleUnreadableModeRefusesToApply is the sibling site 13 lines
// above the bead's, found by the class sweep: a genuine fault on the
// update_apply_mode read also resolved to immediate and applied.
func TestApplyScheduleUnreadableModeRefusesToApply(t *testing.T) {
	db, dataDir, buf, s := rltuFixture(t)
	rltuNullSetting(t, dataDir, "update_apply_mode")

	rltuRequireFault(t, db, "update_apply_mode")
	rltuRequireReadable(t, db, "auto_update_enabled", "true")

	s.runCycle(context.Background())

	if got := buf.String(); strings.Contains(got, rltuPastTheGate) {
		t.Fatalf("the apply mode could not be read, so the scheduler cannot know whether a maintenance "+
			"window applies and must not apply, but runCycle handed the pass to RunAutoUpdates.\ngot log:\n%s", got)
	}
}

// TestApplyScheduleAbsentModeStillApplies is the two-sided arm. An ABSENT
// update_apply_mode row is the pre-migration-14 database, and immediate is
// precisely what migration 14 seeds, so this is a known value rather than a
// fault and must keep applying. A mutant that refuses on every non-nil error
// fails here.
func TestApplyScheduleAbsentModeStillApplies(t *testing.T) {
	db, dataDir, buf, s := rltuFixture(t)
	koy9DeleteSetting(t, dataDir, "update_apply_mode")

	if _, err := db.GetSetting("update_apply_mode"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("after deleting the row the read must be sql.ErrNoRows, got %v", err)
	}

	s.runCycle(context.Background())

	if got := buf.String(); !strings.Contains(got, rltuPastTheGate) {
		t.Fatalf("an absent update_apply_mode is the seeded default, not a fault, and must still apply, "+
			"but runCycle never reached RunAutoUpdates.\ngot log:\n%s", got)
	}
}

// TestApplyScheduleImmediateModeStillApplies is the plain control: nothing
// corrupted, mode is the seeded immediate. It proves rltuPastTheGate is
// reachable under this fixture at all, so a silent "absent" in the two refusal
// tests above cannot be an instrument that never fires.
func TestApplyScheduleImmediateModeStillApplies(t *testing.T) {
	db, _, buf, s := rltuFixture(t)
	if err := db.SetSetting("update_apply_mode", "immediate"); err != nil {
		t.Fatalf("seed update_apply_mode: %v", err)
	}

	s.runCycle(context.Background())

	if got := buf.String(); !strings.Contains(got, rltuPastTheGate) {
		t.Fatalf("immediate mode must apply on the scan tick, but runCycle never reached "+
			"RunAutoUpdates.\ngot log:\n%s", got)
	}
}

// TestApplyScheduleRebuiltSettingsTableLeavesTheInstrumentWorking is the
// fixture control the two refusal tests rest on. rltuNullSetting rebuilds the
// settings table, and a rebuild that broke every subsequent read would make
// those tests pass for the wrong reason — the same trap that cost
// agent-os-1gqn a cycle. Here the table is rebuilt and a seeded key NOTHING on
// this path reads (update_scan_interval) is nulled, so the instrument must still fire.
func TestApplyScheduleRebuiltSettingsTableLeavesTheInstrumentWorking(t *testing.T) {
	db, dataDir, buf, s := rltuFixture(t)
	if err := db.SetSetting("update_apply_mode", "immediate"); err != nil {
		t.Fatalf("seed update_apply_mode: %v", err)
	}
	rltuNullSetting(t, dataDir, "update_scan_interval")

	rltuRequireFault(t, db, "update_scan_interval")
	rltuRequireReadable(t, db, "update_apply_mode", "immediate")

	s.runCycle(context.Background())

	if got := buf.String(); !strings.Contains(got, rltuPastTheGate) {
		t.Fatalf("rebuilding the settings table must not break the reads these tests depend on, "+
			"but runCycle never reached RunAutoUpdates.\ngot log:\n%s", got)
	}
}
