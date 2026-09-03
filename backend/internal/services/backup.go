package services

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/pathutil"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

// ErrBackupBusy is returned by RunBackup/RunSync/RunRestore when another
// operation is already in progress. Callers (handlers) map this to HTTP 409.
var ErrBackupBusy = errors.New("backup operation already in progress")

// ErrBackupUnavailable is returned when the required binaries are absent or
// the repository is not reachable.
var ErrBackupUnavailable = errors.New("backup engine unavailable")

// Backup run trigger values. These MUST match the backup_runs.trigger CHECK
// constraint in migrations.go (trigger IN ('manual','scheduled')). Passing any
// other value makes CreateBackupRun fail with a CHECK constraint error, which
// previously broke every user-initiated "Back up now" (the WS handler passed
// "api"). See docker-manager-ly6.
const (
	TriggerManual    = "manual"    // user-initiated (UI "Back up now" / API)
	TriggerScheduled = "scheduled" // automatic, from the backup scheduler
)

// DatabaseBackupTag is the restic tag carried by the capstan.db snapshot.
//
// It is deliberately not a stack ID. Stack snapshots are tagged with the stack
// they came from, so tagging the database distinctly keeps it selectable in the
// Snapshots UI and impossible to mistake for a stack — and lets a restore
// target it directly with `restic snapshots --tag capstan-database`.
const DatabaseBackupTag = "capstan-database"

// databaseStagingDir is the directory under DataDir where the database snapshot
// is staged before restic picks it up.
//
// The path is fixed rather than a random temp dir on purpose: it becomes the
// path recorded inside the restic snapshot, and therefore the path an operator
// types during a restore. A random path would make the runbook unwritable.
const databaseStagingDir = "backup-staging"

// BackupAvailability describes which parts of the engine are functional.
type BackupAvailability struct {
	ResticPresent bool   `json:"resticPresent"`
	RclonePresent bool   `json:"rclonePresent"`
	RepoReachable bool   `json:"repoReachable"`
	Available     bool   `json:"available"`
	Message       string `json:"message,omitempty"`
}

// dockerStopper is the narrow interface BackupService needs from DockerService.
// Defining it here (consumer side) keeps BackupService testable without the
// full DockerService. Stop/Start use the verified lifecycle variants so a
// stop-before-backup (or restart-after) is proven, not just exit-code-trusted —
// this matters for backup consistency in particular.
type dockerStopper interface {
	StopVerified(stack models.Stack) (truth.ActionResult, string)
	StartVerified(stack models.Stack) (truth.ActionResult, string)
	Status(stack models.Stack) (string, []models.Container, error)
}

// BackupScheduler is the interface for the scheduler that will be wired in the
// next task. BackupService holds a pointer slot so main.go can call
// svc.SetScheduler(sched) after construction.
//
// The two Start variants correspond to the two ScheduleModes: Start for
// interval mode, StartScheduled for a fixed wall-clock time on chosen days.
type BackupScheduler interface {
	Start(interval time.Duration)
	StartScheduled(sched DailySchedule)
	Stop()
}

// BackupService orchestrates restic backups, rclone syncs, restores, and
// prune operations for all Capstan-managed stacks. It reuses existing Capstan
// services (DockerService, OperationLock, ActionLogger) so it does not
// duplicate their behaviour.
//
// Availability: if the restic or rclone binaries are absent NewBackupService
// still returns a valid *BackupService (graceful degradation). Write methods
// return ErrBackupUnavailable; status can be queried via Available().
type BackupService struct {
	cfg     *config.Config
	db      *database.DB
	docker  dockerStopper
	opLock  *OperationLock
	actions *ActionLogger
	logger  *slog.Logger

	// sched is set by SetScheduler after construction; may be nil.
	sched BackupScheduler

	// busy is 1 while a global operation (backup/sync/restore/dr) is running.
	busy atomic.Int32

	// schedulerActive tracks whether the periodic backup scheduler is currently
	// started. It reflects StartScheduler/StopScheduler rather than the busy flag
	// so status endpoints can report the scheduler state accurately.
	schedulerActive atomic.Bool

	// resticBin / rcloneBin cache the resolved binary paths at construction
	// time for fast availability checks. Empty = binary absent.
	resticBin string
	rcloneBin string

	// resticMgrFactory / rcloneMgrFactory are factory seams used in tests to
	// inject fake commandRunners. When nil the real constructors are used.
	resticMgrFactory func(bc BackupConfig) *ResticManager
	rcloneMgrFactory func(bc BackupConfig) *RcloneManager
}

// NewBackupService constructs a BackupService. ResticManager and RcloneManager
// are created internally from the resolved config at each call site so that GUI
// changes take effect without a restart. The scheduler field is intentionally
// nil until SetScheduler is called (wired in the next wave).
func NewBackupService(
	cfg *config.Config,
	db *database.DB,
	docker dockerStopper,
	opLock *OperationLock,
	actions *ActionLogger,
) *BackupService {
	svc := &BackupService{
		cfg:     cfg,
		db:      db,
		docker:  docker,
		opLock:  opLock,
		actions: actions,
		logger:  slog.Default().With("component", "backup-service"),
	}

	// Resolve binary paths once at construction. We intentionally do NOT fail
	// if the binaries are absent — the service degrades gracefully.
	if p, err := exec.LookPath("restic"); err == nil {
		svc.resticBin = p
	}
	if p, err := exec.LookPath("rclone"); err == nil {
		svc.rcloneBin = p
	}

	return svc
}

// dockerSvc returns the Docker dependency, substituting a typed-nil
// *DockerService when nothing was wired at all.
//
// The two outage shapes have to converge on one behaviour (agent-os-xay):
// main.go passes the concrete *DockerService, which is a NIL POINTER inside a
// non-nil interface when the daemon was unreachable at startup, while a caller
// that passes no docker leaves a NIL INTERFACE here. Calling a method on the
// former dispatches to DockerService's nil-receiver guards and returns
// ErrDockerUnavailable; calling one on the latter panics. Converting the second
// case into the first means every backup path reports "docker daemon
// unreachable" instead of panicking inside a detached run goroutine, where the
// panic is recovered into an unactionable "panic: runtime error: invalid memory
// address" run status (agent-os-ck4).
func (s *BackupService) dockerSvc() dockerStopper {
	if s.docker == nil {
		return (*DockerService)(nil)
	}
	return s.docker
}

