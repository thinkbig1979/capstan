package services

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// fakeVerifier is a VerifyLauncher that records each launch. done is the
// channel handed back; nil means "already finished" (a closed channel).
type fakeVerifier struct {
	mu       sync.Mutex
	triggers []string
	subsets  []string
	done     chan struct{}
	// order, when set, is appended "verify" at launch so a test can see the
	// launch relative to RunBackup.
	order *[]string
}

func (f *fakeVerifier) LaunchVerify(subset, trigger string) (string, <-chan struct{}, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.triggers = append(f.triggers, trigger)
	f.subsets = append(f.subsets, subset)
	if f.order != nil {
		*f.order = append(*f.order, "verify")
	}
	if f.done != nil {
		return "verify-run", f.done, nil
	}
	closed := make(chan struct{})
	close(closed)
	return "verify-run", closed, nil
}

func (f *fakeVerifier) launches() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.triggers)
}

// addVerifyRun records a finished verify row started at startedAt.
func addVerifyRun(t *testing.T, svc *BackupSchedulerService, id, status string, startedAt time.Time) {
	t.Helper()
	require.NoError(t, svc.db.CreateBackupRun(&models.BackupRun{
		ID:        id,
		Kind:      string(RunKindVerify),
		Trigger:   TriggerManual,
		Status:    status,
		StartedAt: startedAt.UTC().Format(time.RFC3339),
	}))
}

// TestBackupScheduler_WeeklyVerifyRunsAfterTheBackup pins agent-os-ffaj's
// trigger: a scheduled cycle with no check on record launches one, AFTER the
// backup, with the scheduled trigger and the default subset (the empty string,
// D68.2: depth is not exposed).
func TestBackupScheduler_WeeklyVerifyRunsAfterTheBackup(t *testing.T) {
	t.Parallel()

	var order []string
	runner := &fakeBackupRunner{
		runFn: func(context.Context, []string, bool, string, chan<- StreamLine) (*models.BackupRun, error) {
			order = append(order, "backup")
			return &models.BackupRun{ID: "b1", Status: "success"}, nil
		},
	}
	svc := newTestBackupScheduler(t, runner)
	v := &fakeVerifier{order: &order}
	svc.SetVerifier(v)

	svc.runCycle(context.Background())

	assert.Equal(t, []string{"backup", "verify"}, order)
	assert.Equal(t, []string{TriggerScheduled}, v.triggers)
	assert.Equal(t, []string{""}, v.subsets)
}

// TestBackupScheduler_WeeklyVerifyDueness covers when a check counts as "done
// this week": success and failed do, inside the 7 days; interrupted and
// skipped never do, so the next cycle retries (agent-os-ffaj). The boundary
// arms sit exactly on, one second inside and one second outside the window.
// synctest freezes time.Now, so the cycle's "now" is the test's.
func TestBackupScheduler_WeeklyVerifyDueness(t *testing.T) {
	tests := []struct {
		name     string
		status   string
		age      time.Duration
		wantRuns int
	}{
		{"success inside the week", "success", 6 * 24 * time.Hour, 0},
		{"failed inside the week", "failed", 24 * time.Hour, 0},
		{"exactly 7 days old still counts", "success", scheduledVerifyEvery, 0},
		{"one second inside the week", "success", scheduledVerifyEvery - time.Second, 0},
		{"one second past the week", "success", scheduledVerifyEvery + time.Second, 1},
		{"interrupted inside the week does not count", "interrupted", time.Hour, 1},
		{"skipped inside the week does not count", RunStatusSkipped, time.Hour, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				svc := newTestBackupScheduler(t, &fakeBackupRunner{})
				v := &fakeVerifier{}
				svc.SetVerifier(v)
				addVerifyRun(t, svc, "v-prev", tc.status, time.Now().Add(-tc.age))

				svc.runCycle(context.Background())

				assert.Equal(t, tc.wantRuns, v.launches())
			})
		})
	}
}

