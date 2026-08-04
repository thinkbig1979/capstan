package services

import (
	"log/slog"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// dbCall records a single write this test's spyRunStore observed, in the
// order it happened.
type dbCall struct {
	method string
	runID  string
	status string
}

// spyRunStore wraps a real *database.DB and satisfies backupRunStore (see
// backup_runner.go), recording every CreateBackupRun/UpdateBackupRun call it
// forwards. It is the seam agent-os-13k adds: BackupRunnerRegistry's db field
// only requires these three methods, so a spy can substitute for
// *database.DB — which has no injection point of its own — without touching
// any production call site.
//
// Recording call order under a mutex (rather than reading the row back and
// inspecting its value at some later wall-clock moment) is what makes the
// assertion built on top of this deterministic: it does not matter how fast
// the exec goroutine races to finalise the row, because every write it makes
// is captured, in true order, before the test ever inspects the log.
type spyRunStore struct {
	real *database.DB

	mu    sync.Mutex
	calls []dbCall
}

func (s *spyRunStore) CreateBackupRun(r *models.BackupRun) error {
	s.record("CreateBackupRun", r.ID, r.Status)
	return s.real.CreateBackupRun(r)
}

func (s *spyRunStore) UpdateBackupRun(r *models.BackupRun) error {
	s.record("UpdateBackupRun", r.ID, r.Status)
	return s.real.UpdateBackupRun(r)
}

func (s *spyRunStore) GetBackupRunByID(id string) (*models.BackupRun, error) {
	return s.real.GetBackupRunByID(id)
}

func (s *spyRunStore) record(method, runID, status string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, dbCall{method: method, runID: runID, status: status})
}

func (s *spyRunStore) snapshot() []dbCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]dbCall, len(s.calls))
	copy(out, s.calls)
	return out
}

// TestLaunchSync_RunRowCreatedRunningBeforeFinalisation is the agent-os-13k
// regression test for an invariant no handler-level test can check
// deterministically (see agent-os-icp): a durable run row must be persisted
// with status "running" strictly before it is ever finalised to a terminal
// status — i.e. creation must happen, and must happen as "running", before
// the exec goroutine's finalising write.
//
// This deliberately does NOT read the row back and inspect its value at a
// point in time — that races the exec goroutine, which finalises within
// microseconds when rclone is unconfigured (as it is here; see agent-os-icp,
// where exactly that race forced a handler-level assertion to be weakened to
// assert.NotEmpty, which the CHECK constraint on backup_runs.status makes
// unable to ever fail). Instead it substitutes spyRunStore for
// BackupRunnerRegistry's db field and records every write, in true call
// order, under a mutex. reg.Stop() then blocks until the exec goroutine —
// including its finalising DB write — has fully completed (see Stop's doc
// comment / agent-os-80n), so by the time the test inspects the log, every
// write is present and in the order it truly happened, regardless of how the
// scheduler interleaved goroutines to get there.
func TestLaunchSync_RunRowCreatedRunningBeforeFinalisation(t *testing.T) {
	db := newBackupTestDB(t)
	spy := &spyRunStore{real: db}

	// cfg.RcloneRemote is deliberately left unset and no rclone_remote DB
	// setting is seeded, so RunSync's runSyncInternal fails fast with "rclone
	// remote is not configured" before it would ever shell out to rclone or
	// touch a stack. This makes execSync's finalisation fast and needs no
	// fake command runner, no seeded stack, and no restic/rclone binary.
	svc := buildSvc(t, db, &fakeDocker{}, &fakeRunner{}, &fakeRunner{})

	reg := NewBackupRunnerRegistry(spy, svc, slog.Default())

	runID, err := reg.LaunchSync()
	require.NoError(t, err)

	// Blocks until execSync (and its finaliseRunStatus DB write) has fully
	// completed.
	reg.Stop()

	calls := spy.snapshot()
	require.NotEmpty(t, calls, "expected at least one DB write for this run")

	require.Equal(t, "CreateBackupRun", calls[0].method,
		"the run row's creation must be the first DB write for this run")
	assert.Equal(t, runID, calls[0].runID)
	assert.Equal(t, "running", calls[0].status,
		"the row must be created with status running, before the exec goroutine can finalise it")

	for i, c := range calls[1:] {
		assert.NotEqual(t, "CreateBackupRun", c.method,
			"the run row must be created exactly once, at position 0 (found a second CreateBackupRun at index %d)", i+1)
	}

	require.Greater(t, len(calls), 1,
		"expected a finalising write after creation (this test's fixture makes RunSync fail fast)")
	final := calls[len(calls)-1]
	assert.Equal(t, "UpdateBackupRun", final.method,
		"the run must be finalised via UpdateBackupRun after creation")
	assert.NotEqual(t, "running", final.status,
		"finalisation must move the row to a terminal status, not leave it (or re-write it) as running")
}

