package services

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/errdefs"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

// agent-os-vush: a backup or restore that stops and restarts a stack stores
// the verified status of each step, as the REST lifecycle routes do. Before
// the fix the stored status stayed "running" through all of it.

func storedStatus(t *testing.T, db *database.DB, stackID string) string {
	t.Helper()
	st, err := db.GetStack(stackID)
	require.NoError(t, err)
	return st.Status
}

func TestBackup_StoresVerifiedStatusOfStopAndRestart_Vush(t *testing.T) {
	db := newBackupTestDB(t)
	docker := &fakeDocker{statusStr: "running", startPartial: "1 of 3 containers not running"}
	duringBackup := ""
	runner := &fakeRunner{onRun: func(name string, args []string, out chan<- StreamLine) {
		if len(args) > 0 && args[0] == "backup" {
			duringBackup = storedStatus(t, db, "myapp")
		}
		emitBackupSummary(name, args, out)
	}}
	svc := buildSvc(t, db, docker, runner, runner)
	seedStack(t, db, "myapp", "stop")

	out, closeOut := drainStream()
	_, err := svc.RunBackup(context.Background(), []string{"myapp"}, false, "manual", out)
	closeOut()
	t.Logf("RunBackup err (a partial restart may report one): %v", err)

	require.Equal(t, 1, docker.stopped(), "fixture: the stop policy stopped the stack")
	require.Equal(t, 1, docker.started(), "fixture: the stack was restarted")
	assert.Equal(t, "stopped", duringBackup, "a verified stop is stored while the backup runs")
	assert.Equal(t, "partial", storedStatus(t, db, "myapp"), "a partial restart is stored as partial")
}

// restoreErrRunnerVush fails only `restic restore`, so the restore stops the
// stack and then leaves it down on purpose.
type restoreErrRunnerVush struct{ *fakeRunner }

func (r restoreErrRunnerVush) Run(ctx context.Context, name string, args []string, env []string, out chan<- StreamLine) error {
	err := r.fakeRunner.Run(ctx, name, args, env, out)
	if len(args) > 0 && args[0] == "restore" {
		return errors.New("restic restore exploded")
	}
	return err
}

func TestRestore_FailedRestoreStoresStopped_Vush(t *testing.T) {
	db := newBackupTestDB(t)
	docker := &fakeDocker{statusStr: "running"}
	runner := restoreErrRunnerVush{&fakeRunner{outputData: snapshotJSON("abc123", "abc123", "myapp")}}
	svc := buildSvc(t, db, docker, runner, runner)
	seedStackAt(t, db, "myapp", t.TempDir())

	out, closeOut := drainStream()
	err := svc.RunRestore(context.Background(), "myapp", "abc123", out)
	closeOut()

	require.Error(t, err, "fixture: the restore failed")
	require.Equal(t, 1, docker.stopped(), "fixture: the stack was stopped before the restore")
	require.Equal(t, 0, docker.started(), "fixture: a failed restore leaves the stack down")
	assert.Equal(t, "stopped", storedStatus(t, db, "myapp"))
}

func TestRestore_PartialRestartStoresPartial_Vush(t *testing.T) {
	db := newBackupTestDB(t)
	docker := &fakeDocker{statusStr: "running", startPartial: "1 of 2 containers running"}
	runner := &fakeRunner{outputData: snapshotJSON("abc123", "abc123", "myapp")}
	svc := buildSvc(t, db, docker, runner, runner)
	seedStackAt(t, db, "myapp", t.TempDir())

	out, closeOut := drainStream()
	err := svc.RunRestore(context.Background(), "myapp", "abc123", out)
	closeOut()
	var incomplete *RestartIncompleteError
	require.ErrorAs(t, err, &incomplete, "fixture: the restart after the restore was partial")

	require.Equal(t, 1, docker.started(), "fixture: the stack was restarted")
	assert.Equal(t, "partial", storedStatus(t, db, "myapp"))
}

// A pull with redeploy restarts the requested stack through RestartVerified;
// the restart's verified status is stored. The fake docker exits 1, so the
// restart fails unverified and the stored status is "error". The sibling,
// which is not redeployed, keeps what it had.
func TestPullVerified_RedeployStoresVerifiedStatus_Vush(t *testing.T) {
	got := runSiblings(t, siblingsPull{
		files:    siblingFiles,
		changes:  map[string]string{"stackA/compose.yaml": composeNew4d60, "stackB/compose.yaml": composeNew4d60},
		stacks:   siblingStacks,
		pullDir:  "stackA",
		redeploy: true,
	})
	require.True(t, got.dockerSaw("A"), "fixture: stack A was redeployed; docker saw %q", got.docker)
	require.False(t, got.dockerSaw("B"), "fixture: stack B was not redeployed")
	assert.Equal(t, "error", storedStatus(t, got.db, "A"))
	assert.NotEqual(t, "error", storedStatus(t, got.db, "B"), "the sibling that was not redeployed keeps its status")
}

type recordingStatusStore struct{ stored []string }

func (r *recordingStatusStore) UpdateStackStatus(_ string, status string) error {
	r.stored = append(r.stored, status)
	return nil
}

// A refused compose (shared project name, a failed lookup) and a missing
// Docker service started nothing, so the stack's stored status stays as it
// was, as operations.go's persistStatus already does for a refused run
// (agent-os-vush review R1). Backup and git pull have no pre-check that
// stops them reaching the store with such a result.
func TestStoreLifecycleStatus_SkipsResultsThatStartedNothing_Vush(t *testing.T) {
	var nilDocker *DockerService
	nilAR, _ := nilDocker.StartVerified(models.Stack{ID: "s1"})
	cases := map[string]truth.ActionResult{
		"refused compose":   refusedCompose(errors.New("project name shared with another stack")),
		"refused ambiguous": refusedCompose(fmt.Errorf("lookup: %w", errdefs.ErrAmbiguous)),
		"no docker service": nilAR,
	}
	for name, ar := range cases {
		t.Run(name, func(t *testing.T) {
			store := &recordingStatusStore{}
			StoreLifecycleStatus(store, "s1", ar)
			assert.Empty(t, store.stored, "a result that started nothing must not overwrite the stored status")
		})
	}

	// Control on the same instrument: a failed run that did reach Docker is stored.
	store := &recordingStatusStore{}
	StoreLifecycleStatus(store, "s1", truth.Failed("could not verify stack state after stop", errors.New("ps failed")))
	assert.Equal(t, []string{"error"}, store.stored)
}
