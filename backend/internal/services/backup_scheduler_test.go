package services

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
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
	t.Cleanup(func() { _ = db.Close() })
	return NewBackupScheduler(runner, db, nil)
}

// TestBackupScheduler_StartIsRunning verifies that IsRunning returns true
// immediately after Start and false after Stop.
func TestBackupScheduler_StartIsRunning(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		runner := &fakeBackupRunner{}
		svc := newTestBackupScheduler(t, runner)

		svc.Start(100 * time.Millisecond)
		assert.True(t, svc.IsRunning(), "IsRunning should be true after Start")

		svc.Stop()
		assert.False(t, svc.IsRunning(), "IsRunning should be false after Stop")
	})
}

// TestBackupScheduler_RestartLifecycle verifies Start → Stop → Start → Stop.
func TestBackupScheduler_RestartLifecycle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
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
	})
}

// TestBackupScheduler_TickTriggersRunBackup verifies that RunBackup is called
// exactly twice after 2.5 tick intervals of a 20ms ticker: ticks land at
// 20ms and 40ms; the third (60ms) has not arrived by 50ms. This replaces the
// original real-time assert.Eventually(">= 1 within 2s"), which could only
// ever prove "at least one eventually" — under the fake clock the exact tick
// count is knowable and asserting it is strictly stronger: it also catches a
// ticker that fires too often (e.g. a duplicate Start) or too rarely.
func TestBackupScheduler_TickTriggersRunBackup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		runner := &fakeBackupRunner{}
		svc := newTestBackupScheduler(t, runner)

		const interval = 20 * time.Millisecond
		svc.Start(interval)

		time.Sleep(interval*2 + interval/2) // 50ms: past the 2nd tick (40ms), short of the 3rd (60ms)
		assert.Equal(t, int32(2), runner.callCount.Load(),
			"a 20ms ticker must have fired exactly twice by 50ms (20ms, 40ms)")

		svc.Stop()
	})
}

// TestBackupScheduler_RunBackupReceivesCorrectArgs verifies that RunBackup is
// called with nil stackIDs, dryRun=false, and trigger="scheduled".
func TestBackupScheduler_RunBackupReceivesCorrectArgs(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
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

		// A direct blocking receive, not a poll: both calls and the timeout
		// are bubble channels, so the bubble's clock advances straight to
		// whichever fires first — here always calls, at the first 20ms tick.
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
	})
}

// TestBackupScheduler_StopWhileIdleIsSafe verifies that stopping a scheduler
// that was never started does not panic.
func TestBackupScheduler_StopWhileIdleIsSafe(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		runner := &fakeBackupRunner{}
		svc := newTestBackupScheduler(t, runner)

		// Must not panic.
		svc.Stop()
		assert.False(t, svc.IsRunning())
	})
}

// TestBackupScheduler_DoubleStopIsSafe verifies that calling Stop twice does
// not panic or deadlock.
func TestBackupScheduler_DoubleStopIsSafe(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		runner := &fakeBackupRunner{}
		svc := newTestBackupScheduler(t, runner)

		svc.Start(100 * time.Millisecond)
		svc.Stop()
		svc.Stop() // second Stop must be safe

		assert.False(t, svc.IsRunning())
	})
}

