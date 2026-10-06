package services

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// TestRunAutoUpdates_LockedStackIsSkipped is agent-os-a1ye.4's acceptance (b).
// Auto-apply used to update a container while a backup or lifecycle op held
// its stack. c1's stack is held, c2's is free, on the same pass: c1 must be
// skipped with no container touched and no history row, the skip must reach
// update_apply_last_error, and c2 must still be applied (the control that
// proves the pass ran at all).
func TestRunAutoUpdates_LockedStackIsSkipped(t *testing.T) {
	checker := &fakeUpdateChecker{updateFn: succeedingUpdate}
	svc := newApplyFixture(t, checker)
	lock := NewOperationLock()
	svc.SetOperationLock(lock)
	seedContainerPolicy(t, svc, "c1")
	seedContainerPolicy(t, svc, "c2")

	backupToken, err := lock.Acquire("s1", OpKindBackup)
	require.NoError(t, err)

	svc.RunAutoUpdates(context.Background(), []models.CachedUpdate{
		{ContainerID: "c1", ContainerName: "web", ImageRef: "nginx:latest", StackID: "s1"},
		{ContainerID: "c2", ContainerName: "api", ImageRef: "nginx:latest", StackID: "s2"},
	})

	assert.Equal(t, []string{"c2"}, checker.updatedIDs(),
		"the container on the locked stack must not be touched; the free one must be")

	_, n, err := svc.db.GetUpdateHistory(models.UpdateHistoryFilters{StackID: "s1"})
	require.NoError(t, err)
	assert.Zero(t, n, "a skipped update must leave no history row")
	_, n, err = svc.db.GetUpdateHistory(models.UpdateHistoryFilters{StackID: "s2"})
	require.NoError(t, err)
	assert.Equal(t, 1, n, "the applied update records its history row")

	msg, err := svc.db.GetSetting(applyLastErrorKey)
	require.NoError(t, err)
	assert.Equal(t, "1 auto-update(s) skipped: another operation in progress on stack s1; retried next pass", msg)

	// s2's lock was taken for the update and released after it.
	token, err := lock.Acquire("s2", OpKindStart)
	require.NoError(t, err, "auto-apply left s2 locked")
	lock.Release("s2", token)
	// The skip did not disturb the backup's hold on s1.
	_, err = lock.Acquire("s1", OpKindStart)
	require.Error(t, err, "the backup's lock on s1 was lost")
	lock.Release("s1", backupToken)
}
