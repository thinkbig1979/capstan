package services

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// blockRemoval swaps the staged snapshot file for a non-empty directory, so the
// deferred os.Remove(dest) in backupDatabase fails with ENOTEMPTY. A directory
// is used rather than chmod because chmod does not stop root from removing a
// file, and the test must hold wherever it runs.
func blockRemoval(t *testing.T, dest string) {
	t.Helper()
	require.NoError(t, os.Remove(dest))
	require.NoError(t, os.MkdirAll(dest, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dest, "keep"), []byte("x"), 0o600))
	t.Cleanup(func() { _ = os.RemoveAll(dest) })
}

// TestBackupDatabase_ResticAndRemovalBothFail_BothReachTheReturnedError pins
// agent-os-d6om: when the restic backup of the snapshot fails AND the staged
// plaintext copy cannot be removed, the removal failure used to reach only the
// live stream, so no durable record said a full plaintext database was left on
// disk. Both failures must be in the error that becomes the run's database reason.
func TestBackupDatabase_ResticAndRemovalBothFail_BothReachTheReturnedError(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	resticErr := errors.New("injected restic failure")

	var svc *BackupService
	runner := &fakeRunner{runErr: resticErr}
	svc = buildSvc(t, db, &fakeDocker{}, runner, runner)
	// onRun fires inside restic.Backup, after the defer is registered.
	runner.onRun = func(_ string, _ []string, _ chan<- StreamLine) {
		blockRemoval(t, svc.DatabaseSnapshotPath())
	}

	out := drainedOut(t)
	_, err := svc.backupDatabase(context.Background(), svc.resticMgrFactory(BackupConfig{}), false, out)

	require.Error(t, err)
	assert.ErrorIs(t, err, resticErr, "the restic failure must still be in the returned error")
	assert.Contains(t, err.Error(), "restic backup database")
	assert.Contains(t, err.Error(), "remove staged snapshot",
		"the staged-copy removal failure must reach the returned error, not only the stream")
	assert.NotContains(t, err.Error(), "\n",
		"the run's database reason is stored as one line")
}

// TestBackupDatabase_OnlyRemovalFails_ReturnsRemovalError guards the success
// path: a removal failure alone is still the error.
func TestBackupDatabase_OnlyRemovalFails_ReturnsRemovalError(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)

	var svc *BackupService
	runner := &fakeRunner{}
	svc = buildSvc(t, db, &fakeDocker{}, runner, runner)
	runner.onRun = func(_ string, _ []string, _ chan<- StreamLine) {
		blockRemoval(t, svc.DatabaseSnapshotPath())
	}

	out := drainedOut(t)
	_, err := svc.backupDatabase(context.Background(), svc.resticMgrFactory(BackupConfig{}), false, out)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "remove staged snapshot")
	assert.NotContains(t, err.Error(), "restic backup database")
}
