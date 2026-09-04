package services

import (
	"log/slog"
	"os"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// TestAttach_InterruptedRun_ReportsSweptOutcome pins the fix for agent-os-pid:
// a run whose backup_runs row was swept to 'interrupted' at startup (see
// database.SweepInterruptedBackupRuns) must be reported by Attach's DB
// fallback path with its real status and error_message, not silently
// rewritten to the generic "failed" / "operation state lost" fallback that
// exists for a genuinely-unrecognized status.
//
// This exercises exactly the path a client hits reconnecting after a server
// restart: the in-memory registry is empty (it never persists across
// restarts, see BackupRunnerRegistry.runs), so every run from a previous
// process — swept or not — falls through Attach's "not in registry" branch to
// the DB lookup.
func TestAttach_InterruptedRun_ReportsSweptOutcome(t *testing.T) {
	db := newBackupTestDB(t)

	finished := "2026-01-01T00:05:00Z"
	run := &models.BackupRun{
		ID:           "run-swept",
		Kind:         "backup",
		Trigger:      "scheduled",
		Status:       "interrupted",
		StartedAt:    "2026-01-01T00:00:00Z",
		FinishedAt:   &finished,
		ErrorMessage: "process stopped before this run completed",
	}
	require.NoError(t, db.CreateBackupRun(run))

	// svc is nil deliberately: Attach's "not in registry, fall back to DB"
	// branch never touches reg.svc, only reg.db — see backup_runner.go:487-514.
	reg := NewBackupRunnerRegistry(db, nil, slog.Default())
	t.Cleanup(reg.Stop)

	result, err := reg.Attach("run-swept", nil)
	require.NoError(t, err)
	assert.True(t, result.Done)
	assert.Equal(t, "interrupted", result.Outcome,
		"the sweep's real status must be reported, not overwritten to the generic 'failed' fallback")
	assert.Equal(t, "process stopped before this run completed", result.Reason,
		"the sweep's own error_message must be reported, not the generic 'operation state lost' fallback reason")
}

// TestAttach_UnknownRunningRow_UsesGenericFallback is the control case:
// a row that is still genuinely 'running' in the DB with nothing in the
// registry (the actual "server restarted mid-run, in-memory state is gone"
// case the fallback message describes) must still get the generic message.
// This guards against a fix for the 'interrupted' case accidentally widening
// isTerminal to swallow the real "state lost" scenario too.
func TestAttach_UnknownRunningRow_UsesGenericFallback(t *testing.T) {
	db := newBackupTestDB(t)

	run := &models.BackupRun{
		ID:        "run-orphaned",
		Kind:      "backup",
		Trigger:   "manual",
		Status:    "running",
		StartedAt: "2026-01-01T00:00:00Z",
	}
	require.NoError(t, db.CreateBackupRun(run))

	reg := NewBackupRunnerRegistry(db, nil, slog.Default())
	t.Cleanup(reg.Stop)

	result, err := reg.Attach("run-orphaned", nil)
	require.NoError(t, err)
	assert.True(t, result.Done)
	assert.Equal(t, "failed", result.Outcome)
	assert.Equal(t, "operation state lost (server may have restarted)", result.Reason)
}

// ============================================================
// jtax-shape orphan-forwarder leak arm (agent-os-o1jp.3, criterion 3)
// ============================================================
//
// Attach starts a forwardLive goroutine for every still-running run it
// hands to a caller (backup_runner.go:657). That goroutine is not tracked by
// reg.wg and exits only when dr.done closes or clientGone closes
// (backup_runner.go:710-745). A caller that passes a nil clientGone gets a
// channel that is never closed (backup_runner.go:653-655, "safe default"),
// so the only thing standing between that goroutine and a permanent leak is
// dr.done — i.e. the run actually finishing. agent-os-jtax (not yet merged,
// owned by a different session) is about exactly this shape.
//
// testing/synctest makes that leak a hard, mechanical failure instead of an
// argument: inside a bubble, a goroutine still durably blocked when the
// bubble's root goroutine exits is a deadlock, and synctest.Test panics with
// a fixed string. Both arms below use the REAL BackupRunnerRegistry.Attach,
// the same construction, and no database (db is nil — evictFinished is never
// reached because beginStop closes gcStop, and gcLoop's select wakes on that
// close without needing a time advance, so the nil db is never dereferenced;
// this also excludes the OTHER cause of the same panic string, an
// unclosed :memory: DB's connection-opener goroutine — see criterion 2's fix
// to newTestBackupScheduler in backup_scheduler_test.go).

// newAttachLeakProbeRegistry builds the identical registry construction used
// by both arms below, so the only difference between them is the run state
// handed to Attach.
func newAttachLeakProbeRegistry(t *testing.T) *BackupRunnerRegistry {
	t.Helper()
	reg := NewBackupRunnerRegistry(nil, nil, slog.Default())
	t.Cleanup(reg.Stop)
	return reg
}

// TestAttach_JtaxOrphanForwarder_ControlAlreadyDoneRunPasses is the CONTROL
// half of the discriminating pair: Attach on an already-DONE run returns
// synchronously (backup_runner.go:641-649) without starting any goroutine,
// so the bubble has nothing left blocked when the test returns and passes
// cleanly. This is what proves the TEST ARM's panic below is attributable to
// the forwarder Attach started, and not to synctest objecting to concurrency
// in general.
func TestAttach_JtaxOrphanForwarder_ControlAlreadyDoneRunPasses(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		reg := newAttachLeakProbeRegistry(t)

		done := make(chan struct{})
		close(done)
		reg.runs["probe-done"] = &durableRun{
			runID:   "probe-done",
			done:    done,
			outcome: "success",
		}

		result, err := reg.Attach("probe-done", nil)
		require.NoError(t, err)
		assert.True(t, result.Done, "an already-done run must report Done synchronously")
		assert.Equal(t, "success", result.Outcome)
	})
}

