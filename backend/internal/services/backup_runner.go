package services

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// RunKind names the five durable streamable operation types stored in the
// backup_runs table.
type RunKind string

const (
	RunKindBackup    RunKind = "backup"
	RunKindSync      RunKind = "sync"
	RunKindRestore   RunKind = "restore"
	RunKindDRRestore RunKind = "dr_restore"
	RunKindPrune     RunKind = "prune"
)

// durableRun is the in-memory state of a single running (or recently finished)
// operation managed by BackupRunnerRegistry.
//
// Lifecycle:
//  1. Created by LaunchX; registered in the registry.
//  2. A goroutine calls the service method on context.Background().
//  3. Every StreamLine is appended to log (replay buffer).
//  4. On completion, outcome/reason are set and done is closed.
type durableRun struct {
	runID string
	kind  RunKind

	// mu protects log and the cursor-based fan-out.
	mu  sync.Mutex
	log []StreamLine // replay buffer, bounded to replayBufMax

	// done is closed exactly once when the op goroutine finishes.
	done chan struct{}

	// outcome and reason are written once (before done closes) then immutable.
	outcome string // "success" | "partial" | "failed"
	reason  string
}

const replayBufMax = 4096

func (r *durableRun) appendLog(line StreamLine) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.log) >= replayBufMax {
		copy(r.log, r.log[1:])
		r.log = r.log[:replayBufMax-1]
	}
	r.log = append(r.log, line)
}

// snapshot returns a copy of the current replay buffer and whether the run
// has already finished.
func (r *durableRun) snapshot() (lines []StreamLine, isDone bool) {
	r.mu.Lock()
	lines = make([]StreamLine, len(r.log))
	copy(lines, r.log)
	r.mu.Unlock()

	select {
	case <-r.done:
		isDone = true
	default:
	}
	return lines, isDone
}

// backupRunStore is the subset of *database.DB that BackupRunnerRegistry uses
// to persist BackupRun rows. *database.DB satisfies it implicitly (Go's
// structural typing), so this is a pure test seam: no change to database.DB
// and no change to any production call site (see NewBackupRunnerRegistry).
//
// It exists because *database.DB is a concrete struct with no injection
// point, so the only way to observe the exact status and ordering of the
// row-creation call versus the row-finalisation call is to substitute a spy
// here. Asserting on the row's value via a real DB read is NOT a substitute:
// the exec goroutine finalises the row within microseconds when the backend
// binaries are unconfigured (as they are in tests), so a read-back races the
// scheduler — see agent-os-icp, where exactly that assertion had to be
// weakened to assert.NotEmpty because it could observe either state.
type backupRunStore interface {
	CreateBackupRun(r *models.BackupRun) error
	UpdateBackupRun(r *models.BackupRun) error
	GetBackupRunByID(id string) (*models.BackupRun, error)
}

