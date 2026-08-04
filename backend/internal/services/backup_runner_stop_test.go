package services

import (
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/database"
)

// TestBackupRunnerRegistry_StopIsIdempotent is the agent-os-7a5 regression
// test: wiring BackupRunnerRegistry.Stop into main.go's shutdown path adds a
// second real-world caller (main.go, alongside every test's t.Cleanup), so a
// double Stop() must be safe rather than a latent panic.
//
// No LaunchX call is needed to exercise this — Stop's only side effects are
// close(reg.gcStop) and reg.wg.Wait(), neither of which touches db or svc, so
// both are passed as nil: the interface/pointer values are never dereferenced
// on this path.
func TestBackupRunnerRegistry_StopIsIdempotent(t *testing.T) {
	reg := NewBackupRunnerRegistry((*database.DB)(nil), nil, slog.Default())

	reg.Stop()
	reg.Stop() // must not panic: close of an already-closed channel
}

// TestStopWithTimeout_ExpiresWhileRunInFlight pins the bound-expiry contract
// agent-os-7a5 adds for main.go's graceful shutdown: StopWithTimeout must
// return false (not block forever, not panic) while an exec goroutine is
// still in flight, and the goroutine must be left running rather than
// aborted. onRun blocks the fake rclone runner on a channel so the sync
// deterministically outlives a short timeout, instead of racing a sleep.
func TestStopWithTimeout_ExpiresWhileRunInFlight(t *testing.T) {
	db := newBackupTestDB(t)

	release := make(chan struct{})
	entered := make(chan struct{})
	rcloneRunner := &fakeRunner{
		onRun: func(_ string, _ []string, _ chan<- StreamLine) {
			close(entered)
			<-release
		},
	}
	svc := buildSvc(t, db, &fakeDocker{}, &fakeRunner{}, rcloneRunner)
	svc.cfg.RcloneRemote = "myremote" // resolveBackupConfig falls back to cfg when DB has none

	reg := NewBackupRunnerRegistry(db, svc, slog.Default())

	_, err := reg.LaunchSync()
	require.NoError(t, err)

	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("sync never reached the blocking runner call")
	}

	completed := reg.StopWithTimeout(50 * time.Millisecond)
	assert.False(t, completed, "expected StopWithTimeout to expire while the run is still blocked")

	// Release the blocked runner and confirm the goroutine was left running
	// (not aborted) and genuinely finishes afterwards — StopWithTimeout only
	// stops waiting, it does not cancel anything.
	close(release)
	reg.Stop() // now unbounded: the run is unblocked, so this returns promptly
}