// TestBackupScheduler_BusyGuardSkipsTick verifies that when RunBackup takes
// longer than one tick interval, the scheduler does NOT stack up multiple
// concurrent cycles (single-flight guard).
// Demonstrates: the beginCycle single-flight guard, not merely "no crash under
// load" — several ticks land while the first RunBackup call is still blocked,
// and every one of them is rejected rather than queued or run concurrently.
// The 15ms-interval/60ms-sleep numbers are unchanged from the original, but
// their meaning changes from "let some load-dependent number of ticks fire"
// (3, 4, or 5 depending on real scheduling) to an exact, arithmetic fact under
// the fake clock: the blocking call is admitted on the tick at 15ms; three
// more ticks land strictly before the 60ms sleep ends, at 30ms, 45ms, and
// 60ms itself is excluded because sleeping exactly 4 intervals from entry
// would race the loop's own tick at that same instant (see
// TestApplyTimer_FiresAndAppliesCachedUpdates for why: a boundary-exact sleep
// leaves the outcome dependent on which of two simultaneously-ready bubble
// goroutines runs first) — so the sleep is offset by half an interval to land
// clear of that boundary, and exactly 3 skips are asserted, not "at least
// one."
func TestBackupScheduler_BusyGuardSkipsTick(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
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

		svc, logs := newLoggingBackupScheduler(t, runner)

		const interval = 15 * time.Millisecond
		svc.Start(interval)

		// A direct blocking receive: the bubble advances straight to the
		// first tick (15ms), which admits the cycle and signals entered.
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("timeout: first RunBackup never called")
		}

		// Three more ticks (30ms, 45ms, 60ms-minus-epsilon) land while the
		// first cycle is still blocked; see the doc comment for the exact
		// arithmetic and the boundary-race reason for the half-interval offset.
		time.Sleep(3*interval + interval/2)

		// Unblock and stop.
		close(blockUntil)
		svc.Stop()

		// Verify peak concurrency never exceeded 1.
		assert.Equal(t, int32(1), maxConcurrent.Load(),
			"concurrent cycles must not exceed 1 (single-flight guard)")
		assert.Equal(t, 3, strings.Count(logs.String(), "Backup cycle still running; skipping tick"),
			"exactly 3 ticks must have been rejected by the guard while the first cycle was blocked")
	})
}

// TestBackupScheduler_ErrBackupUnavailableIsHandled verifies that the scheduler
// continues running (does not crash) when RunBackup returns ErrBackupUnavailable.
//
// Exactly 2 calls by 50ms (2.5 intervals of the 20ms ticker: ticks at 20ms and
// 40ms, with the 3rd at 60ms not yet reached) replaces the original
// assert.Eventually(">= 2 within 2s"), for the same reason as
// TestBackupScheduler_TickTriggersRunBackup: under a real ticker "at least 2"
// was the strongest claim a poll could make; under the fake clock the exact
// count is knowable and is the stronger, more specific claim.
func TestBackupScheduler_ErrBackupUnavailableIsHandled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var callCount atomic.Int32
		runner := &fakeBackupRunner{
			runFn: func(ctx context.Context, stackIDs []string, dryRun bool, trigger string, out chan<- StreamLine) (*models.BackupRun, error) {
				callCount.Add(1)
				return nil, ErrBackupUnavailable
			},
		}

		svc := newTestBackupScheduler(t, runner)
		const interval = 20 * time.Millisecond
		svc.Start(interval)

		time.Sleep(interval*2 + interval/2) // 50ms: past the 2nd tick, short of the 3rd
		assert.Equal(t, int32(2), callCount.Load(),
			"scheduler must keep ticking after ErrBackupUnavailable: exactly 2 ticks by 50ms")

		svc.Stop()
		assert.False(t, svc.IsRunning())
	})
}

// TestBackupScheduler_SatisfiesInterface verifies at compile time that
// *BackupSchedulerService satisfies the BackupScheduler interface declared in
// backup.go.
func TestBackupScheduler_SatisfiesInterface(t *testing.T) {
	var _ BackupScheduler = (*BackupSchedulerService)(nil)
}

// ============================================================
// Scheduled (fixed wall-clock) mode — agent-os-mtbo.3
// ============================================================

// syncBuffer is an io.Writer safe for the slog handler to write to from the
// scheduler goroutine while the test reads it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// newLoggingBackupScheduler is newTestBackupScheduler with a captured logger,
// so a test can observe the goroutine's own lifecycle messages.
func newLoggingBackupScheduler(t *testing.T, runner backupRunner) (*BackupSchedulerService, *syncBuffer) {
	t.Helper()
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	logs := &syncBuffer{}
	logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return NewBackupScheduler(runner, db, logger), logs
}

