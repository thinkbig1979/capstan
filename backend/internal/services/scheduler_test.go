package services

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

// fakeUpdateChecker implements updateChecker for tests.
type fakeUpdateChecker struct {
	// checkFn is called by CheckForUpdates; if nil, returns empty results immediately.
	checkFn func(ctx context.Context) ([]models.ContainerUpdateInfo, error)
}

func (f *fakeUpdateChecker) CheckForUpdates(ctx context.Context, db DashboardDB) ([]models.ContainerUpdateInfo, error) {
	if f.checkFn != nil {
		return f.checkFn(ctx)
	}
	return nil, nil
}

func (f *fakeUpdateChecker) UpdateContainer(ctx context.Context, containerID string, db DashboardDB) (models.UpdateResult, truth.ActionResult) {
	return models.UpdateResult{}, truth.NoChange("stub: no update")
}

// newTestScheduler creates a SchedulerService wired to an in-memory DB and the
// provided fake checker. broadcastFn may be nil.
func newTestScheduler(t *testing.T, checker updateChecker) *SchedulerService {
	t.Helper()
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	return NewSchedulerService(checker, db, nil, nil)
}

// blockedChecker returns a fakeUpdateChecker whose CheckForUpdates blocks until
// the returned channel is closed (or the context is cancelled).
func blockedChecker() (*fakeUpdateChecker, chan struct{}) {
	release := make(chan struct{})
	checker := &fakeUpdateChecker{
		checkFn: func(ctx context.Context) ([]models.ContainerUpdateInfo, error) {
			select {
			case <-release:
				return nil, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},
	}
	return checker, release
}

// contextIgnoringChecker blocks forever and does NOT respect context cancellation.
// Used only for the timeout/stuck-scan test.
func contextIgnoringChecker(release chan struct{}) *fakeUpdateChecker {
	return &fakeUpdateChecker{
		checkFn: func(ctx context.Context) ([]models.ContainerUpdateInfo, error) {
			<-release // blocks until test signals
			return nil, nil
		},
	}
}

// TestStartBackgroundScan_ConcurrentCallReturnsError verifies that a second
// StartBackgroundScan call while one is already in-flight returns an error
// containing "already in progress", and that IsScanning transitions correctly.
func TestStartBackgroundScan_ConcurrentCallReturnsError(t *testing.T) {
	checker, release := blockedChecker()
	svc := newTestScheduler(t, checker)

	// First call should succeed and leave scan running.
	err := svc.StartBackgroundScan()
	require.NoError(t, err)
	assert.True(t, svc.IsScanning(), "expected IsScanning to be true while scan is in-flight")

	// Second concurrent call must be rejected.
	err = svc.StartBackgroundScan()
	require.Error(t, err)
	require.ErrorIs(t, err, ErrScanInProgress)

	// Signal the first scan to complete.
	close(release)

	// Wait for scanning flag to clear (up to 2 s).
	assert.Eventually(t, func() bool {
		return !svc.IsScanning()
	}, 2*time.Second, 10*time.Millisecond, "expected IsScanning to become false after scan finishes")
}

// TestRunScan_BlockedWhileBackgroundScanInFlight verifies that the shared gate
// prevents RunScan from executing while StartBackgroundScan is mid-flight.
func TestRunScan_BlockedWhileBackgroundScanInFlight(t *testing.T) {
	checker, release := blockedChecker()
	svc := newTestScheduler(t, checker)

	// Start a background scan that will block.
	err := svc.StartBackgroundScan()
	require.NoError(t, err)
	assert.True(t, svc.IsScanning())

	// RunScan must immediately return "already in progress".
	_, err = svc.RunScan(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already in progress",
		"RunScan should be blocked by the shared gate while background scan is running")

	// Unblock the background scan.
	close(release)

	// Wait for scanning flag to clear.
	assert.Eventually(t, func() bool {
		return !svc.IsScanning()
	}, 2*time.Second, 10*time.Millisecond)
}

// TestStop_WaitsForInFlightBackgroundScan verifies that Stop() blocks until the
// in-flight background scan completes (via context cancellation) and that the
// whole operation finishes within the 10-second bound.
func TestStop_WaitsForInFlightBackgroundScan(t *testing.T) {
	checker, release := blockedChecker()
	svc := newTestScheduler(t, checker)

	err := svc.StartBackgroundScan()
	require.NoError(t, err)
	assert.True(t, svc.IsScanning())

	// Allow the blocked goroutine to finish once Stop cancels its context.
	// The checkerFn will unblock on ctx.Done(), so we don't need to close
	// release manually here — Stop() will cancel parentCtx.
	_ = release // release is never closed in this test; context cancellation unblocks

	start := time.Now()
	svc.Stop()
	elapsed := time.Since(start)

	assert.Less(t, elapsed, 10*time.Second, "Stop should return within the 10-second shutdown bound")
	assert.False(t, svc.IsScanning(), "IsScanning must be false after Stop returns")
}

// TestStop_TimesOutOnStuckScan verifies that Stop() returns within ~10 seconds
// even when the background scan ignores context cancellation (stuck goroutine).
//
// NOTE: The goroutine intentionally leaks in this test — that is expected
// behaviour mirroring production: Stop logs a warning and proceeds with
// shutdown rather than blocking indefinitely.  We signal the release channel
// at the end to prevent go test from hanging after the test completes.
func TestStop_TimesOutOnStuckScan(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() {
		// Unblock the leaked goroutine so the process can exit cleanly.
		select {
		case <-release:
		default:
			close(release)
		}
	})

	checker := contextIgnoringChecker(release)
	svc := newTestScheduler(t, checker)

	err := svc.StartBackgroundScan()
	require.NoError(t, err)
	assert.True(t, svc.IsScanning())

	// Give the goroutine time to enter the blocking CheckForUpdates call.
	time.Sleep(20 * time.Millisecond)

	start := time.Now()
	svc.Stop()
	elapsed := time.Since(start)

	// Should time out within ~10 s (we allow a generous 12 s to absorb CI variance).
	assert.Less(t, elapsed, 12*time.Second,
		"Stop must not block forever — it should time out after 10 s and log a warning")

	// After the timeout the goroutine is still stuck, but Stop returned — that is
	// correct.  IsScanning may still be true in this scenario (the goroutine has
	// not exited yet), which is also acceptable.
}

