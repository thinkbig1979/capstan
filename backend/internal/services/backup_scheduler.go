package services

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// backupCycleTimeout is the maximum duration allowed for a single scheduled
// backup cycle. Two hours is generous enough for large stacks while still
// preventing a stuck cycle from holding the scheduler forever.
const backupCycleTimeout = 2 * time.Hour

// scheduledWakeInterval caps how long the scheduled-mode timer is ever armed
// for. time.Timer is monotonic: it does not re-derive its deadline from the
// wall clock, so an NTP step, a manual clock correction, a DST transition or a
// host suspend/resume shifts the fire time by the full offset. With weekday
// scheduling the natural arm can be seven days, and a home server that
// suspends is a plausible deployment. Waking once a minute to re-compare
// against time.Now() costs nothing and makes all of those self-correcting.
const scheduledWakeInterval = time.Minute

// backupRunner is the narrow interface BackupScheduler needs from BackupService.
// Defining it at the consumer keeps the scheduler testable without the full
// BackupService and its binary dependencies.
type backupRunner interface {
	RunBackup(ctx context.Context, stackIDs []string, dryRun bool, trigger string, out chan<- StreamLine) (*models.BackupRun, error)
}

// BackupSchedulerService is the concrete implementation of BackupScheduler.
// It mirrors SchedulerService's lifecycle exactly: same field layout, same
// locals-capture pattern, same 10-second graceful-shutdown timeout.
type BackupSchedulerService struct {
	runner backupRunner
	db     *database.DB
	logger *slog.Logger

	mu     sync.Mutex
	ticker *time.Ticker
	timer  *time.Timer // scheduled mode; nil in interval mode
	// nextFire is the wall-clock instant the scheduled-mode timer is currently
	// working towards. It is NOT the timer's own deadline: the timer is armed
	// for at most scheduledWakeInterval at a time (see that constant), so the
	// two only coincide within the last minute. Zero in interval mode.
	nextFire time.Time
	done     chan struct{}
	running  bool // single-flight guard: true while a cycle is executing
	// stopped is set under mu by Stop() before it ever calls s.wg.Wait(), and
	// checked under the same mu by the tick handler before it calls
	// s.wg.Add(1). Without this, Add (called from the ticker goroutine,
	// outside of any lock Stop() also takes before its own Wait) is
	// unsynchronized with Stop's Wait from the race detector's point of view:
	// sync.WaitGroup deliberately instruments Add's first-increment and
	// Wait's first-waiter transitions as a modelled read/write on the same
	// location specifically to catch "Add concurrent with Wait" (see
	// sync/waitgroup.go), and that is exactly what could happen here — a tick
	// landing while Stop() is unwinding. Routing both Add and the stopped
	// check through mu gives them the real happens-before edge that was
	// missing (agent-os-o26).
	stopped      bool
	wg           sync.WaitGroup
	parentCtx    context.Context
	parentCancel context.CancelFunc
}

// NewBackupScheduler constructs a BackupSchedulerService. *BackupService
// satisfies backupRunner so callers can pass it directly.
func NewBackupScheduler(runner backupRunner, db *database.DB, logger *slog.Logger) *BackupSchedulerService {
	if logger == nil {
		logger = slog.Default()
	}
	//nolint:gosec // stored on the struct as parentCancel; called by Stop() (see the parentCancel() call in this file's Stop method), same lifecycle pattern as SchedulerService
	ctx, cancel := context.WithCancel(context.Background())
	return &BackupSchedulerService{
		runner:       runner,
		db:           db,
		logger:       logger.With("component", "backup-scheduler"),
		parentCtx:    ctx,
		parentCancel: cancel,
	}
}

// Start starts the scheduler at the given interval. If the scheduler is
// already running it is stopped first (equivalent to Restart). Mirrors
// SchedulerService.Start exactly.
func (s *BackupSchedulerService) Start(interval time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()

	done, parentCtx := s.resetLocked()

	s.ticker = time.NewTicker(interval)
	// Capture the ticker as a local too, so the goroutine does not race with
	// Stop() zeroing struct fields.
	ticker := s.ticker

	go func() {
		s.logger.Info("Backup scheduler started", "interval", interval)
		for {
			select {
			case <-ticker.C:
				s.beginCycle(parentCtx)
			case <-done:
				s.logger.Info("Backup scheduler stopped")
				return
			}
		}
	}()
}