// everyDayBackup is the all-weekdays schedule used by most scheduled-mode tests.
func everyDayBackup(hour, minute int) DailySchedule {
	return DailySchedule{
		Hour:   hour,
		Minute: minute,
		Days: []time.Weekday{
			time.Sunday, time.Monday, time.Tuesday, time.Wednesday,
			time.Thursday, time.Friday, time.Saturday,
		},
	}
}

// TestArmDurationFor covers the wall-clock re-check cap. Both sides matter: a
// far deadline must be capped so a clock step cannot be slept through, and a
// near deadline must NOT be stretched to the cap or the fire would be late.
func TestArmDurationFor(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		next time.Time
		want time.Duration
	}{
		{"seven days ahead is capped", now.AddDate(0, 0, 7), scheduledWakeInterval},
		{"one hour ahead is capped", now.Add(time.Hour), scheduledWakeInterval},
		{"exactly the cap is not capped further", now.Add(scheduledWakeInterval), scheduledWakeInterval},
		{"inside the cap is used verbatim", now.Add(10 * time.Second), 10 * time.Second},
		{"one nanosecond ahead is used verbatim", now.Add(time.Nanosecond), time.Nanosecond},
		{"already due fires immediately", now, 0},
		{"overdue fires immediately, never negative", now.Add(-time.Hour), 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, armDurationFor(tc.next, now))
		})
	}
}

// TestBackupScheduler_ScheduledModeArmsFromScheduleTime verifies that
// StartScheduled arms for the configured time of day rather than for an
// interval from process start.
func TestBackupScheduler_ScheduledModeArmsFromScheduleTime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		runner := &fakeBackupRunner{}
		svc := newTestBackupScheduler(t, runner)

		before := time.Now()
		svc.StartScheduled(everyDayBackup(2, 0))
		t.Cleanup(svc.Stop)

		assert.True(t, svc.IsRunning(), "IsRunning must be true in scheduled mode")

		next, ok := svc.nextFireAt()
		require.True(t, ok, "scheduled mode must have armed a next fire instant")
		assert.Equal(t, 2, next.Hour(), "must arm for the configured hour")
		assert.Equal(t, 0, next.Minute(), "must arm for the configured minute")
		assert.Equal(t, 0, next.Second(), "must arm on the minute")
		assert.True(t, next.After(before), "next fire must be in the future")
		assert.True(t, next.Sub(before) <= 24*time.Hour+time.Minute,
			"an every-day 02:00 schedule must be at most a day out, got %s", next.Sub(before))

		svc.Stop()
		_, ok = svc.nextFireAt()
		assert.False(t, ok, "Stop must clear the armed instant")
	})
}

// TestBackupScheduler_ScheduledModeArmsFromScheduleDays verifies the weekday
// half: a schedule restricted to a single weekday must land on that weekday.
func TestBackupScheduler_ScheduledModeArmsFromScheduleDays(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		runner := &fakeBackupRunner{}
		svc := newTestBackupScheduler(t, runner)

		svc.StartScheduled(DailySchedule{Hour: 3, Minute: 30, Days: []time.Weekday{time.Wednesday}})
		t.Cleanup(svc.Stop)

		next, ok := svc.nextFireAt()
		require.True(t, ok)
		assert.Equal(t, time.Wednesday, next.Weekday(), "must arm on the only configured weekday")
		assert.Equal(t, 3, next.Hour())
		assert.Equal(t, 30, next.Minute())
		assert.True(t, next.After(time.Now()))
	})
}

// TestBackupScheduler_ScheduledModeEmptyDaysDoesNotStart is the negative half
// of the arming tests: a schedule that fires on no day must not start at all,
// rather than starting and never firing.
func TestBackupScheduler_ScheduledModeEmptyDaysDoesNotStart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		runner := &fakeBackupRunner{}
		svc, logs := newLoggingBackupScheduler(t, runner)

		svc.StartScheduled(DailySchedule{Hour: 2, Minute: 0, Days: nil})

		assert.False(t, svc.IsRunning(), "a day-less schedule must not start the scheduler")
		_, ok := svc.nextFireAt()
		assert.False(t, ok, "a day-less schedule must not arm a fire instant")
		assert.Contains(t, logs.String(), "schedule selects no weekdays",
			"the refusal must be logged loudly, not silent")
	})
}

