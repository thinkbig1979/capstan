package services

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// TestBackupService_DockerUnavailable_ActionableError is the agent-os-ck4
// regression: POST /api/v1/backups/run used to finalise the run with
// errorMessage="panic: runtime error: invalid memory address or nil pointer
// dereference". A stack whose stop policy needs Docker must now fail with text
// an operator can act on.
//
// Both nil shapes are covered: a nil interface (no docker wired at all) and a
// nil *DockerService inside a non-nil interface (what main.go actually passes).
func TestBackupService_DockerUnavailable_ActionableError(t *testing.T) {
	t.Parallel()

	shapes := map[string]dockerStopper{
		"nil interface":      nil,
		"nil *DockerService": (*DockerService)(nil),
	}

	for name, docker := range shapes {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			db := newBackupTestDB(t)
			runner := &fakeRunner{outputData: snapshotJSON("abc123", "abc", "myapp")}
			svc := buildSvc(t, db, &fakeDocker{}, runner, runner)
			svc.docker = docker
			seedStack(t, db, "myapp", "stop")

			out := make(chan StreamLine, 256)

			var run *models.BackupRun
			var err error
			require.NotPanics(t, func() {
				run, err = svc.RunBackup(context.Background(), nil, false, "manual", out)
			})
			require.NoError(t, err)
			require.NotNil(t, run)

			assert.Equal(t, "failed", run.Status)
			assert.NotContains(t, run.ErrorMessage, "panic",
				"a Docker outage must not surface as a raw panic string")
			assert.Contains(t, strings.ToLower(run.ErrorMessage), "docker daemon unreachable")
			assert.Contains(t, run.ErrorMessage, "stop stack",
				"the message must say which step could not run")
		})
	}
}

// TestBackupRunnerRegistry_DockerUnavailable_PersistsActionableMessage is the
// agent-os-ck4 acceptance test at the layer the operator actually sees: it
// drives the same registry POST /api/v1/backups/run drives, then reads back the
// persisted BackupRun row that GET /api/v1/backups/runs/:runId returns.
//
// The bar is the message text, not merely the absence of a panic: the reported
// symptom was errorMessage="panic: runtime error: invalid memory address or nil
// pointer dereference" with stacksTotal=0, which tells an operator nothing. A
// run that fails with an empty ErrorMessage would fail this task just as surely.
func TestBackupRunnerRegistry_DockerUnavailable_PersistsActionableMessage(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	runner := &fakeRunner{outputData: snapshotJSON("abc123", "abc", "myapp")}
	svc := buildSvc(t, db, &fakeDocker{}, runner, runner)
	svc.docker = (*DockerService)(nil) // what main.go passes during an outage
	seedStack(t, db, "myapp", "stop")

	reg := NewBackupRunnerRegistry(db, svc, slog.Default())
	t.Cleanup(reg.Stop)

	runID, err := reg.LaunchBackup(nil, false)
	require.NoError(t, err)

	// LaunchBackup detaches; Attach blocks until the run goroutine finishes.
	res, err := reg.Attach(runID, make(chan struct{}))
	require.NoError(t, err)
	if res != nil && res.Live != nil {
		for range res.Live {
		}
	}

	row, err := db.GetBackupRunByID(runID)
	require.NoError(t, err)
	require.NotNil(t, row)

	assert.Equal(t, "failed", row.Status)
	assert.NotContains(t, row.ErrorMessage, "panic",
		"the recovered-panic path must not be reached at all")
	assert.NotEmpty(t, row.ErrorMessage,
		"a failed run with no message is as unactionable as a panic string")
	assert.Contains(t, strings.ToLower(row.ErrorMessage), "docker daemon unreachable")
	assert.Equal(t, 1, row.StacksTotal,
		"the run must get far enough to count its targets, unlike the panic path (stacksTotal=0)")
}