// BackupRunnerRegistry manages all in-flight (and recently completed) durable
// backup/restore operations. It is the authoritative runtime store for running
// ops; the database is the persistent store for terminal state.
//
// Architecture:
//   - LaunchX methods: pre-create a DB record with status="running", start a
//     goroutine on context.Background() (decoupled from any request/WS context),
//     register the durableRun, return the runID to the HTTP handler.
//   - Attach: called by the WS handler; returns replay lines + a live stream
//     channel. A WS disconnect does NOT cancel the goroutine.
//   - Background GC removes evicted entries after retentionTTL post-completion.
type BackupRunnerRegistry struct {
	mu     sync.Mutex
	runs   map[string]*durableRun
	db     backupRunStore
	logger *slog.Logger
	svc    *BackupService

	gcStop chan struct{}

	// stopped is set under mu by beginStop() (called from Stop and
	// StopWithTimeout) BEFORE it ever calls wg.Wait(), and checked under the
	// same mu by registerAndAdd() before it calls wg.Add(1). Without this, Add
	// (called from a LaunchX method, outside any lock beginStop also takes
	// before its own Wait) is unsynchronized with Stop's Wait from the race
	// detector's point of view: sync.WaitGroup deliberately instruments Add's
	// first-increment and Wait's first-waiter transitions as a modelled
	// read/write on the same location specifically to catch "Add concurrent
	// with Wait" (see sync/waitgroup.go) — and that panic is NOT gated behind
	// race-detector builds, it fires in production too. A LaunchX call can
	// race a shutdown's Stop/StopWithTimeout for real: srv.Shutdown in
	// main.go has its own 15s bound, and an HTTP handler still executing past
	// that bound keeps running after srv.Shutdown returns, exactly when
	// StopWithTimeout is called next. Routing both Add and the stopped check
	// through mu gives them the real happens-before edge that was missing.
	// Mirrors BackupSchedulerService's identical fix, agent-os-o26; this gap
	// in the registry is agent-os-7a5.
	stopped bool

	// wg tracks every in-flight execX goroutine (execBackup, execRestore,
	// execSync, execDRRestore, execPrune). Add(1) happens in the LaunchX method
	// before the goroutine starts; Done() is the LAST deferred call in execX (see
	// its defer ordering comment), so Wait() only returns once a run's
	// finaliseRunStatus DB write (or recoverExec's, on panic) has completed.
	//
	// This exists because execX goroutines run on context.Background(), detached
	// from any request/WS lifecycle by design (so a WS disconnect can't cancel
	// them) — which means nothing else joins them. Without wg, a test that closes
	// its DB handle or removes its TempDir right after asserting the kickoff
	// response races a still-running goroutine, which then fails with
	// "sql: database is closed" (or, if it still holds files open under a
	// t.TempDir(), a "directory not empty" cleanup error) attributed to whichever
	// unlucky test happens to be running at that moment. See agent-os-80n.
	wg sync.WaitGroup
}

const retentionTTL = 30 * time.Minute

// NewBackupRunnerRegistry constructs the registry and starts the background
// GC goroutine. db is typed as the narrow backupRunStore interface purely as
// a test seam (see its doc comment); every production caller passes a
// *database.DB, which satisfies it implicitly, so this is source-compatible
// with every existing call site.
func NewBackupRunnerRegistry(
	db backupRunStore,
	svc *BackupService,
	logger *slog.Logger,
) *BackupRunnerRegistry {
	reg := &BackupRunnerRegistry{
		runs:   make(map[string]*durableRun),
		db:     db,
		logger: logger,
		svc:    svc,
		gcStop: make(chan struct{}),
	}
	go reg.gcLoop()
	return reg
}

// Stop halts the background GC goroutine and blocks — with no bound — until
// every in-flight execX goroutine (execBackup/execRestore/execSync/
// execDRRestore/execPrune) has fully finished, including its terminal DB
// write. MUST be called by tests (via t.Cleanup) before the test tears down
// anything an execX goroutine still writes to — the DB handle or a
// t.TempDir() the service writes into — since execX runs detached on
// context.Background() and nothing else joins it. See agent-os-80n.
//
// Safe to call more than once, and safe to call concurrently with
// StopWithTimeout: both go through beginStop, which commits reg.stopped and
// closes gcStop exactly once (see beginStop's doc comment), and wg.Wait()
// itself is safe to call from multiple goroutines.
//
// Production shutdown (main.go) uses StopWithTimeout instead: this method's
// unbounded wait is only appropriate for tests, where runs are fast because
// restic/rclone are unconfigured. See agent-os-7a5.
func (reg *BackupRunnerRegistry) Stop() {
	reg.beginStop()
	reg.wg.Wait()
}