// TestBackupScheduler_ScheduledModeFiresAndRearms drives the real timer
// goroutine: the first fire is injected milliseconds away via startScheduledAt,
// and the re-arm afterwards must come from the schedule itself.
//
// Demonstrates: the same goroutine that fires the injected near-term instant
// also re-arms itself from the configured DailySchedule afterwards, with no
// restart. Both waits here are for the SAME goroutine's own forward progress
// from a cold start (fire, then re-arm) — not a race against an
// already-ticking periodic timer — so synctest.Wait() is the right tool, and
// the exact fire instant (call it out, not "eventually >= 1") is checkable
// directly afterwards.
func TestBackupScheduler_ScheduledModeFiresAndRearms(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		runner := &fakeBackupRunner{}
		svc := newTestBackupScheduler(t, runner)

		sched := everyDayBackup(2, 0)
		svc.startScheduledAt(sched, time.Now().Add(50*time.Millisecond))
		t.Cleanup(svc.Stop)

		// The injected fire instant is 50ms out; sleeping past it forces the
		// clock across that boundary (Wait() alone would not, since the timer
		// is already armed — see TestApplyTimer_FiresAndAppliesCachedUpdates).
		time.Sleep(60 * time.Millisecond)
		require.Equal(t, int32(1), runner.callCount.Load(),
			"scheduled mode must run exactly one backup at its injected fire instant")

		// After firing, the goroutine re-arms and settles back into its
		// select durably blocked — that transition needs no further clock
		// advance, so Wait() (not another Sleep) is correct here.
		synctest.Wait()
		next, ok := svc.nextFireAt()
		require.True(t, ok)
		assert.Equal(t, 2, next.Hour(), "after firing, the scheduler must re-arm from the schedule")
		assert.Equal(t, 0, next.Minute())

		svc.Stop()
	})
}

// TestBackupScheduler_ScheduledModeUsesCorrectRunBackupArgs verifies the
// scheduled path calls RunBackup exactly as the ticker path does.
func TestBackupScheduler_ScheduledModeUsesCorrectRunBackupArgs(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
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

		svc.startScheduledAt(everyDayBackup(2, 0), time.Now().Add(50*time.Millisecond))
		t.Cleanup(svc.Stop)

		var got callArgs
		select {
		case got = <-calls:
		case <-time.After(2 * time.Second):
			t.Fatal("timeout waiting for scheduled RunBackup call")
		}
		svc.Stop()

		assert.Nil(t, got.stackIDs, "scheduled backups cover all enabled policies")
		assert.False(t, got.dryRun)
		assert.Equal(t, TriggerScheduled, got.trigger)
	})
}

// TestBackupScheduler_StopDuringScheduledWaitIsPrompt verifies that Stop does
// not wait out the armed duration, and that the timer goroutine actually exits
// rather than being abandoned.
//
// Demonstrates: Stop() tears the scheduled-mode goroutine down immediately —
// under the fake clock "immediately" is now an exact claim (elapsed is ~0,
// not "well under the generous 2s CI allowance" the original comment implied)
// even though the armed wait was up to 24 real hours. synctest.Wait() is
// correct for observing the exit log: nothing here needs the clock to
// advance, only for the goroutine Stop() already unblocked to finish running
// and log on its way out.
func TestBackupScheduler_StopDuringScheduledWaitIsPrompt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		runner := &fakeBackupRunner{}
		svc, logs := newLoggingBackupScheduler(t, runner)

		// 02:00 every day: the armed wait is up to 24 hours of wall clock.
		svc.StartScheduled(everyDayBackup(2, 0))
		require.True(t, svc.IsRunning())

		start := time.Now()
		svc.Stop()
		elapsed := time.Since(start)

		assert.False(t, svc.IsRunning(), "IsRunning must be false after Stop")
		assert.Equal(t, time.Duration(0), elapsed,
			"Stop must return immediately under the fake clock, not wait out the armed duration")

		// The goroutine logs on its way out, which is the observable proof it
		// returned rather than leaking while blocked on the timer.
		synctest.Wait()
		assert.Contains(t, logs.String(), "Backup scheduler stopped",
			"the scheduled-mode goroutine must exit on Stop")

		assert.Zero(t, runner.callCount.Load(), "no backup may run after Stop")
	})
}

