package services

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// fakeBackupRunner implements backupRunner for tests.
type fakeBackupRunner struct {
	callCount atomic.Int32
	// runFn is called by RunBackup; if nil, returns success immediately.
	runFn func(ctx context.Context, stackIDs []string, dryRun bool, trigger string, out chan<- StreamLine) (*models.BackupRun, error)
}

func (f *fakeBackupRunner) RunBackup(
	ctx context.Context,
	stackIDs []string,
	dryRun bool,
	trigger string,
	out chan<- StreamLine,
) (*models.BackupRun, error) {
	f.callCount.Add(1)
	if f.runFn != nil {
		return f.runFn(ctx, stackIDs, dryRun, trigger, out)
	}
	return &models.BackupRun{ID: "test-run", Status: "success"}, nil
}

// newTestBackupScheduler creates a BackupSchedulerService wired to an
// in-memory DB and the provided runner.
func newTestBackupScheduler(t *testing.T, runner backupRunner) *BackupSchedulerService {
	t.Helper()
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	return NewBackupScheduler(runner, db, nil)
}

// TestBackupScheduler_StartIsRunning verifies that IsRunning returns true
// immediately after Start and false after Stop.
func TestBackupScheduler_StartIsRunning(t *testing.T) {
	runner := &fakeBackupRunner{}
	svc := newTestBackupScheduler(t, runner)

	svc.Start(100 * time.Millisecond)
	assert.True(t, svc.IsRunning(), "IsRunning should be true after Start")

	svc.Stop()
	assert.False(t, svc.IsRunning(), "IsRunning should be false after Stop")
}

// TestBackupScheduler_RestartLifecycle verifies Start → Stop → Start → Stop.
func TestBackupScheduler_RestartLifecycle(t *testing.T) {
	runner := &fakeBackupRunner{}
	svc := newTestBackupScheduler(t, runner)

	svc.Start(100 * time.Millisecond)
	assert.True(t, svc.IsRunning())

	svc.Stop()
	assert.False(t, svc.IsRunning())

	// Restart via the Restart helper.
	svc.Restart(100 * time.Millisecond)
	assert.True(t, svc.IsRunning(), "IsRunning should be true after Restart")

	svc.Stop()
	assert.False(t, svc.IsRunning())
}

// TestBackupScheduler_TickTriggersRunBackup verifies that at least one tick
// causes RunBackup to be called.
func TestBackupScheduler_TickTriggersRunBackup(t *testing.T) {
	runner := &fakeBackupRunner{}
	svc := newTestBackupScheduler(t, runner)

	svc.Start(20 * time.Millisecond)

	assert.Eventually(t, func() bool {
		return runner.callCount.Load() >= 1
	}, 2*time.Second, 5*time.Millisecond, "RunBackup should be called at least once within 2 s")

	svc.Stop()
}

// TestBackupScheduler_RunBackupReceivesCorrectArgs verifies that RunBackup is
// called with nil stackIDs, dryRun=false, and trigger="scheduled".
func TestBackupScheduler_RunBackupReceivesCorrectArgs(t *testing.T) {
	type callArgs struct {
		stackIDs []string
		dryRun   bool
		trigger  string
	}
	calls := make(chan callArgs, 4)

	runner := &fakeBackupRunner{
		runFn: func(ctx context.Context, stackIDs []string, dryRun bool, trigger string, out chan<- StreamLine) (*models.BackupRun, error) {
			calls <- callArgs{stackIDs: stackIDs, dryRun: dryRun, trigger: trigger}
			return &models.BackupRun{ID: "test-run", Status: "success"}, nil
		},
	}
	svc := newTestBackupScheduler(t, runner)

	svc.Start(20 * time.Millisecond)

	var got callArgs
	select {
	case got = <-calls:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for RunBackup call")
	}

	svc.Stop()

	assert.Nil(t, got.stackIDs, "stackIDs should be nil (all enabled policies)")
	assert.False(t, got.dryRun, "dryRun should be false for scheduled runs")
	assert.Equal(t, "scheduled", got.trigger)
}

