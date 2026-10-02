package services

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/thinkbig1979/capstan/backend/internal/database"
)

// ---------------------------------------------------------------------------
// agent-os-ehie — an apply / auto-update pass that gives up must leave a trace
// the Updates tab shows, not only a log line
// ---------------------------------------------------------------------------
//
// Six sites gave up after a logger.Error and returned, so containers silently
// stopped getting updates and only the server log said why:
//   - logApplyArming: apply settings unreadable           -> applyArmErrorKey
//   - logApplyArming: scheduled but no next run computable -> applyArmErrorKey
//   - applyNow: cached updates unreadable                 -> applyLastErrorKey
//   - RunAutoUpdates: auto_update_enabled unreadable      -> applyLastErrorKey
//   - RunAutoUpdates: auto-update policies unreadable     -> applyLastErrorKey
//   - runCycle: apply settings unreadable on a scan tick  -> applyLastErrorKey
//
// Each failure test is paired with the success path that must clear the value,
// so a mutant that writes on every call, or never clears, fails one of the two.

const ehieStale = "stale error from an earlier pass"

func ehieSetting(t *testing.T, db *database.DB, key string) string {
	t.Helper()
	v, err := db.GetSetting(key)
	if err != nil {
		t.Fatalf("read %s: %v", key, err)
	}
	return v
}

func ehieRequireSet(t *testing.T, db *database.DB, key, wantSubstr string) {
	t.Helper()
	got := ehieSetting(t, db, key)
	if got == "" {
		t.Fatalf("%s must record why the pass applied nothing, but it is empty", key)
	}
	if !strings.Contains(got, wantSubstr) {
		t.Fatalf("%s = %q, want it to mention %q", key, got, wantSubstr)
	}
}

func ehieRequireCleared(t *testing.T, db *database.DB, key string) {
	t.Helper()
	if got := ehieSetting(t, db, key); got != "" {
		t.Fatalf("%s must be cleared by a successful pass, got %q", key, got)
	}
}

func ehieSeed(t *testing.T, db *database.DB, key, value string) {
	t.Helper()
	if err := db.SetSetting(key, value); err != nil {
		t.Fatalf("seed %s: %v", key, err)
	}
}

// ehieScheduler is a migrated on-disk database wired to a scheduler. dataDir is
// returned so a test can corrupt one row or drop one table.
func ehieScheduler(t *testing.T) (*database.DB, string, *SchedulerService) {
	t.Helper()
	db, dataDir := koy9HealthyDB(t)
	var buf bytes.Buffer
	return db, dataDir, NewSchedulerService(&fakeUpdateChecker{}, db, koy9Logger(&buf), nil)
}

// --- logApplyArming -------------------------------------------------------

func TestEhieArmingUnreadableRecordsError(t *testing.T) {
	db, _, s := ehieScheduler(t)

	s.logApplyArming(applySchedule{unreadable: true}, time.Time{}, false)

	ehieRequireSet(t, db, applyArmErrorKey, "could not be read")
}

func TestEhieArmingScheduledNotArmedRecordsError(t *testing.T) {
	db, _, s := ehieScheduler(t)
	schedule, err := ParseDailySchedule("03:00", "1")
	if err != nil {
		t.Fatalf("parse schedule: %v", err)
	}

	s.logApplyArming(applySchedule{scheduled: true, schedule: schedule}, time.Time{}, false)

	ehieRequireSet(t, db, applyArmErrorKey, "no next run")
}

func TestEhieArmingImmediateClearsError(t *testing.T) {
	db, _, s := ehieScheduler(t)
	ehieSeed(t, db, applyArmErrorKey, ehieStale)

	s.logApplyArming(applySchedule{}, time.Time{}, false)

	ehieRequireCleared(t, db, applyArmErrorKey)
}

