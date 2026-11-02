package services

import (
	"bytes"
	"context"
	"testing"

	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// A scan whose results could not be cached must not report itself fresh
// (agent-os-bog2). Before the fix the cache write error was only logged, and
// the scan then saved update_scan_last_run=now and cleared
// update_scan_last_error, so the Updates tab showed "scanned just now" next to
// the previous scan's list.
//
// The fault is a trigger on a second connection that aborts every insert into
// cached_updates; the checker returns one result so the insert happens.
func TestPerformScan_CacheWriteFailureLeavesScanStale(t *testing.T) {
	const seedRun = "2000-01-01T00:00:00Z"

	setup := func(t *testing.T) (*SchedulerService, string, func(string) string) {
		t.Helper()
		db, dataDir := koy9HealthyDB(t)
		if err := db.SetSetting("update_scan_last_run", seedRun); err != nil {
			t.Fatalf("seed: %v", err)
		}
		get := func(key string) string {
			t.Helper()
			v, err := db.GetSetting(key)
			if err != nil {
				t.Fatalf("read %s: %v", key, err)
			}
			return v
		}
		checker := &fakeUpdateChecker{checkFn: func(context.Context) ([]models.ContainerUpdateInfo, error) {
			return []models.ContainerUpdateInfo{{ContainerID: "c1", ContainerName: "web", ImageRef: "nginx:latest"}}, nil
		}}
		var buf bytes.Buffer
		return NewSchedulerService(checker, db, koy9Logger(&buf), nil), dataDir, get
	}

	t.Run("cache write fails: scan time kept, error recorded", func(t *testing.T) {
		s, dataDir, get := setup(t)
		if _, err := koy9Raw(t, dataDir).Exec(`CREATE TRIGGER bog2_fail_cache BEFORE INSERT ON cached_updates
			BEGIN SELECT RAISE(ABORT, 'bog2 injected storage fault'); END`); err != nil {
			t.Fatalf("create trigger: %v", err)
		}

		if _, err := s.performScan(context.Background()); err == nil {
			t.Error("performScan returned nil although the scan results were not cached")
		}
		if got := get("update_scan_last_run"); got != seedRun {
			t.Errorf("update_scan_last_run = %q, want the seeded %q: the scan was marked fresh although its results were not cached", got, seedRun)
		}
		if got := get("update_scan_last_error"); got == "" {
			t.Error("update_scan_last_error is empty, want the cache write failure recorded")
		}
	})

	t.Run("control: with no fault the scan time advances and the error clears", func(t *testing.T) {
		s, _, get := setup(t)
		if err := s.db.SetSetting("update_scan_last_error", "previous"); err != nil {
			t.Fatalf("seed: %v", err)
		}

		if _, err := s.performScan(context.Background()); err != nil {
			t.Fatalf("performScan: %v", err)
		}
		if got := get("update_scan_last_run"); got == seedRun {
			t.Errorf("update_scan_last_run still %q, want a fresh scan time", got)
		}
		if got := get("update_scan_last_error"); got != "" {
			t.Errorf("update_scan_last_error = %q, want it cleared", got)
		}
	})
}
