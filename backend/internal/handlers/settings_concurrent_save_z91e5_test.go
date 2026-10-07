package handlers

import (
	"net/http"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestUpdateUpdateSettings_ConcurrentSavesLeaveSchedulerMatchingDB is
// agent-os-z91e.5's handler half. Each save reads the old interval, writes the
// new one and then Restarts or Stops the scheduler; with nothing serialising
// two saves, a save of 15 and a save of 0 can interleave so the runtime ends
// running while the DB says 0, or stopped while it says 15. After every pair
// the stored interval and IsRunning() must agree.
func TestUpdateUpdateSettings_ConcurrentSavesLeaveSchedulerMatchingDB(t *testing.T) {
	router, db, sched := newUpdateSettingsFixture(t)

	for i := 0; i < 50; i++ {
		var wg sync.WaitGroup
		start := make(chan struct{})
		for _, body := range []string{`{"scanIntervalMinutes":15}`, `{"scanIntervalMinutes":0}`} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				if w := putUpdateSettings(t, router, body); w.Code != http.StatusOK {
					t.Errorf("PUT %s: status %d", body, w.Code)
				}
			}()
		}
		close(start)
		wg.Wait()

		stored, err := db.GetSetting("update_scan_interval")
		require.NoError(t, err)
		if want := stored != "0"; sched.IsRunning() != want {
			t.Fatalf("iteration %d: stored interval %q but IsRunning() = %v", i, stored, sched.IsRunning())
		}
	}
}