// TestBackupScheduler_ScheduledDoubleStopAndRestartAreSafe covers the
// lifecycle transitions between the two modes.
func TestBackupScheduler_ScheduledDoubleStopAndRestartAreSafe(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		runner := &fakeBackupRunner{}
		svc := newTestBackupScheduler(t, runner)

		svc.StartScheduled(everyDayBackup(2, 0))
		assert.True(t, svc.IsRunning())
		svc.Stop()
		svc.Stop()
		assert.False(t, svc.IsRunning())

		// scheduled → interval → scheduled, each replacing the other cleanly.
		svc.Start(time.Hour)
		assert.True(t, svc.IsRunning())
		_, ok := svc.nextFireAt()
		assert.False(t, ok, "interval mode must not report an armed schedule instant")

		svc.StartScheduled(everyDayBackup(4, 15))
		assert.True(t, svc.IsRunning())
		next, ok := svc.nextFireAt()
		require.True(t, ok, "switching to scheduled mode must arm an instant")
		assert.Equal(t, 4, next.Hour())

		svc.Stop()
		assert.False(t, svc.IsRunning())
	})
}

// TestBackupScheduler_BeginCycleGuards exercises the single shared guard both
// the ticker and the timer path funnel through. All three outcomes are
// asserted, because a guard tested only in the case that must fire would pass
// on a build with no guard at all.
//
// Demonstrates: beginCycle's guard state (idle/busy/stopped) — the
// synctest.Wait() below is safe because it is waiting on a goroutine JUST
// spawned by beginCycle to reach its own first block point (on <-release),
// not racing an already-armed periodic timer.
func TestBackupScheduler_BeginCycleGuards(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		runner := &fakeBackupRunner{
			runFn: func(ctx context.Context, stackIDs []string, dryRun bool, trigger string, out chan<- StreamLine) (*models.BackupRun, error) {
				<-release
				return &models.BackupRun{ID: "test-run", Status: "success"}, nil
			},
		}
		svc, logs := newLoggingBackupScheduler(t, runner)

		// Idle: a cycle starts.
		require.True(t, svc.beginCycle(context.Background()), "an idle scheduler must start a cycle")
		synctest.Wait()
		require.Equal(t, int32(1), runner.callCount.Load())

		// Busy: the second fire is skipped, with the shared warning.
		assert.False(t, svc.beginCycle(context.Background()), "a fire during a running cycle must skip")
		assert.Equal(t, int32(1), runner.callCount.Load(), "the skipped fire must not run a backup")
		assert.Contains(t, logs.String(), "Backup cycle still running; skipping tick")

		close(release)
		svc.Stop()

		// Stopped: no new cycle may be started once Stop has committed.
		assert.False(t, svc.beginCycle(context.Background()), "a fire after Stop must not start a cycle")
		assert.Equal(t, int32(1), runner.callCount.Load(), "no backup may run after Stop")
	})
}

// ============================================================
// BackupService.StartScheduler / NextRunAt mode branching — agent-os-mtbo.3
//
// These live here rather than in backup_test.go because they are about the
// scheduler, and they are deliberately written in ATTACK/CONTROL pairs: the
// interval-zero guards are the defect this task exists to remove, and a test
// asserting only that scheduled mode starts would pass just as happily on a
// build that ignored ScheduleMode entirely.
// ============================================================

