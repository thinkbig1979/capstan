package services

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// Settings keys holding the Docker cleanup policy. Cleanup is GLOBAL -- there
// is no per-stack dangling image -- so it follows the settings-keyed
// backup_auto_prune pattern rather than the per-target auto-update policy
// table.
const (
	SettingDockerCleanupEnabled       = "docker_cleanup_enabled"
	SettingDockerCleanupMinAgeHours   = "docker_cleanup_min_age_hours"
	SettingDockerCleanupIntervalHours = "docker_cleanup_interval_hours"
)

const (
	// DefaultCleanupMinAgeHours is the age floor applied when the setting is
	// unset or unparseable: seven days.
	//
	// This is the DEFAULT, not the floor. MinCleanupAgeHours (docker_cleanup.go)
	// is the floor the API refuses to go below, and it is deliberately much
	// lower -- an operator may legally choose 1h. The spec's deploy-race
	// mitigation ("an image created minutes ago during a deploy or a rollback
	// that nothing references yet") rests on this default, which is what a
	// fresh install gets without choosing anything.
	DefaultCleanupMinAgeHours = 168

	// DefaultCleanupIntervalHours is how often the tick fires when the setting
	// is unset or unparseable. Daily: dangling images accumulate one per
	// auto-update, so anything more frequent buys nothing.
	DefaultCleanupIntervalHours = 24

	// MinCleanupIntervalHours is the floor on the tick interval. An interval of
	// zero or less would make time.NewTicker panic, and a sub-hourly prune has
	// no use case for housekeeping this coarse.
	MinCleanupIntervalHours = 1

	// dockerCleanupCycleTimeout bounds one cleanup cycle. A prune of a badly
	// overgrown image store is slow but not hour-slow; the bound exists so a
	// wedged daemon cannot hold the scheduler forever.
	dockerCleanupCycleTimeout = 30 * time.Minute
)

// dockerCleanupRunner is the narrow interface the scheduler needs from
// DockerCleanupService, declared here on the consumer side.
//
// ONE METHOD ON PURPOSE (agent-os-fn7x.3, D1). The alternative considered and
// rejected was hanging the tick off SchedulerService, whose only Docker seam is
// updateChecker: reaching the pruner from there requires a type assertion that
// COMPILES but that every existing test fake fails, so the tick would silently
// no-op and a "disabled does nothing" test would pass whether or not the policy
// check existed. A one-method interface a fake cannot accidentally fail to
// satisfy removes that whole failure mode.
type dockerCleanupRunner interface {
	Execute(ctx context.Context, trigger string, minAgeHours int) (*models.DockerCleanupRun, error)
}

// DockerCleanupPolicy is the operator-facing cleanup policy, resolved from
// settings.
type DockerCleanupPolicy struct {
	Enabled       bool `json:"enabled"`
	MinAgeHours   int  `json:"minAgeHours"`
	IntervalHours int  `json:"intervalHours"`
}

// ResolveDockerCleanupPolicy reads the three policy keys, falling back to the
// defaults for an ABSENT or unparseable value and returning an error for an
// UNREADABLE one.
//
// That three-way split is the whole point, and it is why this goes through
// readSetting (backup_config.go) rather than db.GetSetting directly: readSetting
// maps sql.ErrNoRows -- and only that -- to the empty string, so an absent row
// is the disabled-by-default state (FR7) while a database that could not answer
// becomes an error the caller must handle. agent-os-rltu and agent-os-r1kc both
// settled that shape as "refuse rather than act at a default when the setting
// cannot be read".
//
// enabled is read FIRST because a settings fault is almost always table-wide, so
// reading it first makes the reported cause the one an operator can act on
// ("could the opt-in be read?") rather than an incidental later key.
func ResolveDockerCleanupPolicy(db *database.DB) (DockerCleanupPolicy, error) {
	enabled, err := resolveBoolSetting(db, SettingDockerCleanupEnabled, "", false)
	if err != nil {
		return DockerCleanupPolicy{}, err
	}
	minAge, err := resolveIntSetting(db, SettingDockerCleanupMinAgeHours, "", DefaultCleanupMinAgeHours)
	if err != nil {
		return DockerCleanupPolicy{}, err
	}
	interval, err := resolveIntSetting(db, SettingDockerCleanupIntervalHours, "", DefaultCleanupIntervalHours)
	if err != nil {
		return DockerCleanupPolicy{}, err
	}
	return DockerCleanupPolicy{
		Enabled:       enabled,
		MinAgeHours:   minAge,
		IntervalHours: interval,
	}, nil
}