// StopWithTimeout behaves like Stop but bounds the wait: it returns true if
// every in-flight execX goroutine finished within timeout, or false if the
// bound expired first. On expiry, any goroutines still running keep running
// (detached on context.Background(), as always) — StopWithTimeout does not
// cancel them, it just stops waiting.
//
// Callers that time out must NOT treat that as data loss: a run still
// "running" in the DB when the process exits is picked up by the startup
// sweeper (database.SweepInterruptedBackupRuns, called on every boot) and
// marked "interrupted", so no row is stranded — see agent-os-7a5's revised
// premise. This is the method main.go's graceful shutdown calls.
//
// Idempotent and safe to call concurrently with Stop or another
// StopWithTimeout (see beginStop). Safe to call even after it has already
// timed out once: beginStop's stopped=true is permanent, so registerAndAdd
// keeps refusing new launches — which is what makes it safe that the
// goroutine spawned below to run wg.Wait() stays parked forever on a timeout
// (nothing after this point can call wg.Add again). Do not remove the
// stopped guard in registerAndAdd thinking it is redundant with the timeout:
// it is the reason a timed-out wait here can never observe a later Add.
func (reg *BackupRunnerRegistry) StopWithTimeout(timeout time.Duration) bool {
	reg.beginStop()

	done := make(chan struct{})
	go func() {
		reg.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

// beginStop commits the registry to shutting down: it sets stopped under mu
// — so registerAndAdd sees it and refuses further wg.Add calls, giving Add
// and Wait a real happens-before edge instead of racing (see stopped's doc
// comment) — and closes gcStop to end the background GC loop. Safe to call
// more than once: the close is guarded by a non-blocking select so a second
// call is a no-op instead of a close-of-closed-channel panic. Mirrors
// BackupSchedulerService.Stop's identical pattern (agent-os-o26).
func (reg *BackupRunnerRegistry) beginStop() {
	reg.mu.Lock()
	reg.stopped = true
	select {
	case <-reg.gcStop:
		// already closed by a previous Stop/StopWithTimeout call
	default:
		close(reg.gcStop)
	}
	reg.mu.Unlock()
}

func (reg *BackupRunnerRegistry) gcLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			reg.evictFinished()
		case <-reg.gcStop:
			return
		}
	}
}

func (reg *BackupRunnerRegistry) evictFinished() {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	for id, dr := range reg.runs {
		select {
		case <-dr.done:
			dbRun, err := reg.db.GetBackupRunByID(id)
			if err != nil || dbRun.FinishedAt == nil {
				continue
			}
			t, err := time.Parse(time.RFC3339, *dbRun.FinishedAt)
			if err != nil {
				continue
			}
			if time.Since(t) > retentionTTL {
				delete(reg.runs, id)
			}
		default:
		}
	}
}

// ErrRegistryStopping is returned by LaunchX methods when a shutdown
// (Stop/StopWithTimeout) has already committed via beginStop. Exported so
// callers — specifically handlers.BackupHandler — can map it to a 503
// rather than a generic 500: a server that is shutting down and refuses a
// new backup is an availability condition, not an internal error. See
// registerAndAdd.
var ErrRegistryStopping = errors.New("backup runner registry is shutting down")

// registerAndAdd inserts dr into the registry and increments wg, atomically
// with the stopped check, under reg.mu — mirroring
// BackupSchedulerService.Start's tick handler, which likewise calls wg.Add
// while still holding the same mutex Stop takes before its own wg.Wait()
// (agent-os-o26). If a stop has already begun, it does neither and returns
// ErrRegistryStopping instead: this is what makes wg.Add "concurrent with
// Wait" from the race detector's perspective impossible, and what stops the
// production-reachable sync.WaitGroup misuse panic (unconditional, not just
// under -race — see sync/waitgroup.go) that an unguarded Add would otherwise
// risk once main.go's shutdown path can call Stop/StopWithTimeout for real.
// See agent-os-7a5.
//
// Callers reach this after already persisting the run's DB row via
// CreateBackupRun (status="running"). If registerAndAdd refuses because a
// stop is in progress, that row is left unfinalised — this is safe, not a
// leak: it is reconciled by the startup sweeper
// (database.SweepInterruptedBackupRuns) on the next boot exactly like any
// other run that was "running" when the process exited.
func (reg *BackupRunnerRegistry) registerAndAdd(dr *durableRun) error {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	if reg.stopped {
		return ErrRegistryStopping
	}
	reg.runs[dr.runID] = dr
	reg.wg.Add(1)
	return nil
}