// TestRunScan_SucceedsWhenNoScanInFlight is a basic sanity check that RunScan
// succeeds (and sets IsScanning back to false) when the checker returns quickly.
func TestRunScan_SucceedsWhenNoScanInFlight(t *testing.T) {
	checker := &fakeUpdateChecker{}
	svc := newTestScheduler(t, checker)

	_, err := svc.RunScan(context.Background())
	assert.NoError(t, err)
	// performScan returns a nil slice when the checker finds no containers — that is valid.
	assert.False(t, svc.IsScanning(), "IsScanning must be false after RunScan returns")
}

// TestStartBackgroundScan_SetsIsScanning verifies that IsScanning is true
// immediately after StartBackgroundScan returns, and returns to false after
// the goroutine finishes.
func TestStartBackgroundScan_SetsIsScanning(t *testing.T) {
	checker, release := blockedChecker()
	svc := newTestScheduler(t, checker)

	err := svc.StartBackgroundScan()
	require.NoError(t, err)
	assert.True(t, svc.IsScanning())

	close(release)

	assert.Eventually(t, func() bool {
		return !svc.IsScanning()
	}, 2*time.Second, 10*time.Millisecond)
}

// TestRunScan_ErrorPropagated verifies that when CheckForUpdates returns an
// error, RunScan propagates it and clears IsScanning.
func TestRunScan_ErrorPropagated(t *testing.T) {
	expectedErr := errors.New("registry unavailable")
	checker := &fakeUpdateChecker{
		checkFn: func(ctx context.Context) ([]models.ContainerUpdateInfo, error) {
			return nil, expectedErr
		},
	}
	svc := newTestScheduler(t, checker)

	_, err := svc.RunScan(context.Background())
	assert.ErrorIs(t, err, expectedErr)
	assert.False(t, svc.IsScanning(), "IsScanning must be false after RunScan returns an error")
}

func TestStartStopStart_Cycle(t *testing.T) {
	checker := &fakeUpdateChecker{}
	svc := newTestScheduler(t, checker)

	// First Start
	svc.Start(100 * time.Millisecond)
	assert.True(t, svc.IsRunning(), "IsRunning should be true after first Start")

	// Stop
	svc.Stop()
	assert.False(t, svc.IsRunning(), "IsRunning should be false after Stop")

	// Second Start — verifies the cancel context is re-initializable; must not panic
	svc.Start(100 * time.Millisecond)
	assert.True(t, svc.IsRunning(), "IsRunning should be true after second Start")

	// Clean up
	svc.Stop()
	assert.False(t, svc.IsRunning(), "IsRunning should be false after second Stop")
}