// TestStartScheduler_ScheduledModeStartsWithZeroInterval is the ATTACK half.
// An operator who switches to a fixed time will plausibly zero the interval.
func TestStartScheduler_ScheduledModeStartsWithZeroInterval(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	require.NoError(t, db.SetSetting("backup_schedule_mode", ScheduleModeScheduled))
	require.NoError(t, db.SetSetting("backup_schedule_time", "02:00"))
	require.NoError(t, db.SetSetting("backup_schedule_days", "1,3,5"))
	// backup_schedule_interval deliberately unset → resolves to 0.

	docker := &fakeDocker{}
	runner := &fakeRunner{}
	svc := buildSvc(t, db, docker, runner, runner)

	sched := &fakeScheduler{}
	svc.SetScheduler(sched)
	svc.StartScheduler()

	got := sched.lastScheduled()
	require.NotNil(t, got, "scheduled mode with a zero interval MUST still start the scheduler")
	assert.Equal(t, 2, got.Hour)
	assert.Equal(t, 0, got.Minute)
	assert.Equal(t, []time.Weekday{time.Monday, time.Wednesday, time.Friday}, got.Days)
	assert.True(t, svc.SchedulerRunning(), "schedulerActive must be true so status reports it")
}

// TestStartScheduler_IntervalModeDoesNotStartWithZeroInterval is the CONTROL
// half: the historical "0 = disabled" rule must survive untouched, on the same
// instrument as the test above.
func TestStartScheduler_IntervalModeDoesNotStartWithZeroInterval(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	require.NoError(t, db.SetSetting("backup_schedule_mode", ScheduleModeInterval))
	// backup_schedule_interval deliberately unset → resolves to 0.

	docker := &fakeDocker{}
	runner := &fakeRunner{}
	svc := buildSvc(t, db, docker, runner, runner)

	sched := &fakeScheduler{}
	svc.SetScheduler(sched)
	svc.StartScheduler()

	sched.mu.Lock()
	started := sched.started
	sched.mu.Unlock()
	assert.False(t, started, "interval mode with a zero interval must stay disabled")
	assert.Nil(t, sched.lastScheduled(), "interval mode must never take the scheduled path")
	assert.False(t, svc.SchedulerRunning())
}

// TestStartScheduler_DefaultModeIsInterval verifies that an install with no
// mode row behaves exactly as it did before this change.
func TestStartScheduler_DefaultModeIsInterval(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	require.NoError(t, db.SetSetting("backup_schedule_interval", "45"))
	// backup_schedule_mode deliberately unset → defaults to interval.

	docker := &fakeDocker{}
	runner := &fakeRunner{}
	svc := buildSvc(t, db, docker, runner, runner)

	sched := &fakeScheduler{}
	svc.SetScheduler(sched)
	svc.StartScheduler()

	sched.mu.Lock()
	interval := sched.interval
	started := sched.started
	sched.mu.Unlock()
	assert.True(t, started)
	assert.Equal(t, 45*time.Minute, interval, "the ticker path must be unchanged")
	assert.Nil(t, sched.lastScheduled())
}

// TestStartScheduler_InvalidStoredScheduleFallsBackToInterval covers finding D:
// silence is the worst outcome for a backup feature, so a corrupt schedule row
// must degrade to interval mode rather than to not running.
func TestStartScheduler_InvalidStoredScheduleFallsBackToInterval(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	require.NoError(t, db.SetSetting("backup_schedule_mode", ScheduleModeScheduled))
	require.NoError(t, db.SetSetting("backup_schedule_time", "25:99"))
	require.NoError(t, db.SetSetting("backup_schedule_interval", "30"))

	docker := &fakeDocker{}
	runner := &fakeRunner{}
	svc := buildSvc(t, db, docker, runner, runner)

	sched := &fakeScheduler{}
	svc.SetScheduler(sched)
	svc.StartScheduler()

	sched.mu.Lock()
	started := sched.started
	interval := sched.interval
	sched.mu.Unlock()
	assert.True(t, started, "an unparseable schedule must not silence backups")
	assert.Equal(t, 30*time.Minute, interval, "it must fall back to the configured interval")
	assert.Nil(t, sched.lastScheduled(), "the scheduled path must not be taken with a bad schedule")
	assert.True(t, svc.SchedulerRunning())
}

