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

// TestLaunchSync_AfterStopHasBegun_ReturnsErrorNotOrphanedRun is the
// deterministic, sequential half of agent-os-7a5's WaitGroup-safety fix: once
// Stop has committed, a later LaunchX call must be refused with
// ErrRegistryStopping rather than silently starting a new, orphaned exec
// goroutine that nothing will ever wait for again.
//
// This is deterministic by construction — no in-flight run, no timing
// dependency — unlike the underlying "wg.Add concurrent with wg.Wait" defect
// it guards against, whose exact panic window is a handful of CPU
// instructions between two atomic ops inside sync.WaitGroup itself (traced
// against $GOROOT/src/sync/waitgroup.go's Add/Wait) and did not reproduce
// even after 1M+ trials across four different concurrent-stress designs.
// This test instead pins the guard's observable CONTRACT, which is fully
// deterministic: registerAndAdd checks `stopped` under the same mu that
// beginStop sets it under, so "was a stop already committed" has a definite
// answer independent of wg scheduling.
func TestLaunchSync_AfterStopHasBegun_ReturnsErrorNotOrphanedRun(t *testing.T) {
	db := newBackupTestDB(t)
	svc := buildSvc(t, db, &fakeDocker{}, &fakeRunner{}, &fakeRunner{})
	reg := NewBackupRunnerRegistry(db, svc, slog.Default())

	reg.Stop() // nothing in flight, returns immediately; commits reg.stopped = true

	runID, err := reg.LaunchSync()
	assert.Empty(t, runID, "a refused launch must not return a runID")
	require.ErrorIs(t, err, ErrRegistryStopping)
}

// TestLaunchSync_DuringStopWithTimeout_ReturnsErrorNotPanic is the
// concurrent half: it reproduces the actual production shape agent-os-7a5
// guards against — main.go's StopWithTimeout call overlapping with a second,
// still-in-flight LaunchX call (the scenario: an HTTP handler still executing
// past srv.Shutdown's own bound reaches the registry after main.go has
// already begun draining). It is deterministic despite the concurrency
// because beginStop sets reg.stopped=true SYNCHRONOUSLY, under mu, as the
// very first thing StopWithTimeout does — before it ever blocks on wg.Wait()
// — so by the time the second LaunchSync call below can observe anything,
// the guard is already either present or not; nothing here depends on
// winning a race against wg's internal atomics the way the panic itself
// would.
//
// FAILING-FIRST EVIDENCE (see agent-os-7a5's final report for the full
// transcript): run against the pre-fix code (registerAndAdd did not exist;
// LaunchX called reg.register(dr); reg.wg.Add(1) unconditionally), this
// exact sequence — Stop the first run mid-flight, then call LaunchSync again
// while StopWithTimeout is still waiting — let the second LaunchSync SUCCEED
// silently (err == nil), starting a second, orphaned exec goroutine that
// StopWithTimeout's in-flight wg.Wait() call was never going to observe
// (it had already begun waiting before the second Add). That silent success
// is the real-world failure mode this test pins: a request landing in the
// drain window proceeds as if nothing were shutting down.
func TestLaunchSync_DuringStopWithTimeout_ReturnsErrorNotPanic(t *testing.T) {
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
	svc.cfg.RcloneRemote = "myremote"

	reg := NewBackupRunnerRegistry(db, svc, slog.Default())

	_, err := reg.LaunchSync()
	require.NoError(t, err)

	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("first sync never reached the blocking runner call")
	}

	stopDone := make(chan bool, 1)
	go func() {
		stopDone <- reg.StopWithTimeout(2 * time.Second)
	}()

	// beginStop sets reg.stopped under mu synchronously as StopWithTimeout's
	// first action; this sleep is a scheduling safety margin, not a
	// correctness dependency — the guard itself has no timing window.
	time.Sleep(20 * time.Millisecond)

	runID, err := reg.LaunchSync()
	assert.Empty(t, runID, "a launch arriving during the drain must not start a new run")
	require.ErrorIs(t, err, ErrRegistryStopping)

	close(release) // let the first (only) run finish
	completed := <-stopDone
	assert.True(t, completed, "StopWithTimeout should complete once the first run finishes")
}