// SetScheduler wires the BackupScheduler (called by main.go after both objects
// are constructed to avoid an import cycle).
func (s *BackupService) SetScheduler(sched BackupScheduler) {
	s.sched = sched
}

// Config returns the config.Config that was supplied at construction time.
// Handlers use it to determine whether a setting value comes from env or DB.
func (s *BackupService) Config() *config.Config {
	return s.cfg
}

// ResolveConfig merges DB settings over the live config.Config and returns the
// effective backup configuration.
//
// Callers outside this package MUST use this rather than resolving on their own.
// The previous exported helper took only a *database.DB and filled in an empty
// config.Config, so cfg.DataDir was "" and the default repository resolved to
// the RELATIVE path "restic-repo" instead of <DataDir>/restic-repo — see
// agent-os-9au. Routing every caller through the service makes that class of
// mistake unrepresentable: there is no longer a way to resolve without the live
// config.
func (s *BackupService) ResolveConfig() BackupConfig {
	return resolveBackupConfig(s.db, s.cfg)
}

// NewResticManager builds a ResticManager from the effective backup config,
// honouring the test factory seam. Handlers use this instead of constructing a
// manager directly, which bypassed both the live config and the seam.
func (s *BackupService) NewResticManager() *ResticManager {
	return s.newResticMgr(s.ResolveConfig())
}

// NewRcloneManager builds an RcloneManager from the effective backup config,
// honouring the test factory seam. See NewResticManager.
func (s *BackupService) NewRcloneManager() *RcloneManager {
	return s.newRcloneMgr(s.ResolveConfig())
}

// SetBins overrides the cached binary paths resolved at construction time.
// This is used by tests (including tests in external packages) to control
// availability without requiring real restic/rclone binaries on the host.
func (s *BackupService) SetBins(resticBin, rcloneBin string) {
	s.resticBin = resticBin
	s.rcloneBin = rcloneBin
}

// SetResticMgrFactory overrides the factory used to create ResticManager
// instances. Used by external test packages that need to inject fake runners.
// Passing nil restores the default (real exec-based) factory.
func (s *BackupService) SetResticMgrFactory(f func(bc BackupConfig) *ResticManager) {
	s.resticMgrFactory = f
}

// SetRcloneMgrFactory overrides the factory used to create RcloneManager
// instances. Used by external test packages that need to inject fake runners.
// Passing nil restores the default (real exec-based) factory.
func (s *BackupService) SetRcloneMgrFactory(f func(bc BackupConfig) *RcloneManager) {
	s.rcloneMgrFactory = f
}

// ForceSetBusy sets the busy flag to 1 (true) or 0 (false). It is used only
// by tests that need to simulate an in-progress operation.
func (s *BackupService) ForceSetBusy(busy bool) {
	if busy {
		s.busy.Store(1)
	} else {
		s.busy.Store(0)
	}
}

// StartScheduler starts the scheduler if it has been set and the resolved
// configuration asks for one. It is called from main.go after wiring.
//
// In interval mode a zero interval means "disabled" and nothing is started.
// In scheduled mode the interval is irrelevant — an operator who switches to a
// fixed time will plausibly zero it — so the interval guard must not be
// consulted, or scheduled mode would silently never run.
func (s *BackupService) StartScheduler() {
	if s.sched == nil {
		return
	}
	bc := resolveBackupConfig(s.db, s.cfg)

	if bc.ScheduleMode == ScheduleModeScheduled {
		sched, err := ParseDailySchedule(bc.ScheduleTime, bc.ScheduleDays)
		if err != nil {
			// Fall back to interval mode rather than to silence: a backup
			// feature that quietly stops backing up is the worst available
			// outcome. The handler rejects bad values with 400, so reaching
			// here means the stored rows were written some other way.
			s.logger.Error("Invalid stored backup schedule; falling back to interval mode",
				"schedule_time", bc.ScheduleTime,
				"schedule_days", bc.ScheduleDays,
				"error", err,
			)
			s.startIntervalScheduler(bc)
			return
		}
		s.sched.StartScheduled(sched)
		s.schedulerActive.Store(true)
		return
	}

	s.startIntervalScheduler(bc)
}

// startIntervalScheduler starts the ticker path, honouring the historical
// "interval <= 0 means disabled" rule.
func (s *BackupService) startIntervalScheduler(bc BackupConfig) {
	if bc.ScheduleInterval <= 0 {
		return
	}
	s.sched.Start(time.Duration(bc.ScheduleInterval) * time.Minute)
	s.schedulerActive.Store(true)
}

// StopScheduler stops the scheduler gracefully.
func (s *BackupService) StopScheduler() {
	if s.sched != nil {
		s.sched.Stop()
	}
	s.schedulerActive.Store(false)
}

// SchedulerRunning reports whether the periodic backup scheduler is currently
// started. Unlike IsBusy (which reflects an in-flight operation) this reflects
// the scheduler lifecycle and is what status endpoints should surface.
func (s *BackupService) SchedulerRunning() bool {
	return s.schedulerActive.Load()
}

// NextRunAt returns the timestamp of the next scheduled automatic backup, or
// nil when the scheduler is not running or is configured not to run.
//
// In scheduled mode the answer is exact: the schedule itself knows its next
// wall-clock instant, so NextAfter(now) is returned directly and the interval
// is not consulted at all (a scheduled-mode install may legitimately have a
// zero interval).
//
// In interval mode it is only an estimate. The scheduler uses a plain
// time.Ticker whose start time is not persisted, so the estimate is derived
// from the most recent backup run's FinishedAt timestamp plus the configured
// interval. When no run exists yet (scheduler just started) the estimate is
// time.Now plus the interval.
func (s *BackupService) NextRunAt() *time.Time {
	if !s.schedulerActive.Load() {
		return nil
	}
	bc := resolveBackupConfig(s.db, s.cfg)

	if bc.ScheduleMode == ScheduleModeScheduled {
		sched, err := ParseDailySchedule(bc.ScheduleTime, bc.ScheduleDays)
		if err != nil {
			// Misconfigured, so there is no next instant to report. The
			// StartScheduler fallback logs this loudly; do not log per status
			// poll.
			return nil
		}
		next, ok := sched.NextAfter(time.Now())
		if !ok {
			return nil
		}
		return &next
	}

	if bc.ScheduleInterval <= 0 {
		return nil
	}
	interval := time.Duration(bc.ScheduleInterval) * time.Minute

	// Use the most recent run's finish time as the base, falling back to now.
	var base time.Time
	runs, err := s.db.GetBackupRuns(1)
	if err == nil && len(runs) > 0 && runs[0].FinishedAt != nil {
		parsed, parseErr := time.Parse(time.RFC3339, *runs[0].FinishedAt)
		if parseErr == nil {
			base = parsed
		}
	}
	if base.IsZero() {
		base = time.Now().UTC()
	}

	next := base.Add(interval)
	return &next
}