// TestStartBackgroundScan_ConcurrentWithStop_NoRace is the regression test for
// agent-os-mtbo.9: SchedulerService used to call s.wg.Add(1) outside s.mu while
// Stop() released s.mu and then called s.wg.Wait(), with no happens-before edge
// between the two. sync.WaitGroup deliberately instruments Add's first
// increment and Wait's first waiter as a modelled read/write on the same
// location precisely to catch "Add concurrent with Wait", so this shape trips
// the race detector.
//
// Run it with -race; without -race it cannot fail for the reason it exists.
// Against the unfixed code it reported:
//
//	WARNING: DATA RACE
//	  .../internal/services/scheduler.go:241 +0x2f4   (s.wg.Add(1) in StartBackgroundScan)
//	  .../internal/services/scheduler.go:124 +0x64    (s.wg.Wait() in Stop)
//
// The fix is the `stopped` field: see its doc comment on SchedulerService.
func TestStartBackgroundScan_ConcurrentWithStop_NoRace(t *testing.T) {
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	const iterations = 60

	var admitted, rejected int
	for i := 0; i < iterations; i++ {
		svc := NewSchedulerService(&fakeUpdateChecker{}, db, nil, nil)
		svc.Start(10 * time.Millisecond)

		var startErr error
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			startErr = svc.StartBackgroundScan()
		}()
		go func() {
			defer wg.Done()
			svc.Stop()
		}()
		wg.Wait()

		if startErr == nil {
			admitted++
		} else {
			rejected++
		}

		// Order-independent invariant: whichever goroutine won the mutex, once
		// Stop() has returned there is no scan still running. If the scan was
		// admitted, its wg.Add happened under the same mu that Stop takes
		// before its Wait, so Stop waited for it; if it was rejected, no scan
		// was ever started. A failure here means Stop returned while a scan it
		// did not know about was still in flight.
		assert.False(t, svc.IsScanning(),
			"iteration %d: IsScanning must be false once Stop has returned (startErr=%v)", i, startErr)
	}

	// Positive control. The race only exists on the interleaving where Stop
	// wins the mutex first, which post-fix is exactly the `rejected` bucket.
	// If a future change makes this test stop exercising that interleaving it
	// would pass identically on racy and non-racy code, guarding nothing — so
	// record the split, and fail loudly if the scan side never ran at all.
	t.Logf("Start/Stop interleavings over %d iterations: admitted=%d rejected=%d", iterations, admitted, rejected)
	require.Positive(t, admitted,
		"StartBackgroundScan was rejected on all %d iterations — Stop won the mutex every time, "+
			"so this test is no longer exercising the concurrent Add/Wait path it exists to guard",
		iterations)
}

// TestStartBackgroundScan_RejectedWhileStopped is the deterministic companion
// to the race test above: it pins the `stopped` guard's observable semantics
// without depending on any interleaving. Stop() latches stopped, so a later
// StartBackgroundScan refuses rather than calling s.wg.Add behind Stop's back;
// Start() clears the latch again.
func TestStartBackgroundScan_RejectedWhileStopped(t *testing.T) {
	svc := newTestScheduler(t, &fakeUpdateChecker{})

	svc.Start(time.Hour)
	require.NoError(t, svc.StartBackgroundScan(), "a freshly started scheduler must admit scans")
	assert.Eventually(t, func() bool { return !svc.IsScanning() }, 2*time.Second, 10*time.Millisecond)

	svc.Stop()

	err := svc.StartBackgroundScan()
	require.Error(t, err, "a stopped scheduler must refuse to start a background scan")
	require.ErrorIs(t, err, ErrSchedulerStopping,
		"the refusal must be the exported sentinel: handlers.checkUpdates matches on it "+
			"with errors.Is to keep a refresh during the stop window off the 500 path")
	assert.False(t, svc.IsScanning(), "a refused scan must not leave the scanning flag set")

	// Start() must clear the latch, otherwise Restart() would permanently
	// disable background scans.
	svc.Start(time.Hour)
	require.NoError(t, svc.StartBackgroundScan(), "Start must clear the stopped latch")
	svc.Stop()
}
