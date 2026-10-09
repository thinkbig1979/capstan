package services

import (
	"bytes"
	"context"
	"testing"
)

// A finished scan records update_scan_last_run and clears
// update_scan_last_error. Both describe the same scan, so they are saved
// together or not at all (agent-os-qz9b). Before the fix they were two
// SetSetting calls: when the second failed, the Updates tab showed the new
// scan time next to the previous scan's error, which reads as if the latest
// scan failed.
//
// The fault is a trigger on a second connection that aborts only the write
// clearing update_scan_last_error. The seeded rows already exist, so the
// write is an upsert (INSERT OR REPLACE); the fault arm's
// update_scan_last_error assertion is what shows the trigger fired on that
// path.

const (
	qz9bSeedRun   = "2000-01-01T00:00:00Z"
	qz9bSeedError = "qz9b previous scan failed"
)

func qz9bFixture(t *testing.T) (*SchedulerService, string, func(key string) string) {
	t.Helper()
	db, dataDir := koy9HealthyDB(t)
	for key, value := range map[string]string{
		"update_scan_last_run":   qz9bSeedRun,
		"update_scan_last_error": qz9bSeedError,
	} {
		if err := db.SetSetting(key, value); err != nil {
			t.Fatalf("seed %s: %v", key, err)
		}
	}
	get := func(key string) string {
		t.Helper()
		v, err := db.GetSetting(key)
		if err != nil {
			t.Fatalf("read %s: %v", key, err)
		}
		return v
	}
	var buf bytes.Buffer
	return NewSchedulerService(&fakeUpdateChecker{}, db, koy9Logger(&buf), nil), dataDir, get
}

func TestPerformScan_ScanResultKeysSavedTogether(t *testing.T) {
	t.Run("fault on clearing the error leaves the scan time unchanged", func(t *testing.T) {
		s, dataDir, get := qz9bFixture(t)
		if _, err := koy9Raw(t, dataDir).Exec(`CREATE TRIGGER qz9b_fail_clear BEFORE INSERT ON settings
			WHEN NEW.key = 'update_scan_last_error' AND NEW.value = ''
			BEGIN SELECT RAISE(ABORT, 'qz9b injected storage fault'); END`); err != nil {
			t.Fatalf("create trigger: %v", err)
		}

		if _, err := s.performScan(context.Background()); err != nil {
			t.Fatalf("performScan: %v", err)
		}

		if got := get("update_scan_last_error"); got != qz9bSeedError {
			t.Errorf("update_scan_last_error = %q, want the seeded %q: the injected fault did not fire", got, qz9bSeedError)
		}
		if got := get("update_scan_last_run"); got != qz9bSeedRun {
			t.Errorf("update_scan_last_run = %q, want the seeded %q: the scan time was saved although clearing the error failed", got, qz9bSeedRun)
		}
	})

	t.Run("control: with no fault both keys are written", func(t *testing.T) {
		s, _, get := qz9bFixture(t)

		if _, err := s.performScan(context.Background()); err != nil {
			t.Fatalf("performScan: %v", err)
		}

		if got := get("update_scan_last_error"); got != "" {
			t.Errorf("update_scan_last_error = %q, want it cleared", got)
		}
		if got := get("update_scan_last_run"); got == qz9bSeedRun {
			t.Errorf("update_scan_last_run still %q, want a fresh scan time", got)
		}
	})
}