// RepoSizeBytes returns the raw-data size of the restic repository in bytes,
// or nil when the repository is not reachable, the binary is absent, or stats
// cannot be retrieved. It never returns an error — failures are silently
// swallowed so that the status endpoint remains responsive.
func (s *BackupService) RepoSizeBytes(ctx context.Context) *int64 {
	if s.resticBin == "" {
		return nil
	}
	bc := resolveBackupConfig(s.db, s.cfg)
	restic := s.newResticMgr(bc)

	size, err := restic.Stats(ctx)
	if err != nil {
		s.logger.Debug("RepoSizeBytes: stats failed", "error", err)
		return nil
	}
	return &size
}

// Available returns the current availability state of the backup engine.
// It is cheap (no exec calls) and safe to call at any time.
func (s *BackupService) Available() BackupAvailability {
	av := BackupAvailability{
		ResticPresent: s.resticBin != "",
		RclonePresent: s.rcloneBin != "",
	}

	if !av.ResticPresent {
		av.Message = "restic binary not found in PATH"
		return av
	}

	av.Available = true
	return av
}

// CheckRepository probes the restic repository and returns whether it is
// reachable. Unlike Available() this performs an actual exec call.
func (s *BackupService) CheckRepository(ctx context.Context) BackupAvailability {
	av := s.Available()
	if !av.ResticPresent {
		return av
	}

	bc := resolveBackupConfig(s.db, s.cfg)
	restic := s.newResticMgr(bc)
	if err := restic.CheckRepository(ctx); err != nil {
		av.RepoReachable = false
		av.Available = false
		av.Message = fmt.Sprintf("repository not reachable: %v", err)
		return av
	}

	av.RepoReachable = true
	av.Available = true
	return av
}

// newResticMgr creates a ResticManager, using the test factory when set.
func (s *BackupService) newResticMgr(bc BackupConfig) *ResticManager {
	if s.resticMgrFactory != nil {
		return s.resticMgrFactory(bc)
	}
	return NewResticManager(bc, s.logger)
}

// newRcloneMgr creates an RcloneManager, using the test factory when set.
func (s *BackupService) newRcloneMgr(bc BackupConfig) *RcloneManager {
	if s.rcloneMgrFactory != nil {
		return s.rcloneMgrFactory(bc)
	}
	return NewRcloneManager(bc, s.logger)
}

// --- single-flight guard helpers ---

func (s *BackupService) tryAcquireGlobal() bool {
	return s.busy.CompareAndSwap(0, 1)
}

func (s *BackupService) releaseGlobal() {
	s.busy.Store(0)
}

// IsBusy reports whether a global backup/sync/restore is currently running.
func (s *BackupService) IsBusy() bool {
	return s.busy.Load() == 1
}

// --- RunBackup ---