// TestStartScheduler_InvalidStoredDaysFallsBackToInterval is the weekday half
// of the same fallback.
func TestStartScheduler_InvalidStoredDaysFallsBackToInterval(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	require.NoError(t, db.SetSetting("backup_schedule_mode", ScheduleModeScheduled))
	require.NoError(t, db.SetSetting("backup_schedule_days", "9"))
	require.NoError(t, db.SetSetting("backup_schedule_interval", "15"))

	docker := &fakeDocker{}
	runner := &fakeRunner{}
	svc := buildSvc(t, db, docker, runner, runner)

	sched := &fakeScheduler{}
	svc.SetScheduler(sched)
	svc.StartScheduler()

	sched.mu.Lock()
	interval := sched.interval
	sched.mu.Unlock()
	assert.Equal(t, 15*time.Minute, interval)
	assert.Nil(t, sched.lastScheduled())
}

// TestNextRunAt_ScheduledModeUsesScheduleWithZeroInterval is the explicit
// sibling of TestNextRunAt_NilWhenIntervalZero, which encodes the assumption
// this task removes.
func TestNextRunAt_ScheduledModeUsesScheduleWithZeroInterval(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	require.NoError(t, db.SetSetting("backup_schedule_mode", ScheduleModeScheduled))
	require.NoError(t, db.SetSetting("backup_schedule_time", "03:30"))
	// backup_schedule_interval deliberately unset → 0.

	docker := &fakeDocker{}
	runner := &fakeRunner{}
	svc := buildSvc(t, db, docker, runner, runner)

	sched := &fakeScheduler{}
	svc.SetScheduler(sched)
	svc.schedulerActive.Store(true)

	next := svc.NextRunAt()
	require.NotNil(t, next, "scheduled mode must report a next run even with a zero interval")
	assert.Equal(t, 3, next.Hour())
	assert.Equal(t, 30, next.Minute())
	assert.True(t, next.After(time.Now()))
}

// TestNextRunAt_ScheduledModeIgnoresLastRun proves NextRunAt switched estimator:
// in scheduled mode the last run's finish time is irrelevant.
func TestNextRunAt_ScheduledModeIgnoresLastRun(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	require.NoError(t, db.SetSetting("backup_schedule_mode", ScheduleModeScheduled))
	require.NoError(t, db.SetSetting("backup_schedule_time", "05:45"))
	require.NoError(t, db.SetSetting("backup_schedule_interval", "10"))

	docker := &fakeDocker{}
	runner := &fakeRunner{}
	svc := buildSvc(t, db, docker, runner, runner)
	svc.schedulerActive.Store(true)

	next := svc.NextRunAt()
	require.NotNil(t, next)
	assert.Equal(t, 5, next.Hour(), "scheduled mode must not use last-run-plus-interval")
	assert.Equal(t, 45, next.Minute())
}

// TestNextRunAt_ScheduledModeNilWhenScheduleInvalid: a misconfigured schedule
// has no next instant to display.
func TestNextRunAt_ScheduledModeNilWhenScheduleInvalid(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	require.NoError(t, db.SetSetting("backup_schedule_mode", ScheduleModeScheduled))
	require.NoError(t, db.SetSetting("backup_schedule_days", ""))
	require.NoError(t, db.SetSetting("backup_schedule_time", "notatime"))

	docker := &fakeDocker{}
	runner := &fakeRunner{}
	svc := buildSvc(t, db, docker, runner, runner)
	svc.schedulerActive.Store(true)

	assert.Nil(t, svc.NextRunAt(), "an unparseable schedule has no next run to report")
}