// StartScheduled starts the scheduler in fixed wall-clock mode: it fires at
// sched's time of day on sched's weekdays, in the server's local zone, instead
// of every N minutes from process start. Like Start, calling it while the
// scheduler is running restarts it.
//
// There is no catch-up. If the process was down at a scheduled instant that
// run is simply missed and the next slot is used, which is what an operator
// expects from "back up at 02:00" and avoids a restart storm firing a backup
// immediately after every deploy.
func (s *BackupSchedulerService) StartScheduled(sched DailySchedule) {
	next, ok := sched.NextAfter(time.Now())
	if !ok {
		// Only reachable from a hand-built DailySchedule with no days:
		// ParseDailySchedule and ParseWeekdays both reject an empty list, so a
		// schedule that came through the parser always answers. Refusing to arm
		// is right either way — a schedule that fires on no day has no next
		// instant to wait for.
		s.logger.Error("Backup scheduler not started: schedule selects no weekdays")
		return
	}
	s.startScheduledAt(sched, next)
}

// startScheduledAt is StartScheduled with the FIRST fire instant supplied by
// the caller instead of derived from sched. Every later fire still comes from
// sched.NextAfter, so this only shifts the first one. It exists so tests can
// exercise the real timer goroutine within milliseconds instead of waiting for
// a minute boundary; production always enters through StartScheduled.
func (s *BackupSchedulerService) startScheduledAt(sched DailySchedule, next time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	done, parentCtx := s.resetLocked()

	s.nextFire = next
	// Armed short deliberately; see scheduledWakeInterval.
	s.timer = time.NewTimer(armDurationFor(next, time.Now()))
	timer := s.timer

	go func() {
		s.logger.Info("Backup scheduler started",
			"mode", ScheduleModeScheduled,
			"time", sched.FormatTime(),
			"days", sched.FormatDays(),
			"next_run", next,
		)
		for {
			select {
			case <-timer.C:
				now := time.Now()
				if now.Before(next) {
					// A capped wake, not the scheduled instant. Re-arm against
					// the wall clock, which is the whole point of capping.
					timer.Reset(armDurationFor(next, now))
					continue
				}

				s.beginCycle(parentCtx)

				// Advance past the instant just handled whether or not a cycle
				// actually started: a fire skipped by the single-flight guard
				// waits for the next slot rather than retrying a minute later.
				following, stillOK := sched.NextAfter(now)
				if !stillOK {
					s.logger.Error("Backup scheduler stopping: schedule selects no weekdays")
					return
				}
				next = following
				s.mu.Lock()
				s.nextFire = next
				s.mu.Unlock()
				timer.Reset(armDurationFor(next, time.Now()))
			case <-done:
				// Drain the captured timer on the way out. Stop() already
				// called Stop on it under mu; doing the drain here rather than
				// there keeps the only receive on timer.C in this goroutine.
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				s.logger.Info("Backup scheduler stopped")
				return
			}
		}
	}()
}

// armDurationFor returns how long to arm the scheduled-mode timer for: the
// time remaining until next, capped at scheduledWakeInterval and floored at
// zero so an already-past deadline fires immediately rather than never.
func armDurationFor(next, now time.Time) time.Duration {
	remaining := next.Sub(now)
	if remaining <= 0 {
		return 0
	}
	if remaining > scheduledWakeInterval {
		return scheduledWakeInterval
	}
	return remaining
}

// resetLocked tears down any previous run and installs a fresh lifecycle. The
// caller must hold mu. It returns the new done channel and parent context as
// locals, because the goroutine that uses them must not read the struct fields
// Stop() zeroes.
func (s *BackupSchedulerService) resetLocked() (chan struct{}, context.Context) {
	if s.ticker != nil {
		s.ticker.Stop()
		s.ticker = nil
	}
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	s.nextFire = time.Time{}
	if s.done != nil {
		close(s.done)
	}

	// Re-initialise the lifecycle context so a fresh Stop() works correctly.
	if s.parentCancel != nil {
		s.parentCancel()
	}
	//nolint:gosec // stored on the struct as parentCancel; called by Stop() (or replaced by the next Start()/StartScheduled(), which cancels the old one before creating a new one, as above)
	s.parentCtx, s.parentCancel = context.WithCancel(context.Background())

	s.done = make(chan struct{})
	s.stopped = false

	return s.done, s.parentCtx
}

