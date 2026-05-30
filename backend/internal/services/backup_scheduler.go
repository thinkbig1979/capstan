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

	mu           sync.Mutex
	ticker       *time.Ticker
	done         chan struct{}
	running      bool // single-flight guard: true while a cycle is executing
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

	if s.ticker != nil {
		s.ticker.Stop()
	}
	if s.done != nil {
		close(s.done)
	}

	// Re-initialise the lifecycle context so a fresh Stop() works correctly.
	if s.parentCancel != nil {
		s.parentCancel()
	}
	s.parentCtx, s.parentCancel = context.WithCancel(context.Background())

	s.ticker = time.NewTicker(interval)
	s.done = make(chan struct{})

	// Capture locals so the goroutine does not race with Stop() zeroing struct fields.
	ticker := s.ticker
	done := s.done
	parentCtx := s.parentCtx

	go func() {
		s.logger.Info("Backup scheduler started", "interval", interval)
		for {
			select {
			case <-ticker.C:
				s.mu.Lock()
				if s.running {
					s.mu.Unlock()
					s.logger.Warn("Backup cycle still running; skipping tick")
					continue
				}
				s.running = true
				s.mu.Unlock()

				s.wg.Add(1)
				go func() {
					defer s.wg.Done()
					defer func() {
						s.mu.Lock()
						s.running = false
						s.mu.Unlock()
					}()
					s.runCycle(parentCtx)
				}()
			case <-done:
				s.logger.Info("Backup scheduler stopped")
				return
			}
		}
	}()
}

// Stop stops the scheduler and waits up to 10 seconds for any in-flight cycle
// to finish. Mirrors SchedulerService.Stop exactly.
func (s *BackupSchedulerService) Stop() {
	s.mu.Lock()

	if s.ticker != nil {
		s.ticker.Stop()
		s.ticker = nil
	}
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

// IsRunning reports whether the scheduler ticker is active.
func (s *BackupSchedulerService) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ticker != nil
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

	run, err := s.runner.RunBackup(cycleCtx, nil, false, "scheduled", out)

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
