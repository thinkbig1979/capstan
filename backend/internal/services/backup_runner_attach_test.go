package services

import (
	"log/slog"
	"testing"

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