// RunBackup runs a local restic backup for the given stackIDs (or all enabled
// stacks when stackIDs is empty). It creates a BackupRun record, iterates
// stacks sequentially, applies the stop policy per stack, records a
// BackupRunItem per stack, and finalises the run with the aggregated status.
//
// Per-stack failures do not abort the run — the final status is "partial" when
// at least one stack failed and at least one succeeded.
//
// Output lines are streamed to out; the caller is responsible for draining the
// channel. out is NOT closed by RunBackup — this mirrors DockerService streaming
// conventions where the caller owns the channel lifecycle.
func (s *BackupService) RunBackup(
	ctx context.Context,
	stackIDs []string,
	dryRun bool,
	trigger string,
	out chan<- StreamLine,
) (*models.BackupRun, error) {
	if !s.tryAcquireGlobal() {
		return nil, ErrBackupBusy
	}
	defer s.releaseGlobal()

	if s.resticBin == "" {
		return nil, ErrBackupUnavailable
	}

	bc := resolveBackupConfig(s.db, s.cfg)
	restic := s.newResticMgr(bc)

	now := time.Now().UTC().Format(time.RFC3339)
	run := &models.BackupRun{
		ID:        uuid.New().String(),
		Kind:      "backup",
		Trigger:   trigger,
		Status:    "running",
		StartedAt: now,
	}

	if err := s.db.CreateBackupRun(run); err != nil {
		return nil, fmt.Errorf("create backup run: %w", err)
	}

	stream(out, "info", fmt.Sprintf("Backup run %s started (dryRun=%v)", run.ID, dryRun))

	// Determine which stacks to back up.
	policies, unresolved, err := s.resolveTargetPolicies(stackIDs)
	if err != nil {
		run.Status = "failed"
		run.ErrorMessage = err.Error()
		s.finaliseRun(run)
		return run, fmt.Errorf("resolve policies: %w", err)
	}

	run.StacksTotal = len(policies)

	var totalBytesAdded int64

	// firstItemErr carries the first per-stack failure up to the run record, so a
	// failed run names its cause instead of showing an empty ErrorMessage
	// (agent-os-ck4: a Docker outage used to surface here as a raw panic string).
	var firstItemErr error

	// The database goes first, deliberately. It is the artifact without which a
	// restore produces an empty Capstan, and capturing it before the per-stack
	// loop means a stack failure part-way through still leaves it in the
	// repository. See backupDatabase for the full rationale (agent-os-36o).
	dbFailed := false
	if dbBytes, dbErr := s.backupDatabase(ctx, restic, dryRun, out); dbErr != nil {
		dbFailed = true
		stream(out, "error", fmt.Sprintf("database snapshot failed: %v", dbErr))
	} else {
		totalBytesAdded += dbBytes
	}

	for _, policy := range policies {
		stackID := policy.TargetID
		itemBytes, itemErr := s.backupStack(ctx, restic, stackID, policy.StopPolicy, dryRun, run.ID, out)
		if itemErr != nil {
			run.StacksFailed++
			if firstItemErr == nil {
				firstItemErr = itemErr
			}
			stream(out, "error", fmt.Sprintf("stack %s failed: %v", stackID, itemErr))
		} else {
			run.StacksOK++
			totalBytesAdded += itemBytes
		}
	}

	// Record the aggregate new bytes written to the repo when at least one stack
	// backed up successfully. A non-nil zero means "ran, added nothing" (dedup /
	// no change); nil means unknown (e.g. the whole run failed before any backup).
	if run.StacksOK > 0 {
		run.BytesAdded = &totalBytesAdded
	}

	// Determine final run status. This is a BackupRun job status
	// ("running"/"success"/"partial"/"failed"), intentionally separate from
	// truth.Outcome action results used elsewhere in the codebase — it tracks
	// the aggregate of many per-stack backup attempts over the run's lifetime,
	// not a single verified action's effect. "partial" here does carry the
	// same domain meaning as truth.OutcomePartial (some targets succeeded,
	// some failed), but the two are not coupled and must not be conflated.
	switch {
	case run.StacksFailed == 0:
		run.Status = "success"
	case run.StacksOK == 0:
		run.Status = "failed"
		if run.ErrorMessage == "" && firstItemErr != nil {
			run.ErrorMessage = firstItemErr.Error()
		}
	default:
		run.Status = "partial"
	}

	// A run that saved every stack but lost the database is not a success. It
	// would look green in the UI while leaving the single most important
	// artifact out of the repository, which is exactly the silent failure
	// agent-os-36o exists to remove.
	if dbFailed && run.Status == "success" {
		run.Status = "partial"
		if run.ErrorMessage == "" {
			run.ErrorMessage = "database snapshot failed; stack backups succeeded"
		}
	}

	// A run asked for specific stacks that have no enabled policy backed up none
	// of them, and the switch above still called it a success: StacksFailed stays
	// 0 when there was nothing to fail. Name them and refuse that verdict
	// (agent-os-6wr). Same principle as the database downgrade directly above —
	// work that did not happen must not read as success.
	if len(unresolved) > 0 {
		msg := fmt.Sprintf("no enabled backup policy for requested stack(s): %s",
			strings.Join(unresolved, ", "))
		stream(out, "error", msg)
		switch {
		case run.StacksOK == 0:
			// Nothing was backed up at all, which is a failed run whatever else
			// happened — including a dbFailed downgrade to "partial" above.
			run.Status = "failed"
		case run.Status == "success":
			run.Status = "partial"
		}
		if run.ErrorMessage == "" {
			run.ErrorMessage = msg
		}
	}

	s.finaliseRun(run)
	stream(out, "info", fmt.Sprintf("Backup run finished: status=%s ok=%d failed=%d",
		run.Status, run.StacksOK, run.StacksFailed))

	// Audit log.
	s.actions.Log("system", nil, ActionBackup, map[string]interface{}{
		"run_id":  run.ID,
		"status":  run.Status,
		"dry_run": dryRun,
	})

	// Optionally sync after backup.
	if !dryRun && bc.SyncAfter && s.rcloneBin != "" {
		stream(out, "info", "Starting post-backup rclone sync")
		if syncErr := s.runSyncInternal(ctx, bc, out); syncErr != nil {
			stream(out, "error", fmt.Sprintf("post-backup sync failed: %v", syncErr))
		}
	}

	return run, nil
}

// RunBackupWithRunID is identical to RunBackup but uses a caller-supplied
// runID (which the caller has already inserted as a "running" BackupRun row).
// This is used by the durable runner registry so the HTTP handler can return
// the runID in the 202 response before the goroutine starts executing.
//
// Callers must insert the BackupRun row with status="running" before calling
// this method. The method will update (not insert) the row on completion.
func (s *BackupService) RunBackupWithRunID(
	ctx context.Context,
	runID string,
	stackIDs []string,
	dryRun bool,
	trigger string,
	out chan<- StreamLine,
) (*models.BackupRun, error) {
	if !s.tryAcquireGlobal() {
		return nil, ErrBackupBusy
	}
	defer s.releaseGlobal()

	if s.resticBin == "" {
		return nil, ErrBackupUnavailable
	}

	bc := resolveBackupConfig(s.db, s.cfg)
	restic := s.newResticMgr(bc)

	now := time.Now().UTC().Format(time.RFC3339)
	run := &models.BackupRun{
		ID:        runID,
		Kind:      "backup",
		Trigger:   trigger,
		Status:    "running",
		StartedAt: now,
	}

	// Row was pre-created by the caller; skip CreateBackupRun.
	stream(out, "info", fmt.Sprintf("Backup run %s started (dryRun=%v)", run.ID, dryRun))

	policies, unresolved, err := s.resolveTargetPolicies(stackIDs)
	if err != nil {
		run.Status = "failed"
		run.ErrorMessage = err.Error()
		s.finaliseRun(run)
		return run, fmt.Errorf("resolve policies: %w", err)
	}

	run.StacksTotal = len(policies)

	var totalBytesAdded int64

	// firstItemErr carries the first per-stack failure up to the run record, so a
	// failed run names its cause instead of showing an empty ErrorMessage
	// (agent-os-ck4: a Docker outage used to surface here as a raw panic string).
	var firstItemErr error

	// The database goes first, deliberately. It is the artifact without which a
	// restore produces an empty Capstan, and capturing it before the per-stack
	// loop means a stack failure part-way through still leaves it in the
	// repository. See backupDatabase for the full rationale (agent-os-36o).
	dbFailed := false
	if dbBytes, dbErr := s.backupDatabase(ctx, restic, dryRun, out); dbErr != nil {
		dbFailed = true
		stream(out, "error", fmt.Sprintf("database snapshot failed: %v", dbErr))
	} else {
		totalBytesAdded += dbBytes
	}

	for _, policy := range policies {
		stackID := policy.TargetID
		itemBytes, itemErr := s.backupStack(ctx, restic, stackID, policy.StopPolicy, dryRun, run.ID, out)
		if itemErr != nil {
			run.StacksFailed++
			if firstItemErr == nil {
				firstItemErr = itemErr
			}
			stream(out, "error", fmt.Sprintf("stack %s failed: %v", stackID, itemErr))
		} else {
			run.StacksOK++
			totalBytesAdded += itemBytes
		}
	}

	if run.StacksOK > 0 {
		run.BytesAdded = &totalBytesAdded
	}

	// BackupRun job status — see the equivalent switch in RunBackup for why
	// this is intentionally distinct from truth.Outcome.
	switch {
	case run.StacksFailed == 0:
		run.Status = "success"
	case run.StacksOK == 0:
		run.Status = "failed"
		if run.ErrorMessage == "" && firstItemErr != nil {
			run.ErrorMessage = firstItemErr.Error()
		}
	default:
		run.Status = "partial"
	}

	// A run that saved every stack but lost the database is not a success. It
	// would look green in the UI while leaving the single most important
	// artifact out of the repository, which is exactly the silent failure
	// agent-os-36o exists to remove.
	if dbFailed && run.Status == "success" {
		run.Status = "partial"
		if run.ErrorMessage == "" {
			run.ErrorMessage = "database snapshot failed; stack backups succeeded"
		}
	}

	// A run asked for specific stacks that have no enabled policy backed up none
	// of them, and the switch above still called it a success: StacksFailed stays
	// 0 when there was nothing to fail. Name them and refuse that verdict
	// (agent-os-6wr). Same principle as the database downgrade directly above —
	// work that did not happen must not read as success.
	if len(unresolved) > 0 {
		msg := fmt.Sprintf("no enabled backup policy for requested stack(s): %s",
			strings.Join(unresolved, ", "))
		stream(out, "error", msg)
		switch {
		case run.StacksOK == 0:
			// Nothing was backed up at all, which is a failed run whatever else
			// happened — including a dbFailed downgrade to "partial" above.
			run.Status = "failed"
		case run.Status == "success":
			run.Status = "partial"
		}
		if run.ErrorMessage == "" {
			run.ErrorMessage = msg
		}
	}

	s.finaliseRun(run)
	stream(out, "info", fmt.Sprintf("Backup run finished: status=%s ok=%d failed=%d",
		run.Status, run.StacksOK, run.StacksFailed))

	s.actions.Log("system", nil, ActionBackup, map[string]interface{}{
		"run_id":  run.ID,
		"status":  run.Status,
		"dry_run": dryRun,
	})

	if !dryRun && bc.SyncAfter && s.rcloneBin != "" {
		stream(out, "info", "Starting post-backup rclone sync")
		if syncErr := s.runSyncInternal(ctx, bc, out); syncErr != nil {
			stream(out, "error", fmt.Sprintf("post-backup sync failed: %v", syncErr))
		}
	}

	return run, nil
}

