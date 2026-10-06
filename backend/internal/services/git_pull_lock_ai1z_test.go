package services

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

// TestPullVerified_NoRedeployOnLockedStackIs409AndPullsNothing is
// agent-os-ai1z: a pull without redeploy still rewrites files under the stack
// directory, but it took the operation lock only when it would redeploy, so it
// could interleave with a backup or restore on the same stack.
func TestPullVerified_NoRedeployOnLockedStackIs409AndPullsNothing(t *testing.T) {
	svc, lock, work := pullLockFixture(t)
	before := headOf(t, work)
	backupToken, err := lock.Acquire("s1", OpKindBackup)
	require.NoError(t, err)

	ar, pullResult := svc.PullVerified(work, false, nil)

	require.Equal(t, truth.OutcomeFailed, ar.Outcome, "reason %q", ar.Reason)
	var appErr *models.AppError
	require.True(t, errors.As(ar.Err, &appErr), "err %v is not an AppError, so the handler would answer 500", ar.Err)
	assert.Equal(t, http.StatusConflict, appErr.Status)
	assert.Equal(t, models.ErrOperationInProgress, appErr.Code)
	assert.Contains(t, appErr.Message, "stack web: backup in progress since")
	assert.Nil(t, pullResult)
	assert.Equal(t, before, headOf(t, work), "the pull ran even though the stack was locked")
	_, statErr := os.Stat(filepath.Join(work, "README"))
	assert.True(t, os.IsNotExist(statErr), "the pulled file landed in the locked stack's directory (stat err %v)", statErr)

	_, err = lock.Acquire("s1", OpKindStart)
	require.Error(t, err, "the backup's lock was lost")
	lock.Release("s1", backupToken)
}

// TestPullVerified_NoRedeployOnFreeStackPullsAndReleases is the other side:
// stack free, so the pull lands and the lock is free afterwards.
func TestPullVerified_NoRedeployOnFreeStackPullsAndReleases(t *testing.T) {
	svc, lock, work := pullLockFixture(t)
	before := headOf(t, work)

	ar, _ := svc.PullVerified(work, false, nil)

	require.Equal(t, truth.OutcomeSuccess, ar.Outcome, "reason %q err %v", ar.Reason, ar.Err)
	assert.NotEqual(t, before, headOf(t, work), "the pull did not advance HEAD")
	token, err := lock.Acquire("s1", OpKindStart)
	require.NoError(t, err, "PullVerified left the stack locked")
	lock.Release("s1", token)
}

// TestPullVerified_FailedPullReleasesTheLock: a pull that fails (here a dirty
// work tree) must not leave the stacks locked, or every later operation on
// them answers 409 until the server restarts.
func TestPullVerified_FailedPullReleasesTheLock(t *testing.T) {
	for _, redeploy := range []bool{false, true} {
		svc, lock, work := pullLockFixture(t)
		writeStackFile(t, work, "compose.yml", "services: {dirty: {}}\n")

		ar, _ := svc.PullVerified(work, redeploy, &DockerService{})
		require.Equal(t, truth.OutcomeFailed, ar.Outcome, "redeploy=%v reason %q", redeploy, ar.Reason)
		var appErr *models.AppError
		require.True(t, errors.As(ar.Err, &appErr), "redeploy=%v err %v", redeploy, ar.Err)
		require.Equal(t, models.ErrGitDirty, appErr.Code, "redeploy=%v: the first pull failed for the wrong reason", redeploy)

		ar, _ = svc.PullVerified(work, redeploy, &DockerService{})
		require.True(t, errors.As(ar.Err, &appErr), "redeploy=%v err %v", redeploy, ar.Err)
		assert.NotEqual(t, models.ErrOperationInProgress, appErr.Code,
			"redeploy=%v: the failed pull left its lock held, so the next pull is a 409", redeploy)
		token, err := lock.Acquire("s1", OpKindStart)
		require.NoError(t, err, "redeploy=%v: PullVerified left the stack locked after a failed pull", redeploy)
		lock.Release("s1", token)
	}
}