// TestAttach_JtaxOrphanForwarder_TestArmFailsPreJtax is the TEST ARM: a
// STILL-RUNNING run attached with a nil clientGone (this is the jtax shape —
// see agent-os-jtax). Attach starts forwardLive, which will only ever exit on
// dr.done or clientGone, and this arm closes neither. Inside the bubble that
// goroutine is durably blocked forever, so once this test function returns
// synctest.Test panics with "deadlock: main bubble goroutine has exited but
// blocked goroutines remain" — the same string criterion 2 warns can also
// come from an unclosed :memory: DB, which is why this arm's registry has no
// DB at all (see the section header comment above).
//
// This is DELIBERATELY, PERMANENTLY red until agent-os-jtax merges and fixes
// Attach to not orphan the forwarder when clientGone is nil. That is why it
// is gated behind an opt-in env var rather than running by default: a Go
// test binary does not recover from a synctest deadlock panic the way it
// recovers from a normal t.Fail — the panic unwinds the whole test process,
// so every test after this one in the package's declaration order would
// silently never run (VERIFIED empirically 2026-09-04: a probe package with
// A/leak/C showed A passing, the leak panicking, and C never printing so
// much as "=== RUN"). Embedding this unconditionally would make
// `go test ./internal/services/...` stop being a report of every test's
// result and start being a report of "everything up to the first
// alphabetically-early red", which is a worse failure mode than the leak
// this bead exists to catch. See CAPSTAN_ALLOW_DESTRUCTIVE_IMAGE_PRUNE in
// backend/internal/integrationtest/resources_test.go for the identical
// opt-in-skip pattern already used in this repo for a test that is
// correct-but-unsafe-by-default.
//
// To reproduce the failing evidence on demand:
//
//	CAPSTAN_RUN_JTAX_LEAK_PROBE=1 go test ./internal/services/ \
//	  -run TestAttach_JtaxOrphanForwarder_TestArmFailsPreJtax -v
//
// Flip this test back to running unconditionally once agent-os-jtax merges
// and this goes green — that is the trigger this bead's parent epic notes
// as the reason it cannot close yet.
func TestAttach_JtaxOrphanForwarder_TestArmFailsPreJtax(t *testing.T) {
	if os.Getenv("CAPSTAN_RUN_JTAX_LEAK_PROBE") == "" {
		t.Skip("skipping: this test is a synctest deadlock panic that crashes the whole test binary, not a normal t.Fail — it is the pre-agent-os-jtax red half of criterion 3 on agent-os-o1jp.3, and stays red until jtax merges. Set CAPSTAN_RUN_JTAX_LEAK_PROBE=1 to reproduce it on demand (see this test's doc comment), or leave unset to let the rest of the suite run.")
	}

	synctest.Test(t, func(t *testing.T) {
		reg := newAttachLeakProbeRegistry(t)

		reg.runs["probe-run"] = &durableRun{
			runID: "probe-run",
			done:  make(chan struct{}), // never closed: the run "never finishes"
		}

		// clientGone: nil is the jtax shape — Attach replaces it with a
		// never-closed channel (backup_runner.go:653-655), so forwardLive's
		// only remaining exit, dr.done, never fires either.
		_, err := reg.Attach("probe-run", nil)
		require.NoError(t, err, "Attach itself must not error; the leak is in the goroutine it starts")

		// No assertion below this point is reachable pre-jtax: synctest.Test
		// panics when this function returns, because forwardLive is still
		// durably blocked.
	})
}
