package services

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// blockingCycleRunner returns a runner whose RunBackup blocks until release is
// closed or its context ends, and reports the context's error on result once
// it returns.
func blockingCycleRunner(entered chan<- struct{}, release <-chan struct{}, result chan<- error) *fakeBackupRunner {
	return &fakeBackupRunner{
		runFn: func(ctx context.Context, _ []string, _ bool, _ string, _ chan<- StreamLine) (*models.BackupRun, error) {
			entered <- struct{}{}
			select {
			case <-release:
			case <-ctx.Done():
			}
			result <- ctx.Err()
			return &models.BackupRun{ID: "test-run", Status: "success"}, nil
		},
	}
}

// TestBackupScheduler_ScheduleSaveDoesNotCancelRunningCycle (agent-os-xjl4):
// saving a backup schedule field runs StopScheduler then StartScheduler
// (handlers/backup.go updateSettings), which reach Stop and Start here. A cycle
// already running must survive that and finish with a live context.
func TestBackupScheduler_ScheduleSaveDoesNotCancelRunningCycle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		entered := make(chan struct{}, 1)
		release := make(chan struct{})
		result := make(chan error, 1)
		svc := newTestBackupScheduler(t, blockingCycleRunner(entered, release, result))
		// Deferred so a failed assertion still ends the bubble's goroutines.
		defer svc.Close()

		const interval = 20 * time.Millisecond
		svc.Start(interval)
		<-entered

		// The schedule save.
		svc.Stop()
		svc.Start(interval)

		close(release)
		require.NoError(t, <-result, "a schedule save must not cancel the backup cycle already running")
	})
}

// TestBackupScheduler_CloseCancelsRunningCycle is the control on the same
// instrument: process shutdown must still cancel an in-flight cycle.
func TestBackupScheduler_CloseCancelsRunningCycle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		entered := make(chan struct{}, 1)
		release := make(chan struct{})
		defer close(release)
		result := make(chan error, 1)
		svc := newTestBackupScheduler(t, blockingCycleRunner(entered, release, result))

		svc.Start(20 * time.Millisecond)
		<-entered

		svc.Close()
		assert.ErrorIs(t, <-result, context.Canceled, "shutdown must cancel the running cycle")
	})
}