// backupStack performs the full backup cycle for a single stack (stop, backup,
// verify, retention, restart). It writes a BackupRunItem to the DB and returns
// any error encountered.
func (s *BackupService) backupStack(
	ctx context.Context,
	restic *ResticManager,
	stackID string,
	stopPolicy string,
	dryRun bool,
	runID string,
	out chan<- StreamLine,
) (bytesAdded int64, retErr error) {
	startedAt := time.Now()

	// Per-stack operation lock — prevents a deploy from racing a backup.
	lockToken, lockErr := s.opLock.Acquire(stackID)
	if lockErr != nil {
		return 0, fmt.Errorf("acquire lock: %w", lockErr)
	}
	defer s.opLock.Release(lockToken)

	stream(out, "info", fmt.Sprintf("[%s] starting (policy=%s dryRun=%v)", stackID, stopPolicy, dryRun))

	// Resolve the full stack record so we have the directory path.
	stackRecord, dbErr := s.db.GetStack(stackID)
	if dbErr != nil {
		return 0, fmt.Errorf("get stack %s: %w", stackID, dbErr)
	}
	stack := *stackRecord

	// Determine whether the stack is currently running so we know whether to
	// restart it after backup.
	wasRunning := false
	if status, _, statusErr := s.dockerSvc().Status(stack); statusErr == nil {
		wasRunning = status == "running" || status == "partial"
	}

	stopApplied := false

	// Defensive restart: even if we return early due to a backup failure, we
	// attempt to restart the stack if we stopped it.
	defer func() {
		if stopApplied && wasRunning {
			stream(out, "info", fmt.Sprintf("[%s] restarting stack (defensive)", stackID))
			ar, _ := s.dockerSvc().StartVerified(stack)
			switch ar.Outcome {
			case truth.OutcomeFailed:
				startErr := ar.Err
				if startErr == nil {
					startErr = errors.New(ar.Reason)
				}
				s.logger.Error("defensive restart failed", "stack", stackID, "error", startErr)
				stream(out, "error", fmt.Sprintf("[%s] restart failed: %v", stackID, startErr))
			case truth.OutcomePartial:
				s.logger.Warn("defensive restart partially succeeded", "stack", stackID, "reason", ar.Reason)
			}
		}
	}()

	// Apply stop policy.
	if stopPolicy == "stop" && !dryRun {
		stream(out, "info", fmt.Sprintf("[%s] stopping stack", stackID))
		if ar, _ := s.dockerSvc().StopVerified(stack); ar.Outcome == truth.OutcomeFailed {
			stopErr := ar.Err
			if stopErr == nil {
				stopErr = errors.New(ar.Reason)
			}
			return 0, fmt.Errorf("stop stack: %w", stopErr)
		}
		stopApplied = true
	}

	if dryRun {
		stream(out, "info", fmt.Sprintf("[%s] dry-run: skipping restic backup", stackID))
		s.recordItem(runID, stackID, "success", "", stopApplied, time.Since(startedAt))
		return 0, nil
	}

	// Run backup using the stack's directory as the source path.
	tags := []string{stackID}
	summary, backupErr := restic.Backup(ctx, stack.Directory, tags, out)
	if backupErr != nil {
		retErr = fmt.Errorf("restic backup: %w", backupErr)
		s.recordItem(runID, stackID, "failed", "", stopApplied, time.Since(startedAt))
		return 0, retErr
	}
	if summary != nil {
		bytesAdded = summary.BytesAdded
	}

	// Verify the snapshot we just created.
	if verifyErr := restic.Verify(ctx, stackID, out); verifyErr != nil {
		// Verification failure is non-fatal: log and continue.
		stream(out, "error", fmt.Sprintf("[%s] verify warning: %v", stackID, verifyErr))
	}

	// Apply retention policy.
	if retentionErr := restic.ApplyRetention(ctx, stackID, out); retentionErr != nil {
		stream(out, "error", fmt.Sprintf("[%s] retention warning: %v", stackID, retentionErr))
	}

	// Retrieve the latest snapshot ID for the run item record.
	snapshotID := ""
	if snaps, listErr := restic.ListSnapshots(ctx, stackID, 1); listErr == nil && len(snaps) > 0 {
		snapshotID = snaps[0].ShortID
	}

	s.recordItem(runID, stackID, "success", snapshotID, stopApplied, time.Since(startedAt))
	stream(out, "info", fmt.Sprintf("[%s] completed successfully", stackID))

	// The deferred restart will fire here for the stop-policy path if wasRunning.
	return bytesAdded, nil
}