// --- LaunchX methods ---

// LaunchBackup pre-creates a running BackupRun row, starts the backup on a
// detached goroutine, and returns the runID.
func (reg *BackupRunnerRegistry) LaunchBackup(stackIDs []string, dryRun bool) (string, error) {
	runID := uuid.New().String()
	now := time.Now().UTC().Format(time.RFC3339)

	row := &models.BackupRun{
		ID:        runID,
		Kind:      "backup",
		Trigger:   TriggerManual,
		Status:    "running",
		StartedAt: now,
	}
	if err := reg.db.CreateBackupRun(row); err != nil {
		return "", fmt.Errorf("persist backup run record: %w", err)
	}

	dr := &durableRun{runID: runID, kind: RunKindBackup, done: make(chan struct{})}
	if err := reg.registerAndAdd(dr); err != nil {
		return "", err
	}
	go reg.execBackup(dr, stackIDs, dryRun)
	return runID, nil
}

func (reg *BackupRunnerRegistry) execBackup(dr *durableRun, stackIDs []string, dryRun bool) {
	// wg.Done() is declared FIRST, so in LIFO order it runs LAST — after
	// recoverExec and close(dr.done) below — so reg.Stop()'s Wait() only
	// unblocks once this run's DB write is truly finished.
	defer reg.wg.Done()
	// close(dr.done) runs last (LIFO), so clients see the outcome before unblocking.
	defer close(dr.done)
	// recover() runs before close(dr.done): sets outcome+DB before unblocking clients.
	defer reg.recoverExec(dr)

	out, finish := reg.drainLoop(dr)
	// defer guarantees out is closed (and drain flushed) even if RunBackupWithRunID
	// panics — without it the drain goroutine would leak (B5).
	defer finish()
	ctx := context.Background()

	run, err := reg.svc.RunBackupWithRunID(ctx, dr.runID, stackIDs, dryRun, TriggerManual, out)
	finish()

	if err != nil {
		dr.outcome = "failed"
		dr.reason = err.Error()
		if run == nil {
			// RunBackupWithRunID failed before it could update the DB record.
			reg.finaliseRunStatus(dr.runID, "failed", err.Error())
		}
		reg.logger.Error("durable backup failed", "run_id", dr.runID, "error", err)
		return
	}

	if run != nil {
		dr.outcome = run.Status
	} else {
		dr.outcome = "success"
	}
	reg.logger.Info("durable backup finished", "run_id", dr.runID, "outcome", dr.outcome)
}

// LaunchRestore pre-creates a running BackupRun row, starts the restore on a
// detached goroutine, and returns the runID. A WS client disconnect does NOT
// cancel the restore — it runs to completion regardless.
func (reg *BackupRunnerRegistry) LaunchRestore(stackID, snapshotID, target string) (string, error) {
	runID := uuid.New().String()
	now := time.Now().UTC().Format(time.RFC3339)

	row := &models.BackupRun{
		ID:        runID,
		Kind:      "restore",
		Trigger:   TriggerManual,
		Status:    "running",
		StartedAt: now,
	}
	if err := reg.db.CreateBackupRun(row); err != nil {
		return "", fmt.Errorf("persist restore run record: %w", err)
	}

	dr := &durableRun{runID: runID, kind: RunKindRestore, done: make(chan struct{})}
	if err := reg.registerAndAdd(dr); err != nil {
		return "", err
	}
	go reg.execRestore(dr, stackID, snapshotID, target)
	return runID, nil
}

