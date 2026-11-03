package services

import (
	"context"
	"strconv"
	"sync"
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

// seedFinishedBackupRun stores a finished backup run, the base NextRunAt and
// the interval scheduler count from.
func seedFinishedBackupRun(t *testing.T, svc *BackupSchedulerService, finished time.Time) {
	t.Helper()
	at := finished.UTC().Format(time.RFC3339)
	require.NoError(t, svc.db.CreateBackupRun(&models.BackupRun{
		ID: "last-run", Kind: "backup", Trigger: TriggerScheduled, Status: "success",
		StartedAt: at, FinishedAt: &at,
	}))
}

// fireTimesRunner records the fake-clock instant of every RunBackup call.
type fireTimesRunner struct {
	mu    sync.Mutex
	times []time.Time
}

func (r *fireTimesRunner) RunBackup(context.Context, []string, bool, string, chan<- StreamLine) (*models.BackupRun, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.times = append(r.times, time.Now())
	return &models.BackupRun{ID: "test-run", Status: "success"}, nil
}

func (r *fireTimesRunner) fired() []time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]time.Time(nil), r.times...)
}

// nextRunAtFor is what the status endpoint shows for an interval-mode schedule
// over the same database.
func nextRunAtFor(t *testing.T, svc *BackupSchedulerService, interval time.Duration) time.Time {
	t.Helper()
	require.NoError(t, svc.db.SetSetting("backup_schedule_interval", strconv.Itoa(int(interval/time.Minute))))
	bs := buildSvc(t, svc.db, &fakeDocker{}, &fakeRunner{}, &fakeRunner{})
	bs.schedulerActive.Store(true)
	next := bs.NextRunAt()
	require.NotNil(t, next)
	return *next
}

// TestBackupScheduler_OverdueIntervalFiresAtStart (agent-os-dg04): with the
// last backup more than an interval ago, a (re)start must back up now, not one
// full interval later. Counting from process start meant a daily restart with
// a daily interval never backed up at all.
func TestBackupScheduler_OverdueIntervalFiresAtStart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		runner := &fireTimesRunner{}
		svc := newTestBackupScheduler(t, runner)
		defer svc.Close()
		const interval = time.Hour
		start := time.Now()
		seedFinishedBackupRun(t, svc, start.Add(-2*interval))

		svc.Start(interval)
		time.Sleep(time.Millisecond)
		synctest.Wait()
		fired := runner.fired()
		require.Len(t, fired, 1, "an overdue interval backup must run at start")
		assert.WithinDuration(t, start, fired[0], 0, "the overdue backup must run at once")
		assert.WithinDuration(t, start.Add(-interval), nextRunAtFor(t, svc, interval), 0,
			"NextRunAt reports the overdue instant, already past")
	})
}

// TestBackupScheduler_IntervalFirstFireMatchesNextRunAt is the control: a last
// backup half an interval ago fires after the remaining half, exactly when
// NextRunAt says, and the next tick counts a full interval from that fire.
func TestBackupScheduler_IntervalFirstFireMatchesNextRunAt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		runner := &fireTimesRunner{}
		svc := newTestBackupScheduler(t, runner)
		defer svc.Close()
		const interval = time.Hour
		start := time.Now()
		seedFinishedBackupRun(t, svc, start.Add(-interval/2))
		want := nextRunAtFor(t, svc, interval)
		require.WithinDuration(t, start.Add(interval/2), want, 0)

		svc.Start(interval)
		time.Sleep(interval/2 - time.Second)
		synctest.Wait()
		assert.Empty(t, runner.fired(), "nothing is due before NextRunAt")

		time.Sleep(interval + 2*time.Second) // past want+interval
		synctest.Wait()
		fired := runner.fired()
		require.Len(t, fired, 2)
		assert.WithinDuration(t, want, fired[0], 0, "the first fire must be the instant NextRunAt reports")
		assert.WithinDuration(t, want.Add(interval), fired[1], 0, "later ticks count from the first fire")
	})
}
