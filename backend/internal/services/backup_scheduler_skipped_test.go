package services

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// TestBackupScheduler_UnstartedCycleLeavesARunRow pins agent-os-4i7r: a
// scheduled cycle that RunBackup refused before it wrote a run row used to be
// a log line only, so the history showed nothing for the missed backup.
func TestBackupScheduler_UnstartedCycleLeavesARunRow(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		cause      error
		wantStatus string
		wantMsg    string
	}{
		{
			name:       "another operation in progress",
			cause:      ErrBackupBusy,
			wantStatus: RunStatusSkipped,
			wantMsg:    "scheduled backup skipped: another backup, sync or restore was in progress",
		},
		{
			name:       "engine unavailable",
			cause:      ErrBackupUnavailable,
			wantStatus: RunStatusSkipped,
			wantMsg:    "scheduled backup skipped: backup engine unavailable",
		},
		{
			// The shape resolveOrRefuse returns: a fault, not a skip.
			name:       "backup settings unreadable",
			cause:      errors.New("read backup settings: database is locked"),
			wantStatus: "failed",
			wantMsg:    "scheduled backup could not start: read backup settings: database is locked",
		},
		{
			// RunBackup wraps CreateBackupRun's error; still a nil run.
			name:       "run row could not be written",
			cause:      fmt.Errorf("create backup run: %w", errors.New("disk I/O error")),
			wantStatus: "failed",
			wantMsg:    "scheduled backup could not start: create backup run: disk I/O error",
		},
		{
			// A wrapped sentinel is still recognised as a skip.
			name:       "wrapped busy",
			cause:      fmt.Errorf("run backup: %w", ErrBackupBusy),
			wantStatus: RunStatusSkipped,
			wantMsg:    "scheduled backup skipped: another backup, sync or restore was in progress",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			runner := &fakeBackupRunner{
				runFn: func(context.Context, []string, bool, string, chan<- StreamLine) (*models.BackupRun, error) {
					return nil, tc.cause
				},
			}
			svc := newTestBackupScheduler(t, runner)

			svc.runCycle(context.Background())

			runs, err := svc.db.GetBackupRuns(10)
			require.NoError(t, err)
			require.Len(t, runs, 1, "exactly one row for the unstarted cycle")
			r := runs[0]
			assert.Equal(t, tc.wantStatus, r.Status)
			assert.Equal(t, tc.wantMsg, r.ErrorMessage)
			assert.Equal(t, "backup", r.Kind)
			assert.Equal(t, TriggerScheduled, r.Trigger)
			require.NotNil(t, r.FinishedAt, "the row is written already finished, never left running")
			assert.Equal(t, r.StartedAt, *r.FinishedAt)
			assert.Zero(t, r.StacksTotal)
			assert.Nil(t, r.BytesAdded, "nothing ran, so bytes added is unknown, not zero")
		})
	}
}

// TestBackupScheduler_StartedRunGetsNoExtraRow: when RunBackup returns an error
// WITH a run, that run's row already exists and is final. Writing another would
// show one scheduled backup twice.
func TestBackupScheduler_StartedRunGetsNoExtraRow(t *testing.T) {
	t.Parallel()

	runner := &fakeBackupRunner{
		runFn: func(context.Context, []string, bool, string, chan<- StreamLine) (*models.BackupRun, error) {
			return &models.BackupRun{ID: "already-there", Status: "failed"}, errors.New("resolve policies: boom")
		},
	}
	svc := newTestBackupScheduler(t, runner)

	svc.runCycle(context.Background())

	runs, err := svc.db.GetBackupRuns(10)
	require.NoError(t, err)
	assert.Empty(t, runs, "the scheduler must not write a row for a run RunBackup already recorded")
}

// TestBackupScheduler_SkippedTickThroughTheTicker drives the real interval
// scheduler: every tick the engine refuses leaves one skipped row, and a tick
// that lands while the previous cycle is still running leaves none (Edwin's
// decision on agent-os-4i7r: that case would spam the history).
func TestBackupScheduler_SkippedTickThroughTheTicker(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		runner := &fakeBackupRunner{
			runFn: func(context.Context, []string, bool, string, chan<- StreamLine) (*models.BackupRun, error) {
				return nil, ErrBackupUnavailable
			},
		}
		svc := newTestBackupScheduler(t, runner)
		const interval = 20 * time.Millisecond
		svc.Start(interval)
		time.Sleep(interval*2 + interval/2) // ticks at 20ms and 40ms
		svc.Stop()

		runs, err := svc.db.GetBackupRuns(10)
		require.NoError(t, err)
		require.Len(t, runs, 2, "one row per refused tick")
		for _, r := range runs {
			assert.Equal(t, RunStatusSkipped, r.Status)
		}
	})
}

// TestBackupScheduler_OverlappingTickWritesNoRow: the "previous cycle still
// running; skipping tick" path writes nothing.
func TestBackupScheduler_OverlappingTickWritesNoRow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		runner := &fakeBackupRunner{
			runFn: func(ctx context.Context, _ []string, _ bool, _ string, _ chan<- StreamLine) (*models.BackupRun, error) {
				<-release
				return &models.BackupRun{ID: "slow", Status: "success"}, nil
			},
		}
		svc := newTestBackupScheduler(t, runner)
		const interval = 20 * time.Millisecond
		svc.Start(interval)
		time.Sleep(interval*3 + interval/2) // tick 1 starts a cycle, ticks 2 and 3 are skipped
		assert.Equal(t, int32(1), runner.callCount.Load(), "overlapping ticks must not call RunBackup")
		close(release)
		synctest.Wait()
		svc.Stop()

		runs, err := svc.db.GetBackupRuns(10)
		require.NoError(t, err)
		assert.Empty(t, runs, "an overlapping tick must not write a row")
	})
}