func (reg *BackupRunnerRegistry) execRestore(dr *durableRun, stackID, snapshotID, target string) {
	// See execBackup's defer ordering comment: declared first so it runs last.
	defer reg.wg.Done()
	defer close(dr.done)
	defer reg.recoverExec(dr)

	out, finish := reg.drainLoop(dr)
	defer finish()
	ctx := context.Background()

	err := reg.svc.RunRestore(ctx, stackID, snapshotID, target, out)
	finish()

	if err != nil {
		dr.outcome = "failed"
		dr.reason = err.Error()
		reg.finaliseRunStatus(dr.runID, "failed", err.Error())
		reg.logger.Error("durable restore failed", "run_id", dr.runID, "error", err)
		return
	}

	dr.outcome = "success"
	dr.reason = "restore completed"
	reg.finaliseRunStatus(dr.runID, "success", "")
	reg.logger.Info("durable restore finished", "run_id", dr.runID)
}

// LaunchSync pre-creates a running BackupRun row and starts the rclone sync.
func (reg *BackupRunnerRegistry) LaunchSync() (string, error) {
	runID := uuid.New().String()
	now := time.Now().UTC().Format(time.RFC3339)

	row := &models.BackupRun{
		ID:        runID,
		Kind:      "sync",
		Trigger:   TriggerManual,
		Status:    "running",
		StartedAt: now,
	}
	if err := reg.db.CreateBackupRun(row); err != nil {
		return "", fmt.Errorf("persist sync run record: %w", err)
	}

	dr := &durableRun{runID: runID, kind: RunKindSync, done: make(chan struct{})}
	if err := reg.registerAndAdd(dr); err != nil {
		return "", err
	}
	go reg.execSync(dr)
	return runID, nil
}

func (reg *BackupRunnerRegistry) execSync(dr *durableRun) {
	// See execBackup's defer ordering comment: declared first so it runs last.
	defer reg.wg.Done()
	defer close(dr.done)
	defer reg.recoverExec(dr)

	out, finish := reg.drainLoop(dr)
	defer finish()
	ctx := context.Background()

	err := reg.svc.RunSync(ctx, out)
	finish()

	if err != nil {
		dr.outcome = "failed"
		dr.reason = err.Error()
		reg.finaliseRunStatus(dr.runID, "failed", err.Error())
		reg.logger.Error("durable sync failed", "run_id", dr.runID, "error", err)
		return
	}

	dr.outcome = "success"
	dr.reason = "sync completed"
	reg.finaliseRunStatus(dr.runID, "success", "")
	reg.logger.Info("durable sync finished", "run_id", dr.runID)
}

// LaunchDRRestore pre-creates a running BackupRun row and starts the DR restore.
// The restore destination is derived server-side (under DataDir) by the service
// and is intentionally not a parameter — see RunDRRestore / finding C1.
func (reg *BackupRunnerRegistry) LaunchDRRestore() (string, error) {
	runID := uuid.New().String()
	now := time.Now().UTC().Format(time.RFC3339)

	row := &models.BackupRun{
		ID:        runID,
		Kind:      "dr_restore",
		Trigger:   TriggerManual,
		Status:    "running",
		StartedAt: now,
	}
	if err := reg.db.CreateBackupRun(row); err != nil {
		return "", fmt.Errorf("persist dr_restore run record: %w", err)
	}

	dr := &durableRun{runID: runID, kind: RunKindDRRestore, done: make(chan struct{})}
	if err := reg.registerAndAdd(dr); err != nil {
		return "", err
	}
	go reg.execDRRestore(dr)
	return runID, nil
}