// --- RunSync ---

// RunSync runs an rclone sync from the local restic repository to the
// configured cloud remote. It is protected by the global single-flight guard.
func (s *BackupService) RunSync(ctx context.Context, out chan<- StreamLine) error {
	if !s.tryAcquireGlobal() {
		return ErrBackupBusy
	}
	defer s.releaseGlobal()

	if s.rcloneBin == "" {
		return ErrBackupUnavailable
	}

	bc := resolveBackupConfig(s.db, s.cfg)
	stream(out, "info", "Starting rclone sync")
	if err := s.runSyncInternal(ctx, bc, out); err != nil {
		return err
	}
	stream(out, "info", "rclone sync completed")
	s.actions.Log("system", nil, ActionBackup, map[string]interface{}{"kind": "sync"})
	return nil
}

// runSyncInternal executes the rclone sync without acquiring the global lock.
// It is called both by RunSync (which holds the lock) and by RunBackup's
// post-backup sync (which already holds the lock).
func (s *BackupService) runSyncInternal(ctx context.Context, bc BackupConfig, out chan<- StreamLine) error {
	if bc.RcloneRemote == "" {
		return fmt.Errorf("rclone remote is not configured")
	}
	rclone := s.newRcloneMgr(bc)
	return rclone.Sync(ctx, bc.ResticRepository, bc.RcloneRemote, bc.RclonePath, bc.RcloneTransfers, 3, out)
}

// --- RunRestore ---

// RunRestore restores a specific snapshot to the stack's directory. It
// validates that the snapshot belongs to the given stackID (contains the
// stack's tag), applies the stop policy before restoring, and restarts the
// stack afterwards. This is a destructive operation and is logged via
// ActionLogger.
func (s *BackupService) RunRestore(
	ctx context.Context,
	stackID string,
	snapshotID string,
	targetDir string,
	out chan<- StreamLine,
) (err error) {
	if !s.tryAcquireGlobal() {
		return ErrBackupBusy
	}
	defer s.releaseGlobal()

	if s.resticBin == "" {
		return ErrBackupUnavailable
	}

	bc := resolveBackupConfig(s.db, s.cfg)
	restic := s.newResticMgr(bc)

	// Validate that the snapshot is tagged with the stack ID.
	if err := s.validateSnapshotBelongsToStack(ctx, restic, snapshotID, stackID); err != nil {
		return fmt.Errorf("snapshot validation: %w", err)
	}

	// P2: load the full stack record so we have the directory and can pass the
	// correct struct to DockerService.Stop/Start.
	stackRecord, dbErr := s.db.GetStack(stackID)
	if dbErr != nil {
		return fmt.Errorf("get stack %s: %w", stackID, dbErr)
	}
	stack := *stackRecord

	// P1: confinement — the restore target must be the stack's own directory.
	// If the caller supplied a targetDir we validate it is identical to (or
	// contained within) the stack's directory. Any path that escapes the stack
	// directory is rejected as a path traversal attempt.
	stackDir := filepath.Clean(stack.Directory)
	restoreTarget := stackDir // default: always restore into the stack directory

	if targetDir != "" {
		cleaned := filepath.Clean(targetDir)
		// Reject relative paths that contain ".." components before cleaning.
		if strings.Contains(targetDir, "..") {
			return models.NewAppError(
				http.StatusBadRequest,
				models.ErrPathTraversal,
				fmt.Sprintf("restore target %q contains path traversal", targetDir),
			)
		}
		// Symlink-aware containment: a symlink inside the stack dir pointing
		// elsewhere must not let `restic restore --target` write outside it (H1).
		contained, err := pathutil.IsContained(stackDir, cleaned)
		if err != nil || !contained {
			return models.NewAppError(
				http.StatusBadRequest,
				models.ErrPathTraversal,
				fmt.Sprintf("restore target %q is outside stack directory %q", targetDir, stackDir),
			)
		}
		restoreTarget = cleaned
	}

	// Acquire per-stack lock.
	lockToken, lockErr := s.opLock.Acquire(stackID)
	if lockErr != nil {
		return fmt.Errorf("acquire lock: %w", lockErr)
	}
	defer s.opLock.Release(lockToken)

	// Determine if the stack was running.
	wasRunning := false
	if status, _, statusErr := s.dockerSvc().Status(stack); statusErr == nil {
		wasRunning = status == "running" || status == "partial"
	}

	// Fetch the stop policy from the backup policy, defaulting to "stop"
	// for restores (restore is always destructive).
	stopPolicy := "stop"
	if policy, pErr := s.db.GetBackupPolicy(stackID); pErr == nil && policy != nil {
		stopPolicy = policy.StopPolicy
	}

	stopApplied := false

	defer func() {
		if !stopApplied || !wasRunning {
			return
		}
		// N13 (agent-os-4pa.7): only restart when the restore SUCCEEDED. A failed
		// restore may have left the stack directory half-written; starting
		// containers over it can corrupt state and destroy the ability to retry
		// the restore cleanly. Leave the stack stopped so the operator can inspect
		// and retry.
		if err != nil {
			stream(out, "error", fmt.Sprintf(
				"[%s] restore failed; stack left stopped deliberately so you can inspect %s and retry (not auto-restarting over a possibly partial restore)",
				stackID, restoreTarget))
			return
		}
		stream(out, "info", fmt.Sprintf("[%s] restarting stack after restore", stackID))
		ar, _ := s.dockerSvc().StartVerified(stack)
		switch ar.Outcome {
		case truth.OutcomeFailed:
			startErr := ar.Err
			if startErr == nil {
				startErr = errors.New(ar.Reason)
			}
			stream(out, "error", fmt.Sprintf("[%s] restart failed: %v", stackID, startErr))
		case truth.OutcomePartial:
			s.logger.Warn("restart after restore partially succeeded", "stack", stackID, "reason", ar.Reason)
		}
	}()

	if stopPolicy == "stop" {
		stream(out, "info", fmt.Sprintf("[%s] stopping stack before restore", stackID))
		if ar, _ := s.dockerSvc().StopVerified(stack); ar.Outcome == truth.OutcomeFailed {
			stopErr := ar.Err
			if stopErr == nil {
				stopErr = errors.New(ar.Reason)
			}
			return fmt.Errorf("stop stack: %w", stopErr)
		}
		stopApplied = true
	}

	stream(out, "info", fmt.Sprintf("[%s] restoring snapshot %s to %s", stackID, snapshotID, restoreTarget))
	// stackDir is the snapshot's stored source path; pass it so restic strips that
	// prefix and restores contents into restoreTarget rather than nesting them.
	if err := restic.Restore(ctx, snapshotID, stackDir, restoreTarget, out); err != nil {
		return fmt.Errorf("restic restore: %w", err)
	}

	stream(out, "info", fmt.Sprintf("[%s] restore completed", stackID))

	s.actions.Log("system", &stackID, ActionRestore, map[string]interface{}{
		"snapshot_id": snapshotID,
		"target_dir":  restoreTarget,
	})

	return nil
}