// DockerCleanupSchedulerService runs the cleanup tick on its own ticker.
//
// It mirrors BackupSchedulerService's lifecycle field-for-field -- same mu,
// done, running single-flight guard, stopped/wg ordering (agent-os-o26) and
// 10-second graceful-shutdown timeout -- minus the scheduled wall-clock mode,
// which has no counterpart here: the policy is an interval in hours, not a time
// of day.
type DockerCleanupSchedulerService struct {
	runner dockerCleanupRunner
	db     *database.DB
	logger *slog.Logger

	mu      sync.Mutex
	ticker  *time.Ticker
	done    chan struct{}
	running bool // single-flight guard: true while a cycle is executing
	// stopped is set under mu by Stop() before it ever calls s.wg.Wait(), and
	// checked under the same mu before s.wg.Add(1). Without it, Add from the
	// ticker goroutine is unsynchronized with Stop's Wait from the race
	// detector's point of view; see BackupSchedulerService.stopped for the full
	// reasoning (agent-os-o26).
	stopped      bool
	wg           sync.WaitGroup
	parentCtx    context.Context
	parentCancel context.CancelFunc
}

// NewDockerCleanupScheduler constructs the scheduler. *DockerCleanupService
// satisfies dockerCleanupRunner, so callers pass it directly.
func NewDockerCleanupScheduler(runner dockerCleanupRunner, db *database.DB, logger *slog.Logger) *DockerCleanupSchedulerService {
	if logger == nil {
		logger = slog.Default()
	}
	//nolint:gosec // stored on the struct as parentCancel; called by Stop(), same lifecycle pattern as BackupSchedulerService
	ctx, cancel := context.WithCancel(context.Background())
	return &DockerCleanupSchedulerService{
		runner:       runner,
		db:           db,
		logger:       logger.With("component", "docker-cleanup-scheduler"),
		parentCtx:    ctx,
		parentCancel: cancel,
	}
}

// Start starts the ticker at the given interval. Calling it while running is
// equivalent to Restart.
func (s *DockerCleanupSchedulerService) Start(interval time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()

	done, parentCtx := s.resetLocked()

	s.ticker = time.NewTicker(interval)
	// Captured as a local so the goroutine never reads the struct fields Stop()
	// zeroes.
	ticker := s.ticker

	go func() {
		s.logger.Info("Docker cleanup scheduler started", "interval", interval)
		for {
			select {
			case <-ticker.C:
				s.beginCycle(parentCtx)
			case <-done:
				s.logger.Info("Docker cleanup scheduler stopped")
				return
			}
		}
	}()
}

// StartFromPolicy arms the ticker from the stored policy, and is the only entry
// point production uses. It is also what the policy PUT handler calls, so an
// operator who opts in does not have to restart the process for the schedule to
// exist.
//
// Three outcomes, not two:
//   - policy unreadable: log at ERROR and arm nothing. Acting at a default here
//     is the rltu/r1kc shape.
//   - disabled: arm nothing, silently. This is the fresh-install state (FR7) and
//     logging it would print a line on every boot of every install.
//   - enabled: arm at the policy interval.
//
// It always Stops first, so calling it after an operator disables cleanup tears
// the previous ticker down rather than leaving it running.
func (s *DockerCleanupSchedulerService) StartFromPolicy() {
	s.Stop()

	policy, err := ResolveDockerCleanupPolicy(s.db)
	if err != nil {
		s.logger.Error("Docker cleanup scheduler not started: the cleanup policy could not be read, so it is unknown whether an operator opted in; nothing will be pruned until this is resolved",
			"error", err)
		return
	}
	if !policy.Enabled {
		return
	}
	s.Start(time.Duration(clampCleanupIntervalHours(policy.IntervalHours)) * time.Hour)
}

// clampCleanupIntervalHours floors the interval. A ticker built from a
// non-positive duration panics, and the stored value is operator-supplied.
func clampCleanupIntervalHours(h int) int {
	if h < MinCleanupIntervalHours {
		return MinCleanupIntervalHours
	}
	return h
}