func (reg *BackupRunnerRegistry) execDRRestore(dr *durableRun) {
	// See execBackup's defer ordering comment: declared first so it runs last.
	defer reg.wg.Done()
	defer close(dr.done)
	defer reg.recoverExec(dr)

	out, finish := reg.drainLoop(dr)
	defer finish()
	ctx := context.Background()

	err := reg.svc.RunDRRestore(ctx, out)
	finish()

	if err != nil {
		dr.outcome = "failed"
		dr.reason = err.Error()
		reg.finaliseRunStatus(dr.runID, "failed", err.Error())
		reg.logger.Error("durable dr-restore failed", "run_id", dr.runID, "error", err)
		return
	}

	dr.outcome = "success"
	dr.reason = "dr-restore completed"
	reg.finaliseRunStatus(dr.runID, "success", "")
	reg.logger.Info("durable dr-restore finished", "run_id", dr.runID)
}

// LaunchPrune pre-creates a running BackupRun row and starts the prune.
func (reg *BackupRunnerRegistry) LaunchPrune(dryRun bool) (string, error) {
	runID := uuid.New().String()
	now := time.Now().UTC().Format(time.RFC3339)

	row := &models.BackupRun{
		ID:        runID,
		Kind:      "prune",
		Trigger:   TriggerManual,
		Status:    "running",
		StartedAt: now,
	}
	if err := reg.db.CreateBackupRun(row); err != nil {
		return "", fmt.Errorf("persist prune run record: %w", err)
	}

	dr := &durableRun{runID: runID, kind: RunKindPrune, done: make(chan struct{})}
	if err := reg.registerAndAdd(dr); err != nil {
		return "", err
	}
	go reg.execPrune(dr, dryRun)
	return runID, nil
}

func (reg *BackupRunnerRegistry) execPrune(dr *durableRun, dryRun bool) {
	// See execBackup's defer ordering comment: declared first so it runs last.
	defer reg.wg.Done()
	defer close(dr.done)
	defer reg.recoverExec(dr)

	out, finish := reg.drainLoop(dr)
	defer finish()
	ctx := context.Background()

	err := reg.svc.Prune(ctx, dryRun, out)
	finish()

	if err != nil {
		dr.outcome = "failed"
		dr.reason = err.Error()
		reg.finaliseRunStatus(dr.runID, "failed", err.Error())
		reg.logger.Error("durable prune failed", "run_id", dr.runID, "error", err)
		return
	}

	dr.outcome = "success"
	dr.reason = "prune completed"
	reg.finaliseRunStatus(dr.runID, "success", "")
	reg.logger.Info("durable prune finished", "run_id", dr.runID)
}

// --- Attach ---

// AttachResult is returned by Attach and carries everything the WS handler
// needs to stream output to the client.
type AttachResult struct {
	// ReplayLines contains log lines captured before this Attach call. The WS
	// handler sends these as data frames immediately after upgrade.
	ReplayLines []StreamLine

	// Done is true when the run has already finished.
	Done bool

	// Outcome and Reason are set only when Done is true.
	Outcome string
	Reason  string

	// Live receives future lines and is closed when the run finishes or the
	// client goes away. It is nil when Done is true, and also when the caller
	// passed a nil clientGone (a state-only lookup, which starts no forwarder
	// — see Attach). Callers that pass a real clientGone and see Done=false
	// are the only ones that may read from it.
	Live <-chan StreamLine

	// Finished is closed when the run goroutine exits (only when Done is false).
	Finished <-chan struct{}
}