// --- RunDRRestore ---

// RunDRRestore performs a Stage-3 DR restore: it fetches the restic repository
// from the configured rclone remote back into the configured local restic
// repository so the snapshots can then be restored. This is a destructive,
// long-running operation that requires the caller to have obtained explicit
// confirmation before invoking.
func (s *BackupService) RunDRRestore(ctx context.Context, out chan<- StreamLine) error {
	if !s.tryAcquireGlobal() {
		return ErrBackupBusy
	}
	defer s.releaseGlobal()

	if s.rcloneBin == "" {
		return ErrBackupUnavailable
	}

	bc := resolveBackupConfig(s.db, s.cfg)
	if bc.RcloneRemote == "" {
		return fmt.Errorf("rclone remote is not configured")
	}

	// Destination is the configured local restic repository (server-derived from
	// config/DB, default <DataDir>/restic-repo) — never taken from client input.
	// RestoreRepo writes (via rclone copy, agent-os-h0my) whatever the configured
	// remote:path contains onto this destination, so a client-supplied path would
	// still be an arbitrary host-path write primitive (C1), independent of
	// whether the rclone verb deletes destination-only files.
	localRepoPath := bc.ResticRepository
	if localRepoPath == "" {
		localRepoPath = filepath.Join(s.cfg.DataDir, "restic-repo")
	}
	if err := os.MkdirAll(localRepoPath, 0o700); err != nil {
		return fmt.Errorf("create restic repository directory: %w", err)
	}

	rclone := s.newRcloneMgr(bc)

	stream(out, "info", fmt.Sprintf("Starting DR restore from %s:%s to %s",
		bc.RcloneRemote, bc.RclonePath, localRepoPath))

	if err := rclone.RestoreRepo(ctx, bc.RcloneRemote, bc.RclonePath, localRepoPath, 3, out); err != nil {
		return fmt.Errorf("rclone restore repo: %w", err)
	}

	stream(out, "info", "DR restore completed")
	s.actions.Log("system", nil, ActionRestore, map[string]interface{}{
		"kind":            "dr_restore",
		"local_repo_path": localRepoPath,
	})
	return nil
}

// --- Prune ---

// Prune runs `restic prune` (optionally with --dry-run) against the repository.
// It is protected by the global single-flight guard.
func (s *BackupService) Prune(ctx context.Context, dryRun bool, out chan<- StreamLine) error {
	if !s.tryAcquireGlobal() {
		return ErrBackupBusy
	}
	defer s.releaseGlobal()

	if s.resticBin == "" {
		return ErrBackupUnavailable
	}

	bc := resolveBackupConfig(s.db, s.cfg)
	restic := s.newResticMgr(bc)

	stream(out, "info", fmt.Sprintf("Starting prune (dryRun=%v)", dryRun))
	if err := restic.Prune(ctx, dryRun, out); err != nil {
		return fmt.Errorf("prune: %w", err)
	}

	stream(out, "info", "Prune completed")
	s.actions.Log("system", nil, ActionBackup, map[string]interface{}{
		"kind":    "prune",
		"dry_run": dryRun,
	})
	return nil
}

// --- helpers ---

// resolveTargetPolicies returns the set of BackupPolicies to run against.
// When stackIDs is non-empty, only the listed stacks are included (and they
// must have an enabled policy). When empty, all enabled policies are returned.
// It also returns the requested stack IDs that matched no enabled policy.
// Those used to be dropped on the floor, which is how a run could back up
// nothing and still report success (agent-os-6wr) — callers must surface them.
func (s *BackupService) resolveTargetPolicies(stackIDs []string) ([]models.BackupPolicy, []string, error) {
	all, err := s.db.GetEnabledBackupPolicies()
	if err != nil {
		return nil, nil, err
	}

	// No explicit request means "every enabled policy", so nothing can be
	// unresolved: the caller named no stack it could fail to get.
	if len(stackIDs) == 0 {
		return all, nil, nil
	}

	// Build sets for O(1) lookup in both directions.
	wanted := make(map[string]bool, len(stackIDs))
	for _, id := range stackIDs {
		wanted[id] = true
	}
	enabled := make(map[string]bool, len(all))
	for _, p := range all {
		enabled[p.TargetID] = true
	}

	filtered := make([]models.BackupPolicy, 0, len(stackIDs))
	for _, p := range all {
		if wanted[p.TargetID] {
			filtered = append(filtered, p)
		}
	}

	// Walk stackIDs rather than the set so the reported order matches what the
	// caller asked for, and de-duplicate so a repeated ID is named once.
	var unresolved []string
	seen := make(map[string]bool, len(stackIDs))
	for _, id := range stackIDs {
		if !enabled[id] && !seen[id] {
			seen[id] = true
			unresolved = append(unresolved, id)
		}
	}

	return filtered, unresolved, nil
}