// TestBackupScheduler_WeeklyVerifyHonoursTheSetting: off means no check, and
// the default (no row) means on.
func TestBackupScheduler_WeeklyVerifyHonoursTheSetting(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		stored   string // "" = no row
		wantRuns int
	}{
		{"no row defaults to on", "", 1},
		{"stored true", "true", 1},
		{"stored false", "false", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := newTestBackupScheduler(t, &fakeBackupRunner{})
			if tc.stored != "" {
				require.NoError(t, svc.db.SetSetting(SettingBackupVerifyWeekly, tc.stored))
			}
			v := &fakeVerifier{}
			svc.SetVerifier(v)

			svc.runCycle(context.Background())

			assert.Equal(t, tc.wantRuns, v.launches())
		})
	}
}

// TestBackupScheduler_WeeklyVerifyNotRunWhenTheBackupDidNot: a cycle whose
// backup was refused (engine unavailable, lock held) launches no check.
func TestBackupScheduler_WeeklyVerifyNotRunWhenTheBackupDidNot(t *testing.T) {
	t.Parallel()

	for _, cause := range []error{ErrBackupUnavailable, ErrBackupBusy} {
		t.Run(cause.Error(), func(t *testing.T) {
			runner := &fakeBackupRunner{
				runFn: func(context.Context, []string, bool, string, chan<- StreamLine) (*models.BackupRun, error) {
					return nil, cause
				},
			}
			svc := newTestBackupScheduler(t, runner)
			v := &fakeVerifier{}
			svc.SetVerifier(v)

			svc.runCycle(context.Background())

			assert.Zero(t, v.launches())
		})
	}
}

// TestBackupScheduler_BackupDueDuringWeeklyVerifyIsSkippedWithARow pins what
// happens to a backup that falls due while the scheduled check still runs:
// it does NOT run (restic's exclusive lock would fail it with exit 11) and it
// is recorded as skipped, with the reason, so the history shows it. Once the
// check ends, the next tick backs up again.
func TestBackupScheduler_BackupDueDuringWeeklyVerifyIsSkippedWithARow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		runner := &fakeBackupRunner{}
		svc := newTestBackupScheduler(t, runner)
		v := &fakeVerifier{done: make(chan struct{})}
		svc.SetVerifier(v)

		const interval = 20 * time.Millisecond
		svc.Start(interval)

		// Tick 1 (20ms) backs up and starts the check, which blocks. Tick 2
		// (40ms) falls due during it.
		time.Sleep(interval*2 + interval/2)
		assert.Equal(t, int32(1), runner.callCount.Load(), "no backup may start while the check runs")
		assert.Equal(t, 1, v.launches())

		runs, err := svc.db.GetBackupRuns(10)
		require.NoError(t, err)
		require.Len(t, runs, 1, "the skipped tick leaves exactly one row")
		assert.Equal(t, RunStatusSkipped, runs[0].Status)
		assert.Equal(t, TriggerScheduled, runs[0].Trigger)
		assert.Equal(t, "scheduled backup skipped: the weekly repository check was still running", runs[0].ErrorMessage)

		close(v.done)
		time.Sleep(interval) // tick 3 at 60ms
		assert.Equal(t, int32(2), runner.callCount.Load(), "backups resume once the check has finished")

		svc.Stop()
	})
}

// TestBackupScheduler_StopDoesNotWaitOutARunningWeeklyVerify: the cycle waits
// on the check, but Stop must not sit out its 10s bound for it; the registry
// owns the run and drains it at shutdown.
func TestBackupScheduler_StopDoesNotWaitOutARunningWeeklyVerify(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		svc := newTestBackupScheduler(t, &fakeBackupRunner{})
		v := &fakeVerifier{done: make(chan struct{})} // never finishes
		svc.SetVerifier(v)

		const interval = 20 * time.Millisecond
		svc.Start(interval)
		time.Sleep(interval + interval/2)
		require.Equal(t, 1, v.launches(), "the check must be running before Stop")

		start := time.Now()
		svc.Stop()
		assert.Less(t, time.Since(start), time.Second, "Stop waited for the check instead of ending the cycle's wait")
	})
}

// --- the service and the registry ---