// Attach returns the current state of an in-flight or recently-finished run.
// It first checks the in-memory registry; if evicted or not found it falls
// back to the DB record. A WS client calling Attach after the run has finished
// always gets Done=true and the terminal outcome.
//
// clientGone must be a channel that is closed when the WS client disconnects
// (typically wsCtx.Done()). It is forwarded to forwardLive so the fan-out
// goroutine exits promptly on disconnect instead of blocking on a full buffer.
//
// A nil clientGone means "there is no client", and is how status-only callers
// (the WS pre-flight existence check, terminal-outcome lookups) ask for state
// without a stream: no fan-out goroutine is started and Live is nil. Starting
// one for a nil clientGone is not merely wasteful, it is unstoppable — a nil
// channel would have to be replaced by one nobody ever closes, so the
// forwarder could never be told the client had left and would live for the
// whole run, holding a 256-slot buffer no one drains, one orphan per call.
// That was agent-os-jtax; see TestAttach_NilClientGone_StartsNoForwarder,
// whose control arm pins that a real clientGone still gets its forwarder.
func (reg *BackupRunnerRegistry) Attach(runID string, clientGone <-chan struct{}) (*AttachResult, error) {
	reg.mu.Lock()
	dr, inReg := reg.runs[runID]
	reg.mu.Unlock()

	if inReg {
		lines, isDone := dr.snapshot()
		if isDone {
			return &AttachResult{
				ReplayLines: lines,
				Done:        true,
				Outcome:     dr.outcome,
				Reason:      dr.reason,
			}, nil
		}

		// Still running. Only a caller with a real client gets a fan-out
		// goroutine: clientGone is what lets forwardLive exit when that client
		// leaves, so with no clientGone there is nothing to forward to and no
		// way to stop forwarding. State-only callers get Live=nil instead.
		if clientGone == nil {
			return &AttachResult{
				ReplayLines: lines,
				Done:        false,
				Finished:    dr.done,
			}, nil
		}

		live := make(chan StreamLine, 256)
		go reg.forwardLive(dr, len(lines), clientGone, live)

		return &AttachResult{
			ReplayLines: lines,
			Done:        false,
			Live:        live,
			Finished:    dr.done,
		}, nil
	}

	// Not in registry — fall back to DB.
	dbRun, err := reg.db.GetBackupRunByID(runID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("run %q not found", runID)
		}
		return nil, fmt.Errorf("look up run %q: %w", runID, err)
	}

	outcome := dbRun.Status
	reason := dbRun.ErrorMessage

	// 'interrupted' (agent-os-pid) is the startup sweep's own terminal status
	// for a run that was 'running' when this process started — reached via
	// exactly this fallback (registry is process-local, so it's empty for
	// every run from before a restart). It must count as terminal here, or
	// the sweep's specific error_message gets discarded in favour of the
	// generic "operation state lost" reason below.
	isTerminal := outcome == "success" || outcome == "partial" || outcome == "failed" || outcome == "interrupted"
	if !isTerminal {
		// DB says "running" but no in-memory entry — likely a server restart.
		outcome = "failed"
		reason = "operation state lost (server may have restarted)"
	}
	if reason == "" {
		reason = outcome
	}

	return &AttachResult{
		Done:    true,
		Outcome: outcome,
		Reason:  reason,
	}, nil
}

// forwardLive polls the durableRun's replay buffer for new entries and
// forwards them to live until the run finishes or clientGone is closed.
//
// cursor is the index of the first unseen line in dr.log.
// clientGone should be the WS handler's wsCtx.Done() channel; when it fires
// the forwarder stops immediately instead of blocking on a full buffer.
// This prevents the goroutine+buffer leak that occurs when a disconnected
// client's 256-line buffer fills and the send loop blocks indefinitely.
func (reg *BackupRunnerRegistry) forwardLive(dr *durableRun, cursor int, clientGone <-chan struct{}, live chan<- StreamLine) {
	defer close(live)

	pollInterval := 50 * time.Millisecond

	// trySend attempts a non-blocking send on live. Returns false (and exits
	// the forwarder) when the client is gone or the channel is persistently full.
	trySend := func(l StreamLine) bool {
		select {
		case <-clientGone:
			return false
		case live <- l:
			return true
		}
	}

	for {
		select {
		case <-clientGone:
			// Client is gone — exit immediately; don't block on a full buffer.
			return

		case <-dr.done:
			// Flush any lines written between the last poll and done.
			dr.mu.Lock()
			tail := make([]StreamLine, len(dr.log[cursor:]))
			copy(tail, dr.log[cursor:])
			dr.mu.Unlock()
			for _, l := range tail {
				if !trySend(l) {
					return
				}
			}
			return

		case <-time.After(pollInterval):
			dr.mu.Lock()
			newLines := dr.log[cursor:]
			batch := make([]StreamLine, len(newLines))
			copy(batch, newLines)
			cursor += len(batch)
			dr.mu.Unlock()
			for _, l := range batch {
				if !trySend(l) {
					return
				}
			}
		}
	}
}