// TestLaunchBackup_CreatesRunningRowBeforeFinalisation_ResticMissingFastFail is
// the agent-os-14f counterpart to TestLaunchSync_RunRowCreatedRunningBeforeFinalisation
// above, for the BACKUP kind — but ONLY for the branch named in the test:
// RunBackupWithRunID failing at its resticBin-missing guard.
//
// SCOPE, READ BEFORE EXTENDING THIS TEST: the backup kind's normal
// finalisation path is NOT covered here and is NOT observable through this
// seam. RunBackupWithRunID (backup.go:558) builds its own in-memory
// *models.BackupRun, accumulates per-stack stats into it, and — on the
// restic-present path — writes it once via BackupService.finaliseRun
// (backup.go:1127), which uses svc.db directly, never reg.db. spyRunStore
// only substitutes for BackupRunnerRegistry.db, so that write is invisible
// to it (OBSERVED: a throwaway probe — LaunchBackup with resticBin left at
// buildSvc's default "/usr/bin/restic" — logged spy.snapshot() containing
// only the initial CreateBackupRun call while db.GetBackupRunByID(runID)
// showed the real row finalised to status "success"; not committed, see
// agent-os-14f report for the exact log lines). BackupService.db also can't
// be narrowed to the same backupRunStore interface without splitting it,
// since it's used for far more than run status (e.g. resolveBackupConfig at
// backup.go:575, AddBackupRunItem at backup.go:1121).
//
// What IS covered: when RunBackupWithRunID fails at its very first check
// (resticBin == "", backup.go:571-573) it returns a nil *models.BackupRun
// before ever touching svc.db. execBackup (backup_runner.go:373-379)
// special-cases exactly that — "if run == nil" — falling back to
// reg.finaliseRunStatus, the same registry-owned, spy-observable path
// RunSync/RunRestore/RunDRRestore/RunPrune always use. This test drives that
// branch via the existing SetBins("", "") setter (mirroring how
// TestLaunchSync_* above leaves RcloneRemote unset to force its own
// fast-fail), so create-then-finalise ordering is asserted through a real,
// existing production code path on this one branch — no seam widening was
// needed to write it, and none should be inferred for the restic-present
// path from this test passing.
func TestLaunchBackup_CreatesRunningRowBeforeFinalisation_ResticMissingFastFail(t *testing.T) {
	db := newBackupTestDB(t)
	spy := &spyRunStore{real: db}

	svc := buildSvc(t, db, &fakeDocker{}, &fakeRunner{}, &fakeRunner{})
	// Force RunBackupWithRunID's very first check (resticBin == "") to fail,
	// so it returns before constructing its run struct or touching svc.db at
	// all — the only branch of the backup kind whose finalisation still
	// routes through reg.finaliseRunStatus (and hence through spy).
	svc.SetBins("", "")

	reg := NewBackupRunnerRegistry(spy, svc, slog.Default())

	runID, err := reg.LaunchBackup(nil, false)
	require.NoError(t, err)

	// Blocks until execBackup (and its finalising DB write) has fully
	// completed.
	reg.Stop()

	calls := spy.snapshot()
	require.NotEmpty(t, calls, "expected at least one DB write for this run")

	require.Equal(t, "CreateBackupRun", calls[0].method,
		"the run row's creation must be the first DB write for this run")
	assert.Equal(t, runID, calls[0].runID)
	assert.Equal(t, "running", calls[0].status,
		"the row must be created with status running, before the exec goroutine can finalise it")

	for i, c := range calls[1:] {
		assert.NotEqual(t, "CreateBackupRun", c.method,
			"the run row must be created exactly once, at position 0 (found a second CreateBackupRun at index %d)", i+1)
	}

	require.Greater(t, len(calls), 1,
		"expected a finalising write after creation (this test's fixture makes RunBackupWithRunID fail fast)")
	final := calls[len(calls)-1]
	assert.Equal(t, "UpdateBackupRun", final.method,
		"the run must be finalised via UpdateBackupRun after creation")
	assert.NotEqual(t, "running", final.status,
		"finalisation must move the row to a terminal status, not leave it (or re-write it) as running")
}