// beginCycle is the guarded entry to a backup cycle, shared verbatim by the
// interval and scheduled paths so the concurrency discipline exists exactly
// once. It reports whether a cycle was actually started.
func (s *BackupSchedulerService) beginCycle(parentCtx context.Context) bool {
	s.mu.Lock()
	if s.stopped {
		// Stop() has already committed to shutting down (and is about to, or
		// already did, call s.wg.Wait()). Starting a new cycle now would call
		// s.wg.Add outside of Stop's knowledge, racing Wait — see the stopped
		// field's doc comment. Skip the fire instead; the caller's <-done case
		// will fire on the next loop iteration.
		s.mu.Unlock()
		return false
	}
	if s.running {
		s.mu.Unlock()
		s.logger.Warn("Backup cycle still running; skipping tick")
		return false
	}
	s.running = true
	// Add while still holding mu, not after releasing it: Stop() also takes mu
	// (to set stopped) before it ever calls s.wg.Wait(), so this ordering gives
	// Add and Wait a real happens-before edge through the mutex instead of
	// racing (agent-os-o26).
	s.wg.Add(1)
	s.mu.Unlock()

	go func() {
		defer s.wg.Done()
		defer func() {
			s.mu.Lock()
			s.running = false
			s.mu.Unlock()
		}()
		s.runCycle(parentCtx)
	}()
	return true
}

// Stop stops the scheduler and waits up to 10 seconds for any in-flight cycle
// to finish. Mirrors SchedulerService.Stop exactly.
func (s *BackupSchedulerService) Stop() {
	s.mu.Lock()

	// Commit to shutdown before releasing mu (and long before the s.wg.Wait()
	// call below). Any tick handler that acquires mu after this point sees
	// stopped and skips s.wg.Add entirely, so Add can never be invoked
	// concurrently with Wait — see the stopped field's doc comment
	// (agent-os-o26).
	s.stopped = true

	if s.ticker != nil {
		s.ticker.Stop()
		s.ticker = nil
	}
	if s.timer != nil {
		// Stopped here, drained by the scheduled goroutine's <-done branch so
		// that timer.C has exactly one receiver.
		s.timer.Stop()
		s.timer = nil
	}
	s.nextFire = time.Time{}
	if s.done != nil {
		select {
		case <-s.done:
		default:
			close(s.done)
		}
		s.done = nil
	}

	// Cancel any in-flight backup cycle contexts.
	if s.parentCancel != nil {
		s.parentCancel()
	}

	s.mu.Unlock()

	// Wait for in-flight backup cycle goroutines to finish.
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		s.logger.Warn("Timed out waiting for in-flight backup cycle during shutdown")
	}
}

// Restart stops then starts the scheduler with a new interval.
func (s *BackupSchedulerService) Restart(interval time.Duration) {
	s.Stop()
	s.Start(interval)
}

// IsRunning reports whether the scheduler is active in either mode: an
// interval ticker or a scheduled-mode timer.
func (s *BackupSchedulerService) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ticker != nil || s.timer != nil
}

// nextFireAt reports the instant the scheduled-mode timer is working towards.
// ok is false in interval mode and once the scheduler has been stopped.
func (s *BackupSchedulerService) nextFireAt() (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.nextFire, !s.nextFire.IsZero()
}

// runCycle executes one scheduled backup cycle. It creates a buffered output
// channel, drains it into the logger, and calls RunBackup for all enabled
// policies (nil stackIDs = all enabled). Errors are logged; panics never occur.
func (s *BackupSchedulerService) runCycle(ctx context.Context) {
	cycleCtx, cancel := context.WithTimeout(ctx, backupCycleTimeout)
	defer cancel()

	// Buffered so that RunBackup is never blocked on a slow drain goroutine.
	out := make(chan StreamLine, 256)

	// Drain goroutine: consume lines into the logger until out is closed.
	var drainWg sync.WaitGroup
	drainWg.Add(1)
	go func() {
		defer drainWg.Done()
		for line := range out {
			switch line.Type {
			case "error":
				s.logger.Error("backup", "line", line.Line)
			default:
				s.logger.Info("backup", "line", line.Line)
			}
		}
	}()

	run, err := s.runner.RunBackup(cycleCtx, nil, false, TriggerScheduled, out)

	// RunBackup does NOT close out, so we close it here to unblock the drain goroutine.
	close(out)
	drainWg.Wait()

	if err != nil {
		if errors.Is(err, ErrBackupUnavailable) {
			s.logger.Warn("Scheduled backup skipped: backup engine unavailable", "error", err)
			return
		}
		if errors.Is(err, ErrBackupBusy) {
			s.logger.Warn("Scheduled backup skipped: another operation in progress")
			return
		}
		s.logger.Error("Scheduled backup cycle failed", "error", err)
		return
	}

	if run != nil {
		s.logger.Info("Scheduled backup cycle completed",
			"run_id", run.ID,
			"status", run.Status,
			"stacks_ok", run.StacksOK,
			"stacks_failed", run.StacksFailed,
		)
	}
}