// validateSnapshotBelongsToStack checks that the given snapshot has a tag
// matching stackID. This prevents cross-stack restore mistakes.
func (s *BackupService) validateSnapshotBelongsToStack(
	ctx context.Context,
	restic *ResticManager,
	snapshotID string,
	stackID string,
) error {
	// List all snapshots for this stack (limit=0 = all) and look for snapshotID.
	snaps, err := restic.ListSnapshots(ctx, stackID, 0)
	if err != nil {
		return fmt.Errorf("list snapshots for stack %s: %w", stackID, err)
	}
	for _, snap := range snaps {
		if snap.ID == snapshotID || snap.ShortID == snapshotID {
			return nil
		}
	}
	return fmt.Errorf("snapshot %s does not belong to stack %s", snapshotID, stackID)
}

// recordItem writes a BackupRunItem to the DB, ignoring errors (they are
// logged but do not affect the backup result).
func (s *BackupService) recordItem(
	runID string,
	stackID string,
	status string,
	snapshotID string,
	stopApplied bool,
	duration time.Duration,
) {
	item := &models.BackupRunItem{
		ID:          uuid.New().String(),
		RunID:       runID,
		StackID:     stackID,
		Status:      status,
		SnapshotID:  snapshotID,
		StopApplied: stopApplied,
		DurationMs:  duration.Milliseconds(),
	}
	if err := s.db.AddBackupRunItem(item); err != nil {
		s.logger.Error("failed to record backup run item", "stack", stackID, "error", err)
	}
}

// finaliseRun sets FinishedAt on the run and writes the update to the DB.
func (s *BackupService) finaliseRun(run *models.BackupRun) {
	finished := time.Now().UTC().Format(time.RFC3339)
	run.FinishedAt = &finished
	if err := s.db.UpdateBackupRun(run); err != nil {
		s.logger.Error("failed to update backup run", "run_id", run.ID, "error", err)
	}
}

// stream sends a StreamLine to out in a non-blocking fashion: if out is full
// the line is dropped to avoid deadlocking the backup operation. A nil channel
// is silently ignored.
func stream(out chan<- StreamLine, typ, line string) {
	if out == nil {
		return
	}
	select {
	case out <- StreamLine{Type: typ, Line: line}:
	default:
	}
}

// --- concurrent policy resolution helper ---

// resolveAllEnabled is a thin alias used by the scheduler to fetch all enabled
// policies without specifying individual stack IDs.
func (s *BackupService) resolveAllEnabled() ([]models.BackupPolicy, error) {
	return s.db.GetEnabledBackupPolicies()
}

// --- mutex-guarded multi-stack drain helpers ---

// drainOut reads lines from src and forwards them to dst until src is closed.
// Used when a caller wants to multiplex per-stack channels into the caller's
// channel.
func drainOut(wg *sync.WaitGroup, src <-chan StreamLine, dst chan<- StreamLine) {
	defer wg.Done()
	for line := range src {
		stream(dst, line.Type, line.Line)
	}
}

// DatabaseSnapshotPath returns the absolute path the capstan.db snapshot is
// staged at, and therefore the path recorded inside the restic snapshot.
//
// Exported because the disaster-recovery runbook has to name it exactly: a
// restore writes to <target>/<this path>, and an operator following the runbook
// under pressure should not have to derive it.
func (s *BackupService) DatabaseSnapshotPath() string {
	return filepath.Join(s.cfg.DataDir, databaseStagingDir, "capstan.db")
}

// backupDatabase snapshots capstan.db and hands the artifact to restic under
// DatabaseBackupTag.
//
// Why this exists (agent-os-36o): the backup engine's scope was each stack's
// compose directory. capstan.db — user accounts, the encrypted git tokens and
// restic password, every setting, the backup and auto-update policies, the
// stacks registry and the audit log — was in no snapshot at all. Worse, the
// restic repository defaults to <DataDir>/restic-repo, the same volume the
// database lives on, so losing that one volume lost the database and the local
// snapshots together. The only surviving copy was whatever rclone had synced
// offsite, which contained stack directories and nothing else.
//
// The snapshot is taken with VACUUM INTO rather than by copying the file:
// capstan.db runs in WAL mode, so a file copy taken while writes are in flight
// can be torn or missing recent commits. See database.DB.VacuumInto.
//
// NOTE ON SECRETS: the secrets inside are encrypted with a key derived from
// STORAGE_KEY, which lives in the environment and is deliberately NOT in the
// backup. A stolen backup therefore does not yield the git tokens — but it also
// means restoring onto a host with a different STORAGE_KEY produces a database
// whose secrets cannot be decrypted. The runbook states this; see README.
func (s *BackupService) backupDatabase(
	ctx context.Context,
	restic *ResticManager,
	dryRun bool,
	out chan<- StreamLine,
) (bytesAdded int64, retErr error) {
	dest := s.DatabaseSnapshotPath()

	if dryRun {
		stream(out, "info", fmt.Sprintf("[database] dry run: would snapshot capstan.db to %s", dest))
		return 0, nil
	}

	stream(out, "info", "[database] snapshotting capstan.db (VACUUM INTO)")

	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return 0, fmt.Errorf("create staging dir: %w", err)
	}

	// VACUUM INTO refuses to overwrite. A file here is a leftover from a run
	// that died before its cleanup, so removing it is correct — but only ever
	// this exact path, never a directory.
	if err := os.Remove(dest); err != nil && !os.IsNotExist(err) {
		return 0, fmt.Errorf("remove stale staged snapshot: %w", err)
	}

	if err := s.db.VacuumInto(dest); err != nil {
		return 0, fmt.Errorf("snapshot database: %w", err)
	}

	// The staged copy is a full plaintext database. It must not outlive the run,
	// whatever happens next.
	defer func() {
		if err := os.Remove(dest); err != nil && !os.IsNotExist(err) {
			stream(out, "error", fmt.Sprintf("[database] failed to remove staged snapshot %s: %v", dest, err))
			if retErr == nil {
				retErr = fmt.Errorf("remove staged snapshot: %w", err)
			}
		}
	}()

	summary, err := restic.Backup(ctx, dest, []string{DatabaseBackupTag}, out)
	if err != nil {
		return 0, fmt.Errorf("restic backup database: %w", err)
	}

	if summary != nil {
		bytesAdded = summary.BytesAdded
		stream(out, "info", fmt.Sprintf("[database] snapshot %s captured (%d bytes added)",
			summary.SnapshotID, summary.BytesAdded))
	}

	return bytesAdded, nil
}