func TestEhieArmingScheduledArmedClearsError(t *testing.T) {
	db, _, s := ehieScheduler(t)
	ehieSeed(t, db, applyArmErrorKey, ehieStale)
	schedule, err := ParseDailySchedule("03:00", "1")
	if err != nil {
		t.Fatalf("parse schedule: %v", err)
	}
	cfg := applySchedule{scheduled: true, schedule: schedule}
	next, armed := nextApplyInstant(cfg, time.Now())
	if !armed {
		t.Fatal("premise: a parsed one-day schedule must arm")
	}

	s.logApplyArming(cfg, next, armed)

	ehieRequireCleared(t, db, applyArmErrorKey)
}

// --- applyNow -------------------------------------------------------------

func TestEhieApplyNowUnreadableCacheRecordsError(t *testing.T) {
	db, dataDir, s := ehieScheduler(t)
	koy9DropTable(t, dataDir, "cached_updates")
	if _, err := db.GetCachedUpdates(); err == nil {
		t.Fatal("premise: cached_updates must be unreadable")
	}

	if !s.applyNow(context.Background()) {
		t.Fatal("applyNow must run (not defer) on an idle scheduler")
	}

	ehieRequireSet(t, db, applyLastErrorKey, "cached updates")
}

// --- RunAutoUpdates -------------------------------------------------------

func TestEhieRunAutoUpdatesUnreadableEnabledRecordsError(t *testing.T) {
	db, dataDir, s := ehieScheduler(t)
	rltuNullSetting(t, dataDir, "auto_update_enabled")
	rltuRequireFault(t, db, "auto_update_enabled")

	s.RunAutoUpdates(context.Background(), nil)

	ehieRequireSet(t, db, applyLastErrorKey, "auto_update_enabled")
}

func TestEhieRunAutoUpdatesUnreadablePoliciesRecordsError(t *testing.T) {
	db, dataDir, s := ehieScheduler(t)
	ehieSeed(t, db, "auto_update_enabled", "true")
	koy9DropTable(t, dataDir, "auto_update_policies")

	s.RunAutoUpdates(context.Background(), nil)

	ehieRequireSet(t, db, applyLastErrorKey, "policies")
}

func TestEhieRunAutoUpdatesPastItsReadsClearsError(t *testing.T) {
	db, _, s := ehieScheduler(t)
	ehieSeed(t, db, "auto_update_enabled", "true")
	ehieSeed(t, db, applyLastErrorKey, ehieStale)

	s.RunAutoUpdates(context.Background(), nil)

	ehieRequireCleared(t, db, applyLastErrorKey)
}

// Auto-update switched off is the operator's choice, read cleanly: nothing is
// expected to apply, so an error from an earlier pass no longer describes the
// system and must not stay on screen.
func TestEhieRunAutoUpdatesDisabledClearsError(t *testing.T) {
	db, _, s := ehieScheduler(t)
	ehieSeed(t, db, "auto_update_enabled", "false")
	ehieSeed(t, db, applyLastErrorKey, ehieStale)

	s.RunAutoUpdates(context.Background(), nil)

	ehieRequireCleared(t, db, applyLastErrorKey)
}

// A disabled pass is not a failure either: it must not write an error.
func TestEhieRunAutoUpdatesDisabledRecordsNothing(t *testing.T) {
	db, _, s := ehieScheduler(t)
	ehieSeed(t, db, "auto_update_enabled", "false")

	s.RunAutoUpdates(context.Background(), nil)

	if v, err := db.GetSetting(applyLastErrorKey); err == nil && v != "" {
		t.Fatalf("a disabled pass is not a failure, but %s = %q", applyLastErrorKey, v)
	}
}

// --- runCycle (found by the class sweep) ----------------------------------

func TestEhieRunCycleUnreadableApplySettingsRecordsError(t *testing.T) {
	db, dataDir, s := ehieScheduler(t)
	ehieSeed(t, db, "auto_update_enabled", "true")
	rltuNullSetting(t, dataDir, "update_apply_mode")
	rltuRequireFault(t, db, "update_apply_mode")

	s.runCycle(context.Background())

	ehieRequireSet(t, db, applyLastErrorKey, "could not be read")
}