// --- internal helpers ---

// drainLoop starts a goroutine that reads from out and appends every line to
// dr's replay buffer. It returns out (the channel service methods write to) and
// finish: an idempotent func that closes out and blocks until the drain
// goroutine has flushed every buffered line.
//
// finish MUST be deferred by the caller so that a panic in the service method
// still closes out — otherwise the drain goroutine blocks forever on `range out`
// and leaks (finding B5). The normal path may also call finish() explicitly
// (before setting the outcome) so all lines are in the replay buffer before
// dr.done closes; the sync.Once makes the second (deferred) call a no-op.
func (reg *BackupRunnerRegistry) drainLoop(dr *durableRun) (out chan StreamLine, finish func()) {
	out = make(chan StreamLine, 512)
	drainDone := make(chan struct{})
	go func() {
		defer close(drainDone)
		for line := range out {
			dr.appendLog(line)
		}
	}()
	var once sync.Once
	finish = func() {
		once.Do(func() {
			close(out)
			<-drainDone
		})
	}
	return out, finish
}

// recoverExec is a deferred panic handler for the execX goroutines.
//
// If a service method panics (e.g. nil dereference inside restic/rclone code),
// this handler:
//  1. Captures the panic value.
//  2. Sets dr.outcome / dr.reason so Attach returns the terminal state
//     (close(dr.done) runs next, unblocking any waiting WS clients).
//  3. Updates the DB row to failed so the run is never stuck "running".
//
// Defer ordering in execX: recoverExec is declared AFTER close(dr.done) so in
// LIFO order it runs BEFORE close(dr.done) — clients see the outcome first.
//
// Without this, a panic would:
//   - Leave dr.outcome="" and the DB row stuck at status="running" forever.
//   - Propagate out of the goroutine and crash the entire process, killing
//     every other in-flight backup/restore operation.
func (reg *BackupRunnerRegistry) recoverExec(dr *durableRun) {
	r := recover()
	if r == nil {
		return // no panic — normal path
	}
	msg := fmt.Sprintf("panic: %v", r)
	reg.logger.Error("exec goroutine panicked; finalising run as failed",
		"run_id", dr.runID, "panic", r)

	// Only overwrite if the normal path did not already set an outcome (it won't
	// have, because a panic unwinds before the normal outcome assignment).
	if dr.outcome == "" {
		dr.outcome = "failed"
		dr.reason = msg
	}
	reg.finaliseRunStatus(dr.runID, "failed", msg)
}

// finaliseRunStatus updates a BackupRun DB record to a terminal status. Used
// by exec* goroutines for operations whose service methods do NOT manage their
// own DB records (sync, restore, dr-restore, prune).
func (reg *BackupRunnerRegistry) finaliseRunStatus(runID, status, errMsg string) {
	dbRun, err := reg.db.GetBackupRunByID(runID)
	if err != nil {
		reg.logger.Error("finaliseRunStatus: fetch", "run_id", runID, "error", err)
		return
	}
	dbRun.Status = status
	dbRun.ErrorMessage = errMsg
	finished := time.Now().UTC().Format(time.RFC3339)
	dbRun.FinishedAt = &finished
	if err := reg.db.UpdateBackupRun(dbRun); err != nil {
		reg.logger.Error("finaliseRunStatus: update", "run_id", runID, "error", err)
	}
}
