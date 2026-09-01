package services

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
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

	// updateFn, when set, decides what UpdateContainer reports for a given
	// container; nil keeps the original stub (no_change). updated records every
	// container UpdateContainer was asked to act on, in order.
	//
	// mu guards updateFn's bookkeeping only. The apply paths call
	// UpdateContainer from the scheduler's own goroutine while the test body
	// reads updatedIDs, so this is a real cross-goroutine access under -race.
	mu       sync.Mutex
	updateFn func(containerID string) (models.UpdateResult, truth.ActionResult)
	updated  []string

	// inspectFn decides what InspectContainer reports for a container; nil means
	// every container still exists. Defining the method unconditionally is what
	// makes this fake satisfy services.containerInspector, so the scheduled
	// apply path's freshness check is exercised rather than skipped.
	inspectFn func(containerID string) error
}

func (f *fakeUpdateChecker) InspectContainer(_ context.Context, containerID string) (container.InspectResponse, error) {
	f.mu.Lock()
	fn := f.inspectFn
	f.mu.Unlock()

	if fn != nil {
		if err := fn(containerID); err != nil {
			return container.InspectResponse{}, err
		}
	}
	return container.InspectResponse{}, nil
}

// notFoundError is what the Docker client returns for a container that no
// longer exists: cerrdefs.IsNotFound (which client.IsErrNotFound aliases)
// matches any error exposing a NotFound() method.
type notFoundError struct{ msg string }

func (e notFoundError) Error() string { return e.msg }
func (e notFoundError) NotFound()     {}

// updatedIDs returns the containers UpdateContainer was called for, in order.
func (f *fakeUpdateChecker) updatedIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.updated...)
}

func (f *fakeUpdateChecker) CheckForUpdates(ctx context.Context, db DashboardDB) ([]models.ContainerUpdateInfo, error) {
	if f.checkFn != nil {
		return f.checkFn(ctx)
	}
	return nil, nil
}