// resetLocked tears down any previous run and installs a fresh lifecycle. The
// caller holds mu. It returns the new done channel and parent context as locals
// because the goroutine using them must not read the fields Stop() zeroes.
func (s *DockerCleanupSchedulerService) resetLocked() (chan struct{}, context.Context) {
	if s.ticker != nil {
		s.ticker.Stop()
		s.ticker = nil
	}
	if s.done != nil {
		close(s.done)
	}

	if s.parentCancel != nil {
		s.parentCancel()
	}
	//nolint:gosec // stored on the struct as parentCancel; called by Stop(), or replaced by the next Start() which cancels the old one first, as above
	s.parentCtx, s.parentCancel = context.WithCancel(context.Background())

	s.done = make(chan struct{})
	s.stopped = false

	return s.done, s.parentCtx
}

// beginCycle is the guarded entry to a cleanup cycle. It reports whether a cycle
// was actually started.
func (s *DockerCleanupSchedulerService) beginCycle(parentCtx context.Context) bool {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return false
	}
	if s.running {
		s.mu.Unlock()
		s.logger.Warn("Docker cleanup cycle still running; skipping tick")
		return false
	}
	s.running = true
	// Add while still holding mu: Stop() takes mu to set stopped before it ever
	// calls Wait, so this gives Add and Wait a happens-before edge through the
	// mutex instead of racing (agent-os-o26).
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

// Stop stops the ticker and waits up to 10 seconds for an in-flight cycle.
func (s *DockerCleanupSchedulerService) Stop() {
	s.mu.Lock()

	// Commit to shutdown before releasing mu and long before Wait below, so a
	// tick handler that acquires mu after this point skips wg.Add entirely
	// (agent-os-o26).
	s.stopped = true

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
	if s.parentCancel != nil {
		s.parentCancel()
	}

	s.mu.Unlock()

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		s.logger.Warn("Timed out waiting for in-flight Docker cleanup cycle during shutdown")
	}
}

// IsRunning reports whether the ticker is armed.
func (s *DockerCleanupSchedulerService) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ticker != nil
}

// runCycle is one tick: re-resolve the policy, then prune only if an operator
// opted in.
//
// The policy is re-read every tick rather than captured at arm time, so
// disabling cleanup takes effect at the next tick without a restart -- the
// direction that matters, because it is the one an operator reaches for when
// something is going wrong.
//
// ON AN UNREADABLE POLICY THIS SKIPS AND LOGS AT ERROR, and the log is not
// optional. Cleanup's disabled state happens to coincide with the safe outcome,
// which INVERTS the usual risk: a read fault reads as "the operator turned it
// off", is indistinguishable from having done so deliberately, and its only
// symptom is the disk filling -- which is spec.md:10-13's original incident
// verbatim ("nothing warned"). Absent rows do NOT reach this branch: readSetting
// maps sql.ErrNoRows to the default, so a fresh install logs nothing.
//
// It also never falls back to DefaultCleanupMinAgeHours on a fault. Pruning
// under a floor nobody chose, on the strength of a database fault, is exactly
// what agent-os-rltu and agent-os-r1kc refused.
func (s *DockerCleanupSchedulerService) runCycle(ctx context.Context) {
	policy, err := ResolveDockerCleanupPolicy(s.db)
	if err != nil {
		s.logger.Error("Docker cleanup skipped: the cleanup policy could not be read, so it is unknown whether an operator opted in and under which age floor; nothing was pruned this pass",
			"error", err)
		return
	}
	if !policy.Enabled {
		return
	}

	cycleCtx, cancel := context.WithTimeout(ctx, dockerCleanupCycleTimeout)
	defer cancel()

	run, err := s.runner.Execute(cycleCtx, TriggerScheduled, policy.MinAgeHours)
	if err != nil {
		s.logger.Error("Scheduled Docker cleanup failed", "error", err)
		return
	}
	if run != nil {
		s.logger.Info("Scheduled Docker cleanup completed",
			"run_id", run.ID,
			"status", run.Status,
			"images_deleted", run.ImagesDeleted,
			"bytes_reclaimed", run.BytesReclaimed,
			"cache_bytes_reclaimed", run.CacheBytesReclaimed,
			"min_age_hours", run.MinAgeHours,
		)
	}
}