// TestBackupScheduler_StopWhileIdleIsSafe verifies that stopping a scheduler
// that was never started does not panic.
func TestBackupScheduler_StopWhileIdleIsSafe(t *testing.T) {
	runner := &fakeBackupRunner{}
	svc := newTestBackupScheduler(t, runner)

	// Must not panic.
	svc.Stop()
	assert.False(t, svc.IsRunning())
}

// TestBackupScheduler_DoubleStopIsSafe verifies that calling Stop twice does
// not panic or deadlock.
func TestBackupScheduler_DoubleStopIsSafe(t *testing.T) {
	runner := &fakeBackupRunner{}
	svc := newTestBackupScheduler(t, runner)

	svc.Start(100 * time.Millisecond)
	svc.Stop()
	svc.Stop() // second Stop must be safe

	assert.False(t, svc.IsRunning())
}

// TestBackupScheduler_BusyGuardSkipsTick verifies that when RunBackup takes
// longer than one tick interval, the scheduler does NOT stack up multiple
// concurrent cycles (single-flight guard).
func TestBackupScheduler_BusyGuardSkipsTick(t *testing.T) {
	// blockUntil is closed by the test to unblock the long-running RunBackup.
	blockUntil := make(chan struct{})

	entered := make(chan struct{}, 1)
	var concurrent atomic.Int32
	var maxConcurrent atomic.Int32

	runner := &fakeBackupRunner{
		runFn: func(ctx context.Context, stackIDs []string, dryRun bool, trigger string, out chan<- StreamLine) (*models.BackupRun, error) {
			n := concurrent.Add(1)
			defer concurrent.Add(-1)

			// Track the peak concurrency seen.
			for {
				old := maxConcurrent.Load()
				if n <= old {
					break
				}
				if maxConcurrent.CompareAndSwap(old, n) {
					break
				}
			}

			// Signal first entry.
			select {
			case entered <- struct{}{}:
			default:
			}

			// Block until the test releases us or context is cancelled.
			select {
			case <-blockUntil:
			case <-ctx.Done():
			}
			return &models.BackupRun{Status: "success"}, nil
		},
	}

	svc := newTestBackupScheduler(t, runner)

	// Use a very short interval so multiple ticks fire while the first cycle blocks.
	svc.Start(15 * time.Millisecond)

	// Wait for the first cycle to start.
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout: first RunBackup never called")
	}

	// Let several ticks fire while the first cycle is still blocked.
	time.Sleep(60 * time.Millisecond)

	// Unblock and stop.
	close(blockUntil)
	svc.Stop()

	// Verify peak concurrency never exceeded 1.
	assert.Equal(t, int32(1), maxConcurrent.Load(),
		"concurrent cycles must not exceed 1 (single-flight guard)")
}

// TestBackupScheduler_ErrBackupUnavailableIsHandled verifies that the scheduler
// continues running (does not crash) when RunBackup returns ErrBackupUnavailable.
func TestBackupScheduler_ErrBackupUnavailableIsHandled(t *testing.T) {
	var callCount atomic.Int32
	runner := &fakeBackupRunner{
		runFn: func(ctx context.Context, stackIDs []string, dryRun bool, trigger string, out chan<- StreamLine) (*models.BackupRun, error) {
			callCount.Add(1)
			return nil, ErrBackupUnavailable
		},
	}

	svc := newTestBackupScheduler(t, runner)
	svc.Start(20 * time.Millisecond)

	// Wait for at least 2 calls to confirm the scheduler keeps going.
	assert.Eventually(t, func() bool {
		return callCount.Load() >= 2
	}, 2*time.Second, 5*time.Millisecond, "scheduler should keep ticking after ErrBackupUnavailable")

	svc.Stop()
	assert.False(t, svc.IsRunning())
}

// TestBackupScheduler_SatisfiesInterface verifies at compile time that
// *BackupSchedulerService satisfies the BackupScheduler interface declared in
// backup.go.
func TestBackupScheduler_SatisfiesInterface(t *testing.T) {
	var _ BackupScheduler = (*BackupSchedulerService)(nil)
}