// ctxRecordingRunner records the deadline of the context restic was run under.
type ctxRecordingRunner struct {
	fakeRunner
	deadline    time.Time
	hasDeadline bool
	runs        atomic.Int32
}

func (r *ctxRecordingRunner) Run(ctx context.Context, name string, args []string, env []string, out chan<- StreamLine) error {
	r.runs.Add(1)
	r.deadline, r.hasDeadline = ctx.Deadline()
	return r.fakeRunner.Run(ctx, name, args, env, out)
}

func drainVerifyLines() (chan StreamLine, func()) {
	out := make(chan StreamLine, 64)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range out {
		}
	}()
	return out, func() { close(out); <-done }
}

// TestResticManager_VerifyRepositoryData_RunsUnderADeadline (safe-defaults
// rule 1): the check holds the backup lock, so it must not be able to hold it
// forever.
func TestResticManager_VerifyRepositoryData_RunsUnderADeadline(t *testing.T) {
	t.Parallel()

	runner := &ctxRecordingRunner{}
	m := newResticManagerWithRunner(testBackupConfig(), runner, nil)
	out, stop := drainVerifyLines()
	before := time.Now()
	require.NoError(t, m.VerifyRepositoryData(context.Background(), "", out))
	stop()

	require.True(t, runner.hasDeadline, "restic check ran with no deadline")
	assert.WithinDuration(t, before.Add(verifyTimeout), runner.deadline, time.Minute)
}

// TestBackupService_VerifyRepositoryData_RefusesWhileTheLockIsHeld: a check
// never runs alongside a backup, sync, restore, prune or another check, and
// it takes the lock while it runs (agent-os-ffaj).
func TestBackupService_VerifyRepositoryData_RefusesWhileTheLockIsHeld(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	runner := &ctxRecordingRunner{}
	svc := buildSvc(t, db, &fakeDocker{}, runner, &fakeRunner{})

	svc.busy.Store(1)
	out, stop := drainVerifyLines()
	err := svc.VerifyRepositoryData(context.Background(), "", out)
	assert.ErrorIs(t, err, ErrBackupBusy)
	assert.Zero(t, runner.runs.Load(), "restic must not run while another operation holds the lock")

	// Control: the same service runs the check once the lock is free, and
	// holds the lock while it does.
	svc.busy.Store(0)
	var heldDuringRun bool
	runner.onRun = func(string, []string, chan<- StreamLine) { heldDuringRun = svc.IsBusy() }
	require.NoError(t, svc.VerifyRepositoryData(context.Background(), "", out))
	stop()
	assert.Equal(t, int32(1), runner.runs.Load())
	assert.True(t, heldDuringRun, "the check must hold the backup lock while restic runs")
	assert.False(t, svc.IsBusy(), "the lock must be released when the check ends")
}

// TestBackupRunnerRegistry_LaunchVerify_RecordsTriggerAndSkipsOnBusy: the row
// carries the trigger it was launched with, and a launch that loses the lock
// is finalised skipped, never failed (a failed check raises the banner).
func TestBackupRunnerRegistry_LaunchVerify_RecordsTriggerAndSkipsOnBusy(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	svc := buildSvc(t, db, &fakeDocker{}, &fakeRunner{}, &fakeRunner{})
	reg := NewBackupRunnerRegistry(db, svc, slog.Default())
	t.Cleanup(reg.Stop)

	runID, done, err := reg.LaunchVerify("", TriggerScheduled)
	require.NoError(t, err)
	<-done
	run, err := db.GetBackupRunByID(runID)
	require.NoError(t, err)
	assert.Equal(t, TriggerScheduled, run.Trigger)
	assert.Equal(t, "success", run.Status, "control: a free lock gives a real check")

	svc.busy.Store(1)
	runID, done, err = reg.LaunchVerify("", TriggerManual)
	require.NoError(t, err)
	<-done
	svc.busy.Store(0)
	run, err = db.GetBackupRunByID(runID)
	require.NoError(t, err)
	assert.Equal(t, TriggerManual, run.Trigger)
	assert.Equal(t, RunStatusSkipped, run.Status)
	assert.Contains(t, run.ErrorMessage, "another backup operation was in progress")
}