func (f *fakeUpdateChecker) UpdateContainer(ctx context.Context, containerID string, db DashboardDB) (models.UpdateResult, truth.ActionResult) {
	f.mu.Lock()
	f.updated = append(f.updated, containerID)
	fn := f.updateFn
	f.mu.Unlock()

	if fn != nil {
		return fn(containerID)
	}
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

// ---------------------------------------------------------------------------
// Apply-path characterization tests (agent-os-mtbo.2).
//
// RunAutoUpdates and the apply half of runCycle had ZERO test coverage before
// this task: `grep -rn RunAutoUpdates --include=*_test.go internal/` matched
// nothing, so a change that broke immediate-mode application would have passed
// every existing test in this file. These four pin TODAY's behaviour and were
// run green against the unmodified scheduler.go before the scheduled-apply mode
// was written, so "immediate mode is unchanged" is a result rather than a claim.
// ---------------------------------------------------------------------------

// newApplyFixture builds a scheduler with auto-update globally enabled — the
// gate RunAutoUpdates checks first and returns on.
func newApplyFixture(t *testing.T, checker updateChecker) *SchedulerService {
	t.Helper()
	svc := newTestScheduler(t, checker)
	require.NoError(t, svc.db.SetSetting("auto_update_enabled", "true"))
	return svc
}

// seedContainerPolicy enables auto-update for one container target.
func seedContainerPolicy(t *testing.T, svc *SchedulerService, containerID string) {
	t.Helper()
	now := time.Now().Format(time.RFC3339)
	require.NoError(t, svc.db.UpsertAutoUpdatePolicy(&models.AutoUpdatePolicy{
		ID:         "policy-" + containerID,
		TargetType: "container",
		TargetID:   containerID,
		Enabled:    true,
		CreatedAt:  now,
		UpdatedAt:  now,
	}))
}

// scanFinding makes CheckForUpdates report exactly one pending update.
func scanFinding(containerID, name string) func(context.Context) ([]models.ContainerUpdateInfo, error) {
	return func(context.Context) ([]models.ContainerUpdateInfo, error) {
		return []models.ContainerUpdateInfo{{
			ContainerID:   containerID,
			ContainerName: name,
			Image:         "nginx:latest",
			ImageRef:      "nginx:latest",
			State:         "running",
		}}, nil
	}
}

func succeedingUpdate(string) (models.UpdateResult, truth.ActionResult) {
	return models.UpdateResult{}, truth.Success("image advanced")
}

func failingUpdate(string) (models.UpdateResult, truth.ActionResult) {
	return models.UpdateResult{}, truth.Failed("could not inspect container before update", errors.New("no such container"))
}

// TestRunCycle_ImmediateMode_AppliesOnScanTick is the characterization test for
// the behaviour the scheduled-apply work must preserve: with the default
// update_apply_mode ("immediate", seeded by migration 14) a scheduler cycle
// scans and then applies on the SAME tick.
//
// It is also the mandatory counterpart to the scheduled-mode test below: "does
// not apply on a scan tick" proves nothing on its own, since a build where
// RunAutoUpdates was never reached at all would satisfy it. Both run on this
// same fixture, so only the mode differs between them.
func TestRunCycle_ImmediateMode_AppliesOnScanTick(t *testing.T) {
	checker := &fakeUpdateChecker{
		checkFn:  scanFinding("c1", "web"),
		updateFn: succeedingUpdate,
	}
	svc := newApplyFixture(t, checker)
	seedContainerPolicy(t, svc, "c1")

	mode, err := svc.db.GetSetting("update_apply_mode")
	require.NoError(t, err)
	require.Equal(t, "immediate", mode, "migration 14 must seed immediate as the default apply mode")

	svc.runCycle(context.Background())

	assert.Equal(t, []string{"c1"}, checker.updatedIDs(),
		"immediate mode must apply the scan's own results on the scan tick")
}

// TestRunAutoUpdates_FailureIncrementsConsecutiveFailures pins the failure
// bookkeeping that the stale-cache defect abuses. It is the CONTROL for the
// eviction test: an eviction test showing ConsecutiveFailures unchanged is
// worthless unless a genuine failure on the same instrument does increment it.
func TestRunAutoUpdates_FailureIncrementsConsecutiveFailures(t *testing.T) {
	checker := &fakeUpdateChecker{updateFn: failingUpdate}
	svc := newApplyFixture(t, checker)
	seedContainerPolicy(t, svc, "c1")

	svc.RunAutoUpdates(context.Background(), []models.CachedUpdate{{
		ContainerID:   "c1",
		ContainerName: "web",
		ImageRef:      "nginx:latest",
	}})

	policy, err := svc.db.GetAutoUpdatePolicy("container", "c1")
	require.NoError(t, err)
	assert.Equal(t, 1, policy.ConsecutiveFailures, "a failed apply must increment the failure counter")
	assert.False(t, policy.Paused, "one failure is not yet a pause")
}

// TestRunAutoUpdates_ThreeFailuresPause pins the pause threshold — the state
// the stale-cache defect drives targets into after three nights.
func TestRunAutoUpdates_ThreeFailuresPause(t *testing.T) {
	checker := &fakeUpdateChecker{updateFn: failingUpdate}
	svc := newApplyFixture(t, checker)
	seedContainerPolicy(t, svc, "c1")

	update := models.CachedUpdate{ContainerID: "c1", ContainerName: "web", ImageRef: "nginx:latest"}
	for i := 0; i < 3; i++ {
		svc.RunAutoUpdates(context.Background(), []models.CachedUpdate{update})
	}

	policy, err := svc.db.GetAutoUpdatePolicy("container", "c1")
	require.NoError(t, err)
	assert.Equal(t, 3, policy.ConsecutiveFailures)
	assert.True(t, policy.Paused, "three consecutive failures must pause the policy")
}

// TestRunAutoUpdates_SkipsTargetsWithoutPolicy pins the other half of the apply
// gate: an update with no enabled policy is skipped, never applied.
func TestRunAutoUpdates_SkipsTargetsWithoutPolicy(t *testing.T) {
	checker := &fakeUpdateChecker{updateFn: succeedingUpdate}
	svc := newApplyFixture(t, checker)
	// deliberately no policy seeded

	svc.RunAutoUpdates(context.Background(), []models.CachedUpdate{{
		ContainerID:   "c1",
		ContainerName: "web",
		ImageRef:      "nginx:latest",
	}})

	assert.Empty(t, checker.updatedIDs(), "a container with no policy must not be updated")
}

// ---------------------------------------------------------------------------
// Scheduled apply mode (agent-os-mtbo.2).
// ---------------------------------------------------------------------------

// testClock is a hand-advanced wall clock for the apply loop. The loop hops on
// real time (shrunk to milliseconds via applyMaxSleep) but decides on this
// clock, so a test can put the scheduled instant in the past deliberately and
// watch the timer fire for exactly the right reason.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func newTestClock(now time.Time) *testClock { return &testClock{now: now} }

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

// enableScheduledApply stores a schedule of hhmm on every weekday.
func enableScheduledApply(t *testing.T, svc *SchedulerService, hhmm string) {
	t.Helper()
	require.NoError(t, svc.db.SetSetting("update_apply_mode", "scheduled"))
	require.NoError(t, svc.db.SetSetting("update_apply_time", hhmm))
	require.NoError(t, svc.db.SetSetting("update_apply_days", "0,1,2,3,4,5,6"))
}

// seedCachedUpdate writes one row into cached_updates, the table the scheduled
// apply path reads from (the immediate path uses the scan's return value).
func seedCachedUpdate(t *testing.T, svc *SchedulerService, containerID, name string) {
	t.Helper()
	existing, err := svc.db.GetCachedUpdates()
	require.NoError(t, err)
	existing = append(existing, models.CachedUpdate{
		ID:            "cached-" + containerID,
		ContainerID:   containerID,
		ContainerName: name,
		Image:         "nginx:latest",
		ImageRef:      "nginx:latest",
		State:         "running",
		ScannedAt:     time.Now().Add(-72 * time.Hour).Format(time.RFC3339),
	})
	require.NoError(t, svc.db.SetCachedUpdates(existing))
}

// TestRunCycle_ScheduledMode_DoesNotApplyOnScanTick is the paired opposite of
// TestRunCycle_ImmediateMode_AppliesOnScanTick. On its own it would be
// satisfied by a build where RunAutoUpdates was never reachable at all; read
// with its immediate-mode twin, which runs the same fixture and differs only in
// update_apply_mode, it shows the mode is what decides.
func TestRunCycle_ScheduledMode_DoesNotApplyOnScanTick(t *testing.T) {
	checker := &fakeUpdateChecker{
		checkFn:  scanFinding("c1", "web"),
		updateFn: succeedingUpdate,
	}
	svc := newApplyFixture(t, checker)
	seedContainerPolicy(t, svc, "c1")
	enableScheduledApply(t, svc, "03:00")

	svc.runCycle(context.Background())

	assert.Empty(t, checker.updatedIDs(),
		"scheduled mode must scan without applying on the scan tick")

	// The scan itself must still have happened and still have cached its result:
	// scheduled mode moves the apply, it does not disable the scan.
	cached, err := svc.db.GetCachedUpdates()
	require.NoError(t, err)
	require.Len(t, cached, 1, "the scan must still run and still cache what it found")
	assert.Equal(t, "c1", cached[0].ContainerID)
}

// TestRunCycle_InvalidStoredSchedule_FallsBackToImmediate covers requirement C:
// an unparseable stored schedule must never mean "no schedule". If it did, the
// apply timer would never fire and nothing would tell the operator their
// updates had silently stopped landing. Immediate is the safe fallback.
func TestRunCycle_InvalidStoredSchedule_FallsBackToImmediate(t *testing.T) {
	checker := &fakeUpdateChecker{
		checkFn:  scanFinding("c1", "web"),
		updateFn: succeedingUpdate,
	}
	svc := newApplyFixture(t, checker)
	seedContainerPolicy(t, svc, "c1")
	enableScheduledApply(t, svc, "03:00")
	require.NoError(t, svc.db.SetSetting("update_apply_time", "25:99"))

	require.False(t, svc.loadApplySchedule().scheduled,
		"an unparseable stored time must resolve to immediate, not to a dead schedule")

	svc.runCycle(context.Background())

	assert.Equal(t, []string{"c1"}, checker.updatedIDs(),
		"a misconfigured schedule must still apply immediately rather than silently never applying")
}

// TestApplyTimer_FiresAndAppliesCachedUpdates drives the real apply loop: it is
// armed by Start(), hops on its own timer, and fires only once the wall clock
// has crossed the scheduled instant. The scan interval is an hour so the scan
// ticker never contributes; everything applied here came from cached_updates.
func TestApplyTimer_FiresAndAppliesCachedUpdates(t *testing.T) {
	checker := &fakeUpdateChecker{updateFn: succeedingUpdate}
	svc := newApplyFixture(t, checker)
	seedContainerPolicy(t, svc, "c1")
	seedCachedUpdate(t, svc, "c1", "web")
	enableScheduledApply(t, svc, "03:00")

	// Just before the scheduled instant, so NextAfter resolves to today 03:00.
	base := time.Date(2026, 3, 2, 2, 59, 0, 0, time.Local)
	clock := newTestClock(base)
	svc.applyClock = clock.Now
	svc.applyMaxSleep = 5 * time.Millisecond

	svc.Start(time.Hour)
	defer svc.Stop()

	// The timer is armed and hopping, but the scheduled instant has not arrived.
	want := time.Date(2026, 3, 2, 3, 0, 0, 0, time.Local)
	require.Eventually(t, func() bool {
		return svc.NextApplyAt().Equal(want)
	}, 2*time.Second, 5*time.Millisecond, "Start must arm the apply timer for today 03:00")
	time.Sleep(50 * time.Millisecond)
	require.Empty(t, checker.updatedIDs(),
		"the apply timer must not fire before the scheduled instant")

	clock.set(base.Add(2 * time.Minute)) // 03:01 — past 03:00

	assert.Eventually(t, func() bool {
		return len(checker.updatedIDs()) == 1
	}, 2*time.Second, 5*time.Millisecond,
		"the apply timer must fire once the wall clock passes the scheduled instant")
	assert.Equal(t, []string{"c1"}, checker.updatedIDs())
}

// TestApplyTimer_StoppedSchedulerDoesNotFire is the companion: Stop() must halt
// and drain the apply timer, so crossing the scheduled instant after shutdown
// applies nothing.
func TestApplyTimer_StoppedSchedulerDoesNotFire(t *testing.T) {
	checker := &fakeUpdateChecker{updateFn: succeedingUpdate}
	svc := newApplyFixture(t, checker)
	seedContainerPolicy(t, svc, "c1")
	seedCachedUpdate(t, svc, "c1", "web")
	enableScheduledApply(t, svc, "03:00")

	base := time.Date(2026, 3, 2, 2, 59, 0, 0, time.Local)
	clock := newTestClock(base)
	svc.applyClock = clock.Now
	svc.applyMaxSleep = 5 * time.Millisecond

	svc.Start(time.Hour)
	svc.Stop()

	clock.set(base.Add(2 * time.Minute))
	time.Sleep(100 * time.Millisecond)

	assert.Empty(t, checker.updatedIDs(),
		"a stopped scheduler's apply timer must not fire")
}

// TestScheduledApply_VanishedContainerIsEvictedNotFailed is the headline
// regression for this task, and it is deliberately TWO-SIDED in a single run.
//
// c1's container no longer exists — a stack redeploy between the scan and the
// apply is enough. Left alone it would fail at truth.ResolveContainerImage,
// increment ConsecutiveFailures, and after three nights pause auto-update
// permanently. It must be evicted instead: no update attempted, counter
// untouched, cache row gone.
//
// c2 is present and its update genuinely fails. It is the control on the same
// instrument in the same invocation: without it, "ConsecutiveFailures stayed 0"
// would also be true of a build that had stopped counting failures at all.
func TestScheduledApply_VanishedContainerIsEvictedNotFailed(t *testing.T) {
	checker := &fakeUpdateChecker{
		updateFn: failingUpdate,
		inspectFn: func(containerID string) error {
			if containerID == "c1" {
				return notFoundError{msg: "Error: No such container: c1"}
			}
			return nil
		},
	}
	svc := newApplyFixture(t, checker)
	seedContainerPolicy(t, svc, "c1")
	seedContainerPolicy(t, svc, "c2")
	seedCachedUpdate(t, svc, "c1", "vanished")
	seedCachedUpdate(t, svc, "c2", "still-here")
	enableScheduledApply(t, svc, "03:00")

	require.True(t, svc.applyNow(context.Background()), "the apply must run, not defer")

	// Attack side: the vanished container was never even attempted.
	assert.Equal(t, []string{"c2"}, checker.updatedIDs(),
		"a container that no longer exists must not reach UpdateContainer")

	vanished, err := svc.db.GetAutoUpdatePolicy("container", "c1")
	require.NoError(t, err)
	assert.Equal(t, 0, vanished.ConsecutiveFailures,
		"an evicted container must not count as an update failure")
	assert.False(t, vanished.Paused, "an evicted container must never drive a policy to paused")

	// Control side, same instrument, same run: a real failure still counts.
	present, err := svc.db.GetAutoUpdatePolicy("container", "c2")
	require.NoError(t, err)
	assert.Equal(t, 1, present.ConsecutiveFailures,
		"a genuine update failure must still increment the failure counter")

	// The stale row is gone, so the next night starts from a clean cache.
	cached, err := svc.db.GetCachedUpdates()
	require.NoError(t, err)
	require.Len(t, cached, 1)
	assert.Equal(t, "c2", cached[0].ContainerID, "the vanished container's cached row must be evicted")
}

// TestScheduledApply_UninspectableContainerIsSkippedNotEvicted pins the third
// case: an inspect that fails for a reason OTHER than not-found (daemon down,
// timeout) is neither applied nor evicted. Evicting on a daemon outage would
// empty the whole cache in one pass.
func TestScheduledApply_UninspectableContainerIsSkippedNotEvicted(t *testing.T) {
	checker := &fakeUpdateChecker{
		updateFn: failingUpdate,
		inspectFn: func(string) error {
			return errors.New("Cannot connect to the Docker daemon")
		},
	}
	svc := newApplyFixture(t, checker)
	seedContainerPolicy(t, svc, "c1")
	seedCachedUpdate(t, svc, "c1", "web")
	enableScheduledApply(t, svc, "03:00")

	require.True(t, svc.applyNow(context.Background()))

	assert.Empty(t, checker.updatedIDs(), "an unresolvable container must not be applied to")

	policy, err := svc.db.GetAutoUpdatePolicy("container", "c1")
	require.NoError(t, err)
	assert.Equal(t, 0, policy.ConsecutiveFailures, "a daemon outage must not count as an update failure")

	cached, err := svc.db.GetCachedUpdates()
	require.NoError(t, err)
	assert.Len(t, cached, 1, "a daemon outage must not evict the cache")
}

// TestScheduledApply_DeferredWhileScanning covers requirement B: RunAutoUpdates
// never reads s.scanning itself, so a scheduled apply landing mid-scan would
// otherwise run concurrently with SetCachedUpdates' DELETE-then-INSERT and with
// the scan's own policy writes. applyNow takes the same single-flight guard and
// reports the deferral so the loop retries instead of losing the run.
func TestScheduledApply_DeferredWhileScanning(t *testing.T) {
	checker, release := blockedChecker()
	checker.updateFn = succeedingUpdate
	svc := newApplyFixture(t, checker)
	seedContainerPolicy(t, svc, "c1")
	seedCachedUpdate(t, svc, "c1", "web")
	enableScheduledApply(t, svc, "03:00")

	require.NoError(t, svc.StartBackgroundScan())
	require.True(t, svc.IsScanning())

	assert.False(t, svc.applyNow(context.Background()),
		"an apply must defer, not run, while a scan holds the single-flight guard")
	assert.Empty(t, checker.updatedIDs())

	// Control: once the scan releases the guard, the same call applies.
	close(release)
	require.Eventually(t, func() bool { return !svc.IsScanning() }, 2*time.Second, 10*time.Millisecond)

	// That scan found nothing, and SetCachedUpdates is a DELETE-then-INSERT, so
	// it has just emptied the cache — which is precisely the write the guard
	// exists to keep an apply from interleaving with. Re-seed for the control.
	seedCachedUpdate(t, svc, "c1", "web")

	assert.True(t, svc.applyNow(context.Background()))
	assert.Equal(t, []string{"c1"}, checker.updatedIDs())
}

// TestReloadApplySchedule_PicksUpASettingsChange pins the handler's re-arm path:
// a scheduler started in immediate mode must start applying on the clock after
// the settings change plus a ReloadApplySchedule, without a restart.
func TestReloadApplySchedule_PicksUpASettingsChange(t *testing.T) {
	checker := &fakeUpdateChecker{updateFn: succeedingUpdate}
	svc := newApplyFixture(t, checker)
	seedContainerPolicy(t, svc, "c1")
	seedCachedUpdate(t, svc, "c1", "web")

	base := time.Date(2026, 3, 2, 2, 59, 0, 0, time.Local)
	clock := newTestClock(base)
	svc.applyClock = clock.Now
	svc.applyMaxSleep = 5 * time.Millisecond

	// Started in immediate mode: there is no schedule to fire.
	svc.Start(time.Hour)
	defer svc.Stop()

	clock.set(base.Add(2 * time.Minute))
	time.Sleep(50 * time.Millisecond)
	require.Empty(t, checker.updatedIDs(), "immediate mode has no apply timer to fire")

	// Now configure a schedule and re-arm the way the handler does.
	clock.set(base)
	enableScheduledApply(t, svc, "03:00")
	svc.ReloadApplySchedule()

	// Wait for the loop to have actually picked the re-arm up before moving the
	// clock. Advancing first would race it: a loop that read the schedule at
	// 03:01 would resolve the next 03:00 to TOMORROW and correctly never fire,
	// which would look like the re-arm failing.
	want := time.Date(2026, 3, 2, 3, 0, 0, 0, time.Local)
	require.Eventually(t, func() bool {
		return svc.NextApplyAt().Equal(want)
	}, 2*time.Second, 5*time.Millisecond, "ReloadApplySchedule must arm the loop for today 03:00")

	clock.set(base.Add(2 * time.Minute))
	assert.Eventually(t, func() bool {
		return len(checker.updatedIDs()) == 1
	}, 2*time.Second, 5*time.Millisecond,
		"ReloadApplySchedule must arm the timer without a scheduler restart")
}

// TestReloadApplySchedule_OnStoppedSchedulerIsNoop guards the shutdown window:
// the handler can call it at any time, including after Stop() cleared the
// re-arm handle.
func TestReloadApplySchedule_OnStoppedSchedulerIsNoop(t *testing.T) {
	svc := newTestScheduler(t, &fakeUpdateChecker{})

	assert.NotPanics(t, svc.ReloadApplySchedule, "a never-started scheduler must tolerate a re-arm")

	svc.Start(time.Hour)
	svc.Stop()
	assert.NotPanics(t, svc.ReloadApplySchedule, "a stopped scheduler must tolerate a re-arm")
}

// TestApplyWait_BoundsEveryArming pins the reason the loop uses bounded hops
// rather than one long sleep: time.Timer is monotonic and does not re-derive
// its deadline from the wall clock, so a seven-day arming would absorb an NTP
// step or a suspend/resume in full.
func TestApplyWait_BoundsEveryArming(t *testing.T) {
	now := time.Date(2026, 3, 2, 2, 59, 0, 0, time.Local)
	const maxSleep = 60 * time.Second

	assert.Equal(t, maxSleep, applyWait(now.Add(7*24*time.Hour), true, now, maxSleep),
		"a week-away schedule must still be armed for one bounded hop")
	assert.Equal(t, 30*time.Second, applyWait(now.Add(30*time.Second), true, now, maxSleep),
		"an instant inside the bound must be armed exactly")
	assert.Equal(t, maxSleep, applyWait(time.Time{}, false, now, maxSleep),
		"an unscheduled loop must keep hopping so a re-arm is not its only wake-up")
	assert.Equal(t, applyRetryDelay, applyWait(now.Add(-time.Hour), true, now, maxSleep),
		"a deferred fire must back off rather than spin on an instant already past")
}
